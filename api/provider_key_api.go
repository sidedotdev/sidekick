package api

import (
	"fmt"
	"net/http"
	"sidekick/domain"
	"sidekick/secret_manager"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/segmentio/ksuid"
)

// ProviderKeyRequest represents the request body for creating/updating a provider key
type ProviderKeyRequest struct {
	Nickname     *string                          `json:"nickname"`
	ProviderType domain.ProviderType              `json:"providerType"`
	KeyValue     string                           `json:"keyValue,omitempty"`
	SecretType   secret_manager.SecretManagerType `json:"secretType"`
}

// ProviderKeyResponse represents the response body for provider key operations
// Note: Never includes the actual key value for security
type ProviderKeyResponse struct {
	Id                string                           `json:"id"`
	Nickname          *string                          `json:"nickname"`
	ProviderType      domain.ProviderType              `json:"providerType"`
	SecretManagerType secret_manager.SecretManagerType `json:"secretManagerType"`
	Created           time.Time                        `json:"created"`
	Updated           time.Time                        `json:"updated"`
}

// DefineProviderKeyApiRoutes sets up the API routes for provider key management
func DefineProviderKeyApiRoutes(r *gin.Engine, ctrl *Controller) *gin.RouterGroup {
	providerKeyApiRoutes := r.Group("/api/v1/provider_keys")
	providerKeyApiRoutes.POST("/", ctrl.CreateProviderKeyHandler)
	providerKeyApiRoutes.GET("/", ctrl.GetProviderKeysHandler)
	providerKeyApiRoutes.GET("/:keyId", ctrl.GetProviderKeyByIdHandler)
	providerKeyApiRoutes.PUT("/:keyId", ctrl.UpdateProviderKeyHandler)
	providerKeyApiRoutes.DELETE("/:keyId", ctrl.DeleteProviderKeyHandler)
	return providerKeyApiRoutes
}

// CreateProviderKeyHandler handles the creation of a new provider key
func (ctrl *Controller) CreateProviderKeyHandler(c *gin.Context) {
	var keyReq ProviderKeyRequest
	if err := c.ShouldBindJSON(&keyReq); err != nil {
		ctrl.ErrorHandler(c, http.StatusBadRequest, err)
		return
	}

	if keyReq.KeyValue == "" {
		ctrl.ErrorHandler(c, http.StatusBadRequest, fmt.Errorf("key value is required"))
		return
	}

	if keyReq.ProviderType == "" {
		ctrl.ErrorHandler(c, http.StatusBadRequest, fmt.Errorf("provider type is required"))
		return
	}

	if keyReq.SecretType == "" {
		// default to keyring secret manager (which is the only one you can use
		// to create via the API, for now)
		keyReq.SecretType = secret_manager.KeyringSecretManagerType
	}

	providerKey := domain.ProviderKey{
		Id:                "pk_" + ksuid.New().String(),
		Nickname:          keyReq.Nickname,
		ProviderType:      keyReq.ProviderType,
		SecretManagerType: keyReq.SecretType,
		SecretName:        "key_" + ksuid.New().String(),
		Created:           time.Now(),
		Updated:           time.Now(),
	}

	if err := providerKey.Validate(); err != nil {
		ctrl.ErrorHandler(c, http.StatusBadRequest, err)
		return
	}

	// Get secret manager based on the type
	secretManager := secret_manager.GetSecretManager(keyReq.SecretType)

	// Store the actual key value in the secret manager
	if err := secretManager.SetSecret(providerKey.SecretName, keyReq.KeyValue); err != nil {
		ctrl.ErrorHandler(c, http.StatusInternalServerError, fmt.Errorf("failed to store key in secret manager: %w", err))
		return
	}

	// Store the provider key metadata
	if err := ctrl.service.PersistProviderKey(c, providerKey); err != nil {
		// Attempt to clean up the secret if metadata storage fails
		_ = secretManager.DeleteSecret(providerKey.SecretName)
		ctrl.ErrorHandler(c, http.StatusInternalServerError, fmt.Errorf("failed to store provider key metadata: %w", err))
		return
	}

	response := ProviderKeyResponse{
		Id:                providerKey.Id,
		Nickname:          providerKey.Nickname,
		ProviderType:      providerKey.ProviderType,
		SecretManagerType: providerKey.SecretManagerType,
		Created:           providerKey.Created,
		Updated:           providerKey.Updated,
	}

	c.JSON(http.StatusOK, gin.H{"providerKey": response})
}

// GetProviderKeysHandler handles the request for listing all provider keys
func (ctrl *Controller) GetProviderKeysHandler(c *gin.Context) {
	keys, err := ctrl.service.GetAllProviderKeys(c)
	if err != nil {
		ctrl.ErrorHandler(c, http.StatusInternalServerError, fmt.Errorf("failed to retrieve provider keys: %w", err))
		return
	}

	if keys == nil {
		keys = []domain.ProviderKey{}
	}

	responses := make([]ProviderKeyResponse, len(keys))
	for i, key := range keys {
		responses[i] = ProviderKeyResponse{
			Id:                key.Id,
			Nickname:          key.Nickname,
			ProviderType:      key.ProviderType,
			SecretManagerType: key.SecretManagerType,
			Created:           key.Created,
			Updated:           key.Updated,
		}
	}

	c.JSON(http.StatusOK, gin.H{"providerKeys": responses})
}

// GetProviderKeyByIdHandler handles the request for getting a specific provider key
func (ctrl *Controller) GetProviderKeyByIdHandler(c *gin.Context) {
	keyId := c.Param("keyId")
	key, err := ctrl.service.GetProviderKey(c, keyId)
	if err != nil {
		ctrl.ErrorHandler(c, http.StatusNotFound, err)
		return
	}

	response := ProviderKeyResponse{
		Id:                key.Id,
		Nickname:          key.Nickname,
		ProviderType:      key.ProviderType,
		SecretManagerType: key.SecretManagerType,
		Created:           key.Created,
		Updated:           key.Updated,
	}

	c.JSON(http.StatusOK, gin.H{"providerKey": response})
}

// UpdateProviderKeyHandler handles updates to a provider key
func (ctrl *Controller) UpdateProviderKeyHandler(c *gin.Context) {
	keyId := c.Param("keyId")
	var keyReq ProviderKeyRequest
	if err := c.ShouldBindJSON(&keyReq); err != nil {
		ctrl.ErrorHandler(c, http.StatusBadRequest, err)
		return
	}

	key, err := ctrl.service.GetProviderKey(c, keyId)
	if err != nil {
		ctrl.ErrorHandler(c, http.StatusNotFound, err)
		return
	}

	// Only allow updating the nickname
	key.Nickname = keyReq.Nickname
	key.Updated = time.Now()

	if err := ctrl.service.PersistProviderKey(c, key); err != nil {
		ctrl.ErrorHandler(c, http.StatusInternalServerError, fmt.Errorf("failed to update provider key: %w", err))
		return
	}

	response := ProviderKeyResponse{
		Id:                key.Id,
		Nickname:          key.Nickname,
		ProviderType:      key.ProviderType,
		SecretManagerType: key.SecretManagerType,
		Created:           key.Created,
		Updated:           key.Updated,
	}

	c.JSON(http.StatusOK, gin.H{"providerKey": response})
}

// DeleteProviderKeyHandler handles the deletion of a provider key
func (ctrl *Controller) DeleteProviderKeyHandler(c *gin.Context) {
	keyId := c.Param("keyId")

	key, err := ctrl.service.GetProviderKey(c, keyId)
	if err != nil {
		ctrl.ErrorHandler(c, http.StatusNotFound, err)
		return
	}

	// Get secret manager based on the type
	secretManager := secret_manager.GetSecretManager(key.SecretManagerType)

	// Delete from secret manager first
	if err := secretManager.DeleteSecret(key.SecretName); err != nil {
		ctrl.ErrorHandler(c, http.StatusInternalServerError, fmt.Errorf("failed to delete key from secret manager: %w", err))
		return
	}

	// Delete the provider key metadata
	if err := ctrl.service.DeleteProviderKey(c, keyId); err != nil {
		ctrl.ErrorHandler(c, http.StatusInternalServerError, fmt.Errorf("failed to delete provider key metadata: %w", err))
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Provider key deleted successfully"})
}
