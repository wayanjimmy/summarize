// Package mcpauth provides authentication middleware for the MCP endpoint.
//
// Three auth modes are supported:
//   - none:   local/loopback; all requests get a default "local" principal.
//   - static: a shared API key (MCP_API_KEY) sent as a bearer token.
//   - oauth:  production; bearer tokens are validated as OAuth JWTs against
//     a configured issuer and JWKS endpoint (RFC 9207 issuer validation).
//
// The authenticated Principal is stored in the request context so that
// downstream tool handlers can scope all store queries by owner.
package mcpauth

import "context"

// Auth mode constants.
const (
	AuthModeNone   = "none"
	AuthModeStatic = "static"
	AuthModeOAuth  = "oauth"
)

// DefaultOwnerID is the owner used when auth is disabled (none mode).
const DefaultOwnerID = "local"

// Principal represents the authenticated caller. ID is the stable owner
// identifier persisted on runs; Tenant is an optional realm/issuer for
// multi-tenant deployments.
type Principal struct {
	ID     string
	Tenant string
}

type contextKey struct{}

var principalKey = contextKey{}

// WithPrincipal stores the Principal in the context.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

// PrincipalFromContext extracts the Principal from the context.
// Returns false if no principal is present.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	if ctx == nil {
		return Principal{}, false
	}
	p, ok := ctx.Value(principalKey).(Principal)
	return p, ok
}
