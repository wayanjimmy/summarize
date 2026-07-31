package mcpserver

import (
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

	mcpHandler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{
			Stateless:                    true,
			JSONResponse:                 true,
			PropagateRequestCancellation: true,
		},
	)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			// Stateless server: no standalone SSE streams.
			// Return 406 so clients fall back to POST-only mode.
			w.Header().Set("Allow", "POST")
			w.WriteHeader(http.StatusNotAcceptable)
			return
		}
		mcpHandler.ServeHTTP(w, r)
	})
}
