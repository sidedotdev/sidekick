package server

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Route fixes a proxy's upstream base URL and host-side credential source.
// Callers address the proxy as PathPrefix + name and append the API path
// relative to the upstream, e.g. "/responses" for an OpenAI base of
// "https://api.openai.com/v1".
type Route struct {
	Upstream  string
	Authorize func(context.Context) (http.Header, error)
	// CredentialHeaders names headers that carry identity or credentials for
	// this upstream. They are always dropped from the caller, even when
	// Authorize injects nothing for them, so the host alone decides them.
	CredentialHeaders []string
}

// Headers never forwarded from a caller: sandbox-held credentials, and the
// hop-by-hop upgrade/trailer headers ReverseProxy reinstates before Rewrite,
// which would let a caller negotiate a protocol upgrade through the proxy.
var strippedCallerHeaders = []string{
	"Authorization", "Proxy-Authorization", "Cookie",
	"Connection", "Upgrade", "Te",
}

func NewHandler(token string, routes map[string]Route) (http.Handler, error) {
	return newHandlerWithTransport(token, routes, nil)
}

func newHandlerWithTransport(token string, routes map[string]Route, transport http.RoundTripper) (http.Handler, error) {
	if token == "" {
		return nil, errors.New("proxy token is required")
	}
	handlers := make(map[string]http.Handler, len(routes))
	for name, route := range routes {
		target, err := url.Parse(route.Upstream)
		if err != nil || !validProxyName(name) || target.Host == "" ||
			(target.Scheme != "https" && target.Scheme != "http") ||
			target.User != nil || target.RawQuery != "" || target.Fragment != "" || route.Authorize == nil {
			return nil, errors.New("invalid proxy route")
		}
		authorize := route.Authorize
		stripped := append(append([]string(nil), strippedCallerHeaders...), route.CredentialHeaders...)
		basePath := strings.TrimRight(target.Path, "/")
		proxy := &httputil.ReverseProxy{
			Transport:     transport,
			FlushInterval: -1,
			// pr.Out arrives with hop-by-hop headers already removed and no
			// X-Forwarded-* added, so the sandbox's identity is not disclosed.
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.Out.URL = new(url.URL)
				*pr.Out.URL = *target
				if relative := pr.In.Context().Value(relativePathKey{}).(string); relative != "" {
					pr.Out.URL.Path = basePath + relative
				}
				// Encoded separators and dots were rejected on the raw path,
				// so re-encoding the decoded path cannot resurrect them.
				pr.Out.URL.RawPath = ""
				pr.Out.URL.RawQuery = pr.In.URL.RawQuery
				pr.Out.URL.Fragment = ""
				pr.Out.Host = target.Host
				for _, name := range stripped {
					pr.Out.Header.Del(name)
				}
				headers := pr.In.Context().Value(credentialsKey{}).(http.Header)
				for name, values := range headers {
					pr.Out.Header[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
				}
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				http.Error(w, "upstream unavailable", http.StatusBadGateway)
			},
		}
		handlers[name] = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			headers, err := authorize(r.Context())
			if err != nil {
				http.Error(w, "upstream credentials unavailable", http.StatusServiceUnavailable)
				return
			}
			proxy.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), credentialsKey{}, headers)))
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		name, relative, ok := splitProxyPath(r.URL.EscapedPath())
		handler, exists := handlers[name]
		if !ok || !exists {
			http.NotFound(w, r)
			return
		}
		handler.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), relativePathKey{}, relative)))
	}), nil
}

// splitProxyPath separates the proxy name from the upstream-relative path.
// "/credential-proxies/name" (optionally with a trailing slash) addresses the
// configured upstream itself; anything after it is appended to the upstream
// path as decoded segments. It inspects the escaped request path so that
// encoded separators or dots cannot slip past segment validation and later be
// reinterpreted by the upstream.
func splitProxyPath(escapedPath string) (name, relative string, ok bool) {
	rest, found := strings.CutPrefix(escapedPath, PathPrefix)
	if !found {
		return "", "", false
	}
	name, rawRelative, _ := strings.Cut(rest, "/")
	if !validProxyName(name) {
		return "", "", false
	}
	if rawRelative == "" {
		return name, "", true
	}
	rawSegments := strings.Split(rawRelative, "/")
	segments := make([]string, 0, len(rawSegments))
	for _, raw := range rawSegments {
		if !safePathSegment(raw) {
			return "", "", false
		}
		segment, err := url.PathUnescape(raw)
		if err != nil {
			return "", "", false
		}
		segments = append(segments, segment)
	}
	return name, "/" + strings.Join(segments, "/"), true
}

// safePathSegment accepts one escaped path segment that is non-empty, not a
// dot segment, and free of control characters, backslashes, and any
// percent-encoding of '/', '\', '.' or a control character.
func safePathSegment(raw string) bool {
	if raw == "" || raw == "." || raw == ".." {
		return false
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c < 0x20 || c == 0x7f || c == '\\' {
			return false
		}
		if c != '%' {
			continue
		}
		if i+2 >= len(raw) {
			return false
		}
		code, err := strconv.ParseUint(raw[i+1:i+3], 16, 8)
		if err != nil || code < 0x20 || code == 0x7f || code == '/' || code == '\\' || code == '.' {
			return false
		}
	}
	return true
}

func validProxyName(name string) bool {
	if name == "" {
		return false
	}
	for _, char := range name {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9' || char == '-' || char == '_' {
			continue
		}
		return false
	}
	return true
}

type relativePathKey struct{}

type credentialsKey struct{}

func newServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       time.Minute,
	}
}
