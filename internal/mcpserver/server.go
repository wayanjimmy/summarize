package mcpserver

import (
	"encoding/json"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wayanjimmy/summarize/internal/summary"
)

// NewHandler creates an http.Handler that serves the MCP 2026-07-28 stateless
// Streamable HTTP transport. The returned handler handles POST /mcp.
//
// The same MCP server instance is reused for every request (stateless mode),
// so tool and resource registrations are performed once at construction time.
func NewHandler(svc *summary.Service, version string, fi *FeedbackIntegration) http.Handler {
	server := mcp.NewServer(
		&mcp.Implementation{
			Name:    "summarize",
			Version: version,
		},
		nil,
	)

	registerTools(server, svc, fi)
	if fi != nil {
		registerFeedbackTool(server, fi)
	}
	registerResources(server, svc)

	handler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{
			Stateless:                    true,
			JSONResponse:                 true,
			PropagateRequestCancellation: true,
		},
	)
	if fi != nil {
		return validateMCPHeaders(handler)
	}
	return handler
}

func validateMCPHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protocol := r.Header.Get("MCP-Protocol-Version")
		method := r.Header.Get("Mcp-Method")
		name := r.Header.Get("Mcp-Name")
		if protocol != "" && protocol != "2026-07-28" {
			writeHeaderError(w, "unsupported MCP-Protocol-Version")
			return
		}
		if method == "tools/call" && name == "" {
			writeHeaderError(w, "Mcp-Name is required for tools/call")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeHeaderError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0", "id": nil,
		"error": map[string]any{"code": -32600, "message": message},
	})
}
