package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"sidekick/domain"
	"sidekick/iroh"
	"sidekick/srv"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"github.com/segmentio/ksuid"
	irohlib "github.com/tmc/go-iroh/iroh"
)

// currentRemoteTicket holds the iroh connection ticket of the running remote
// server, so pairing handlers on the local HTTP server can hand it out for QR
// rendering without holding a direct reference to the endpoint.
var currentRemoteTicket atomic.Value // string

func setRemoteTicket(ticket string) {
	currentRemoteTicket.Store(ticket)
}

func getRemoteTicket() string {
	if v := currentRemoteTicket.Load(); v != nil {
		return v.(string)
	}
	return ""
}

// generateDeviceToken returns a high-entropy, URL-safe token suitable for
// authenticating a paired device. The plaintext is only ever shown once during
// pairing; the server persists only its hash.
func generateDeviceToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate device token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// hashDeviceToken returns the hex-encoded SHA-256 hash of a device token. The
// token has full entropy, so a fast hash (rather than a password KDF) is
// sufficient to prevent recovering the token from the stored hash.
func hashDeviceToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// bearerToken extracts the token from an Authorization header value of the form
// "Bearer <token>", returning an empty string when absent or malformed.
func bearerToken(header string) string {
	const prefix = "Bearer "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

// DeviceAuthMiddleware authenticates requests by matching the SHA-256 hash of
// the presented bearer token against a paired RemoteDevice, updating the
// device's last-used timestamp on success and rejecting unknown tokens with a
// 401.
func DeviceAuthMiddleware(service srv.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := bearerToken(c.GetHeader("Authorization"))
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing device token"})
			return
		}

		device, err := service.GetRemoteDeviceByTokenHash(c.Request.Context(), hashDeviceToken(token))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid device token"})
			return
		}

		if err := service.UpdateRemoteDeviceLastUsed(c.Request.Context(), device.Id, time.Now()); err != nil {
			log.Error().Err(err).Str("deviceId", device.Id).Msg("failed to update remote device last used")
		}

		c.Next()
	}
}

// blockRemotePairingMiddleware rejects any request targeting the local-only
// pairing management routes, ensuring remote devices cannot create, list, or
// revoke pairings over iroh.
func blockRemotePairingMiddleware() gin.HandlerFunc {
	const pairingPrefix = "/api/v1/remote/pairings"
	return func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, pairingPrefix) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "pairing management is not available over the remote connection"})
			return
		}
		c.Next()
	}
}

type CreatePairingRequest struct {
	Name string `json:"name"`
}

type CreatePairingResponse struct {
	Device domain.RemoteDevice `json:"device"`
	Ticket string              `json:"ticket"`
	Token  string              `json:"token"`
}

// CreatePairingHandler mints a new device token, persists the paired device
// (storing only the token hash), and returns the iroh ticket together with the
// one-time plaintext token for QR-based pairing.
func (ctrl *Controller) CreatePairingHandler(c *gin.Context) {
	var req CreatePairingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Unnamed device"
	}

	ticket := getRemoteTicket()
	if ticket == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "remote server is not running"})
		return
	}

	token, err := generateDeviceToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate device token"})
		return
	}

	now := time.Now()
	device := domain.RemoteDevice{
		Id:        ksuid.New().String(),
		Name:      name,
		TokenHash: hashDeviceToken(token),
		Created:   now,
		LastUsed:  time.Time{},
	}

	if err := ctrl.service.CreateRemoteDevice(c.Request.Context(), device); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create paired device"})
		return
	}

	c.JSON(http.StatusCreated, CreatePairingResponse{
		Device: device,
		Ticket: ticket,
		Token:  token,
	})
}

// ListPairingsHandler returns all currently paired devices. Token hashes are
// part of the RemoteDevice payload but the plaintext token is never persisted,
// so it cannot be recovered here.
func (ctrl *Controller) ListPairingsHandler(c *gin.Context) {
	devices, err := ctrl.service.GetAllRemoteDevices(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list paired devices"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"devices": devices})
}

// DeletePairingHandler unpairs a device, immediately revoking its token.
func (ctrl *Controller) DeletePairingHandler(c *gin.Context) {
	id := c.Param("id")
	if err := ctrl.service.DeleteRemoteDevice(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete paired device"})
		return
	}
	c.Status(http.StatusNoContent)
}

// RemoteServer serves the Sidekick REST API over an iroh endpoint, gated by
// per-device token authentication.
type RemoteServer struct {
	httpServer *http.Server
	endpoint   *iroh.Endpoint
}

// Ticket returns the iroh connection ticket that remote devices use to dial
// this server.
func (s *RemoteServer) Ticket() string {
	return s.endpoint.Ticket()
}

// Shutdown gracefully stops serving over iroh and closes the endpoint.
func (s *RemoteServer) Shutdown(ctx context.Context) error {
	setRemoteTicket("")
	err := s.httpServer.Shutdown(ctx)
	if closeErr := s.endpoint.Close(ctx); closeErr != nil && err == nil {
		err = closeErr
	}
	return err
}

// RunRemoteServer starts serving the existing REST API over an iroh endpoint in
// a background goroutine, wrapping every route with device-token
// authentication. The returned RemoteServer exposes the iroh ticket and a
// matching Shutdown.
func RunRemoteServer(ctx context.Context) (*RemoteServer, error) {
	allowedOrigins, err := GetAllowedOrigins()
	if err != nil {
		return nil, fmt.Errorf("failed to parse allowed origins: %w", err)
	}

	ctrl, err := NewController()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize controller: %w", err)
	}

	return serveRemote(ctx, ctrl, allowedOrigins)
}

// serveRemote wraps the existing REST API with device-token authentication and
// serves it over an iroh endpoint. It is split from RunRemoteServer so tests can
// exercise the full serving path with a test controller. Additional iroh
// endpoint options may be supplied, e.g. to bind a loopback address in tests.
func serveRemote(ctx context.Context, ctrl Controller, allowedOrigins *AllowedOrigins, endpointOpts ...irohlib.Option) (*RemoteServer, error) {
	gin.SetMode(gin.ReleaseMode)

	apiRouter := DefineRoutes(ctrl, allowedOrigins)

	// A thin outer engine runs the device-auth middleware before delegating to
	// the full API router, so authentication applies to every route without
	// re-registering handlers. Pairing management is a local-only capability, so
	// it is blocked here to prevent remote devices from minting new tokens.
	authRouter := gin.New()
	authRouter.Use(DeviceAuthMiddleware(ctrl.service))
	authRouter.Use(blockRemotePairingMiddleware())
	authRouter.NoRoute(gin.WrapH(apiRouter.Handler()))

	endpoint, err := iroh.NewEndpoint(ctx, endpointOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to start iroh endpoint: %w", err)
	}
	setRemoteTicket(endpoint.Ticket())

	httpServer := &http.Server{Handler: authRouter.Handler()}
	listener := endpoint.Listener()

	go func() {
		if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("iroh remote server stopped")
		}
	}()

	log.Info().Str("nodeId", endpoint.NodeID()).Msg("Starting remote (iroh) API server")

	return &RemoteServer{
		httpServer: httpServer,
		endpoint:   endpoint,
	}, nil
}
