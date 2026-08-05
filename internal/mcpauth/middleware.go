package mcpauth

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Config holds auth middleware configuration.
type Config struct {
	Mode         string      // none, static, oauth
	APIKey       string      // for static mode (MCP_API_KEY)
	AnonymousRef string      // for none mode: deployment-stable first-party ID (MCP_ANONYMOUS_REF)
	OAuth        OAuthConfig // for oauth mode
}

// Middleware returns HTTP middleware that authenticates the request according
// to the configured mode and stores the Principal in the request context.
//
// In "none" mode, every request gets a Principal with ID=DefaultOwnerID.
// In "static" mode, the bearer token must match APIKey.
// In "oauth" mode, the bearer token is validated as a JWT against the configured issuer/JWKS.
func Middleware(cfg Config) func(http.Handler) http.Handler {
	// Pre-build the OAuth validator once at middleware construction time.
	var validator *jwtValidator
	if cfg.Mode == AuthModeOAuth {
		v, err := newJWTValidator(cfg.OAuth)
		if err != nil {
			// If JWKS is unreachable at startup, fall back to a lazy
			// validator that will refresh on the first request.
			validator, _ = newLazyValidator(cfg.OAuth)
		} else {
			validator = v
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, err := authenticate(r, cfg, validator)
			if err != nil {
				writeAuthError(w, err)
				return
			}
			ctx := WithPrincipal(r.Context(), principal)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// authenticate validates the request and returns the Principal.
func authenticate(r *http.Request, cfg Config, validator *jwtValidator) (Principal, error) {
	switch cfg.Mode {
	case AuthModeNone, "":
		id := cfg.AnonymousRef
		if id == "" {
			id = DefaultOwnerID
		}
		return Principal{ID: id, Anonymous: true}, nil

	case AuthModeStatic:
		if cfg.APIKey == "" {
			return Principal{}, errors.New("static auth configured without API key")
		}
		token := extractBearer(r)
		if token == "" {
			return Principal{}, errMissingToken
		}
		if !constantTimeEqual(token, cfg.APIKey) {
			return Principal{}, errInvalidToken
		}
		return Principal{ID: "mcp-static"}, nil

	case AuthModeOAuth:
		if validator == nil {
			return Principal{}, errors.New("oauth validator not initialized")
		}
		token := extractBearer(r)
		if token == "" {
			return Principal{}, errMissingToken
		}
		return validator.validate(token)

	default:
		return Principal{}, fmt.Errorf("unknown auth mode %q", cfg.Mode)
	}
}

// extractBearer pulls the raw token from the Authorization header.
func extractBearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// writeAuthError sends a 401 with a WWW-Authenticate header.
func writeAuthError(w http.ResponseWriter, err error) {
	w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"` + err.Error() + `"}}`))
}

func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var result byte
	for i := 0; i < len(a); i++ {
		result |= a[i] ^ b[i]
	}
	return result == 0
}

var (
	errMissingToken = errors.New("missing or malformed Authorization header")
	errInvalidToken = errors.New("invalid API key")
)

// newLazyValidator creates a JWT validator without pre-fetching JWKS.
// The cache will be populated on the first token validation attempt.
func newLazyValidator(cfg OAuthConfig) (*jwtValidator, error) {
	if cfg.Issuer == "" || cfg.JWKSURL == "" {
		return nil, errors.New("oauth issuer and jwks_url are required")
	}
	c := &jwksCache{
		url:    cfg.JWKSURL,
		ttl:    15 * time.Minute,
		client: &http.Client{Timeout: 10 * time.Second},
	}
	return &jwtValidator{cfg: cfg, jwks: c}, nil
}
