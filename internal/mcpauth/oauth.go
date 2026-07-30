package mcpauth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// OAuthConfig holds configuration for OAuth resource server token validation.
type OAuthConfig struct {
	Issuer   string // expected iss claim (RFC 9207 issuer)
	JWKSURL  string // JWKS endpoint URL
	Audience string // optional expected aud claim
}

// jwksKey caches JWKS keys with periodic refresh.
type jwksKey struct {
	keys      map[string]any // kid -> crypto key
	fetchedAt time.Time
	fetchErr  error
}

type jwksCache struct {
	url      string
	ttl      time.Duration
	client   *http.Client
	key      jwt.Keyfunc
	mu       sync.RWMutex
	cached   jwksKey
	refresh  sync.Mutex // serializes refresh calls
}

// jwtValidator validates bearer tokens as OAuth JWTs.
type jwtValidator struct {
	cfg     OAuthConfig
	jwks    *jwksCache
}

func newJWTValidator(cfg OAuthConfig) (*jwtValidator, error) {
	if cfg.Issuer == "" {
		return nil, fmt.Errorf("mcpauth: oauth issuer is required")
	}
	if cfg.JWKSURL == "" {
		return nil, fmt.Errorf("mcpauth: oauth jwks_url is required")
	}
	c := &jwksCache{
		url:    cfg.JWKSURL,
		ttl:    15 * time.Minute,
		client: &http.Client{Timeout: 10 * time.Second},
	}
	v := &jwtValidator{cfg: cfg, jwks: c}
	// Pre-fetch JWKS so the first request doesn't pay the latency.
	c.refresh.Lock()
	c.fetch()
	c.refresh.Unlock()
	if c.cached.fetchErr != nil {
		return nil, fmt.Errorf("mcpauth: initial JWKS fetch: %w", c.cached.fetchErr)
	}
	return v, nil
}

// fetch downloads and parses the JWKS document. Caller must hold refresh mutex.
func (c *jwksCache) fetch() {
	resp, err := c.client.Get(c.url)
	if err != nil {
		c.cached = jwksKey{fetchErr: fmt.Errorf("jwks fetch: %w", err)}
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		c.cached = jwksKey{fetchErr: fmt.Errorf("jwks fetch: HTTP %d", resp.StatusCode)}
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB max
	if err != nil {
		c.cached = jwksKey{fetchErr: fmt.Errorf("jwks read: %w", err)}
		return
	}
	var jwks struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
			X   string `json:"x"`
			Y   string `json:"y"`
			Crv string `json:"crv"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(body, &jwks); err != nil {
		c.cached = jwksKey{fetchErr: fmt.Errorf("jwks parse: %w", err)}
		return
	}
	keys := make(map[string]any, len(jwks.Keys))
	for _, k := range jwks.Keys {
		pk, err := parseJWK(k.Kty, k.N, k.E, k.X, k.Y, k.Crv)
		if err != nil {
			continue // skip unparseable keys
		}
		keys[k.Kid] = pk
	}
	c.cached = jwksKey{keys: keys, fetchedAt: time.Now()}
}

// keyFunc returns a jwt.Keyfunc that resolves the verification key from the
// cached JWKS by kid, refreshing the cache if stale.
func (c *jwksCache) keyFunc() jwt.Keyfunc {
	return func(t *jwt.Token) (any, error) {
		// Refresh if cache is empty or stale.
		c.mu.RLock()
		cached := c.cached
		c.mu.RUnlock()
		if cached.fetchErr != nil || time.Since(cached.fetchedAt) > c.ttl {
			c.refresh.Lock()
			// Double-check after acquiring write lock.
			if c.cached.fetchErr != nil || time.Since(c.cached.fetchedAt) > c.ttl {
				c.fetch()
			}
			cached = c.cached
			c.refresh.Unlock()
		}
		if cached.fetchErr != nil {
			return nil, fmt.Errorf("jwks unavailable: %w", cached.fetchErr)
		}
		kid, ok := t.Header["kid"].(string)
		if !ok {
			return nil, fmt.Errorf("token missing kid header")
		}
		key, ok := cached.keys[kid]
		if !ok {
			return nil, fmt.Errorf("no key for kid %q in JWKS", kid)
		}
		return key, nil
	}
}

// validate parses and validates a bearer token, returning the authenticated Principal.
func (v *jwtValidator) validate(tokenStr string) (Principal, error) {
	claims := jwt.MapClaims{}
	opts := []jwt.ParserOption{
		jwt.WithIssuer(v.cfg.Issuer),
	}
	if v.cfg.Audience != "" {
		opts = append(opts, jwt.WithAudience(v.cfg.Audience))
	}
	token, err := jwt.ParseWithClaims(tokenStr, claims, v.jwks.keyFunc(), opts...)
	if err != nil {
		return Principal{}, fmt.Errorf("token validation failed: %w", err)
	}
	if !token.Valid {
		return Principal{}, fmt.Errorf("token is not valid")
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return Principal{}, fmt.Errorf("token missing sub claim")
	}
	iss, _ := claims["iss"].(string)
	return Principal{ID: sub, Tenant: iss}, nil
}
