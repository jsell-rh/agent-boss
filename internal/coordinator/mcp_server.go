package coordinator

// mcp_server.go: Boss MCP server implementation.
// Exposes agent bootstrap resources via the Model Context Protocol on POST /mcp.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// buildMCPHandler creates the MCP server and returns an http.Handler for mounting at /mcp.
func (s *Server) buildMCPHandler() http.Handler {
	srv := mcpserver.NewMCPServer(
		"boss",
		"1.0.0",
		mcpserver.WithResourceCapabilities(true, true),
	)

	// Resource: boss://bootstrap/{space}/{agent}
	// Returns the full agent ignition/bootstrap text for a specific agent.
	bootstrapTemplate := mcp.NewResourceTemplate(
		"boss://bootstrap/{space}/{agent}",
		"Agent bootstrap instructions",
		mcp.WithTemplateDescription("Full ignition prompt for a named agent in a space"),
		mcp.WithTemplateMIMEType("text/plain"),
	)
	srv.AddResourceTemplate(bootstrapTemplate, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		uri := req.Params.URI
		// Parse space and agent from URI: boss://bootstrap/{space}/{agent}
		rest := strings.TrimPrefix(uri, "boss://bootstrap/")
		idx := strings.Index(rest, "/")
		if idx < 0 {
			return nil, fmt.Errorf("invalid URI: missing agent name")
		}
		spaceName := rest[:idx]
		agentName := rest[idx+1:]
		if spaceName == "" || agentName == "" {
			return nil, fmt.Errorf("invalid URI: space and agent are required")
		}

		s.mu.RLock()
		text := s.buildIgnitionText(spaceName, agentName, "")
		// Prepend assembled persona prompt if agent has personas configured.
		if ks, ok := s.spaces[spaceName]; ok {
			canonical := resolveAgentName(ks, agentName)
			if cfg := ks.agentConfig(canonical); cfg != nil && len(cfg.Personas) > 0 {
				personaPrompt := s.assemblePersonaPrompt(cfg.Personas)
				if personaPrompt != "" {
					text = personaPrompt + "\n\n" + text
				}
			}
		}
		s.mu.RUnlock()

		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      uri,
				MIMEType: "text/plain",
				Text:     text,
			},
		}, nil
	})

	// Resource: boss://protocol
	// Returns the embedded agent collaboration protocol.
	protocolResource := mcp.NewResource(
		"boss://protocol",
		"Agent collaboration protocol",
		mcp.WithResourceDescription("The agent communication and collaboration protocol"),
		mcp.WithMIMEType("text/markdown"),
	)
	srv.AddResource(protocolResource, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      "boss://protocol",
				MIMEType: "text/markdown",
				Text:     protocolTemplate,
			},
		}, nil
	})

	// Resource template: boss://space/{space}/blackboard
	// Returns the rendered markdown blackboard for a space.
	blackboardTemplate := mcp.NewResourceTemplate(
		"boss://space/{space}/blackboard",
		"Space blackboard",
		mcp.WithTemplateDescription("Current state of all agents in a space"),
		mcp.WithTemplateMIMEType("text/markdown"),
	)
	srv.AddResourceTemplate(blackboardTemplate, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		uri := req.Params.URI
		spaceName := strings.TrimPrefix(uri, "boss://space/")
		spaceName = strings.TrimSuffix(spaceName, "/blackboard")
		if spaceName == "" {
			return nil, fmt.Errorf("invalid URI: missing space name")
		}

		s.mu.RLock()
		ks, ok := s.spaces[spaceName]
		var md string
		if ok {
			md = ks.RenderMarkdown()
		} else {
			md = fmt.Sprintf("# %s\n\nSpace not found.\n", spaceName)
		}
		s.mu.RUnlock()

		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      uri,
				MIMEType: "text/markdown",
				Text:     md,
			},
		}, nil
	})

	return mcpserver.NewStreamableHTTPServer(srv)
}

// handleSettings handles GET and PATCH /settings.
// Exposes server-wide configuration toggles.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	type settingsPayload struct {
		AllowSkipPermissions bool `json:"allow_skip_permissions"`
	}

	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(settingsPayload{
			AllowSkipPermissions: s.allowSkipPermissions,
		})

	case http.MethodPatch:
		var patch settingsPayload
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			writeJSONError(w, "invalid json: "+err.Error(), http.StatusBadRequest)
			return
		}
		s.allowSkipPermissions = patch.AllowSkipPermissions
		s.logEvent(fmt.Sprintf("settings updated: allow_skip_permissions=%v", s.allowSkipPermissions))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(settingsPayload{
			AllowSkipPermissions: s.allowSkipPermissions,
		})

	default:
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
