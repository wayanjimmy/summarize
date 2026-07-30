package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wayanjimmy/summarize/internal/domain"
	"github.com/wayanjimmy/summarize/internal/summary"
)

// registerResources registers the summarize://models resource on the server.
func registerResources(server *mcp.Server, svc *summary.Service) {
	server.AddResource(
		&mcp.Resource{
			Name:        "models",
			URI:         "summarize://models",
			Description: "Available LLM models for each engine (pi, agy) with status and default model information.",
			MIMEType:    "application/json",
		},
		modelsResourceHandler(svc),
	)
}

func modelsResourceHandler(svc *summary.Service) mcp.ResourceHandler {
	return func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		result, err := svc.ListModels(ctx)
		if err != nil {
			return nil, err
		}

		resource := ModelsResource{
			Engines: map[string]EngineModels{},
		}

		for _, name := range []string{domain.EnginePi, domain.EngineAgy} {
			info := result.Engines[name]
			entry := EngineModels{
				Models:       info.Models,
				DefaultModel: info.DefaultModel,
				Status:       info.Status,
				Available:    info.Available,
				Stale:        info.Stale,
				Error:        info.Error,
			}
			if !info.FetchedAt.IsZero() {
				entry.FetchedAt = info.FetchedAt.UTC().Format(time.RFC3339)
			}
			resource.Engines[name] = entry
		}

		data, err := json.Marshal(resource)
		if err != nil {
			return nil, fmt.Errorf("marshal models: %w", err)
		}

		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      req.Params.URI,
					MIMEType: "application/json",
					Text:     string(data),
				},
			},
		}, nil
	}
}
