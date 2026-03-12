// cmd/boss-observe: standalone MCP observability plugin for boss.
//
// Exposes 4 read-only MCP tools that let agents query system state:
//   - get_session_output  — last N lines of an agent's tmux pane
//   - list_sessions       — all tmux sessions with idle/running/missing status
//   - get_recent_events   — recent events from the boss HTTP API
//   - get_agent_status    — combined agent DB record + session status + recent output
//
// Registration (pre-spawn via --mcp-config JSON, recommended):
//
//	{"mcpServers":{
//	  "boss-mcp":     {"type":"http","url":"http://localhost:8899/mcp"},
//	  "boss-observe": {"type":"stdio","command":"./bin/boss-observe","args":["--boss-url","http://localhost:8899"]}
//	}}
//
// Or mid-session curl fallback: see scripts/boss-observe.sh
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	bossURL := flag.String("boss-url", "", "Boss server base URL (default: $BOSS_URL or http://localhost:8899)")
	bossToken := flag.String("boss-token", "", "Bearer token for boss API (default: $BOSS_API_TOKEN)")
	flag.Parse()

	if *bossURL == "" {
		*bossURL = os.Getenv("BOSS_URL")
	}
	if *bossURL == "" {
		*bossURL = "http://localhost:8899"
	}
	if *bossToken == "" {
		*bossToken = os.Getenv("BOSS_API_TOKEN")
	}

	srv := newObserveServer(strings.TrimRight(*bossURL, "/"), *bossToken)

	ctx := context.Background()
	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil {
		fmt.Fprintf(os.Stderr, "boss-observe: %v\n", err)
		os.Exit(1)
	}
}

// observeServer holds config for HTTP API calls and provides MCP tool handlers.
type observeServer struct {
	bossURL    string
	bossToken  string
	httpClient *http.Client
}

func newObserveServer(bossURL, bossToken string) *mcp.Server {
	obs := &observeServer{
		bossURL:   bossURL,
		bossToken: bossToken,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}

	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "boss-observe",
		Version: "1.0.0",
	}, nil)

	obs.addToolGetSessionOutput(srv)
	obs.addToolListSessions(srv)
	obs.addToolGetRecentEvents(srv)
	obs.addToolGetAgentStatus(srv)

	return srv
}

// --- HTTP helpers ---

func (o *observeServer) get(path string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, o.bossURL+path, nil)
	if err != nil {
		return nil, err
	}
	if o.bossToken != "" {
		req.Header.Set("Authorization", "Bearer "+o.bossToken)
	}
	resp, err := o.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d: %s", path, resp.StatusCode, string(body))
	}
	return body, nil
}

// --- tmux helpers ---

// tmuxCaptureLines returns the last N lines from a tmux pane, newest-last.
func tmuxCaptureLines(sessionID string, lines int) ([]string, error) {
	if lines <= 0 {
		lines = 50
	}
	out, err := exec.Command("tmux", "capture-pane", "-p", "-t", sessionID).Output()
	if err != nil {
		return nil, fmt.Errorf("tmux capture-pane -t %s: %w", sessionID, err)
	}
	all := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return all, nil
}

// tmuxListAll returns all active tmux session names.
func tmuxListAll() ([]string, error) {
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		// tmux returns exit 1 when no sessions exist — treat as empty.
		return nil, nil
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, nil
	}
	return strings.Split(raw, "\n"), nil
}

// tmuxIsIdle checks whether the tmux pane is at a shell prompt (not running a process).
func tmuxIsIdle(sessionID string) bool {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", sessionID, "#{pane_current_command}").Output()
	if err != nil {
		return false
	}
	cmd := strings.TrimSpace(string(out))
	return cmd == "bash" || cmd == "zsh" || cmd == "sh" || cmd == "fish"
}

// tmuxSessionExists returns true if the given session name is active.
func tmuxSessionExists(sessionID string) bool {
	return exec.Command("tmux", "has-session", "-t", sessionID).Run() == nil
}

// --- MCP tool helpers ---

func toolError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
		IsError: true,
	}
}

func toolJSON(v any) *mcp.CallToolResult {
	data, _ := json.MarshalIndent(v, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(data)}},
	}
}

func parseArgs(req *mcp.CallToolRequest) (map[string]any, error) {
	var args map[string]any
	if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	return args, nil
}

func strArg(args map[string]any, key string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func intArg(args map[string]any, key string, defaultVal int) int {
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case string:
			if i, err := strconv.Atoi(n); err == nil {
				return i
			}
		}
	}
	return defaultVal
}

func jsonSchema(required []string, props map[string]map[string]any) map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": props,
		"required":   required,
	}
}

func prop(typ, desc string) map[string]any {
	return map[string]any{"type": typ, "description": desc}
}

// --- Tool: get_session_output ---

func (o *observeServer) addToolGetSessionOutput(srv *mcp.Server) {
	srv.AddTool(&mcp.Tool{
		Name:        "get_session_output",
		Description: "Get the last N lines of output from an agent's tmux session. Useful for observing what an agent is currently doing.",
		InputSchema: jsonSchema([]string{"session_id"}, map[string]map[string]any{
			"session_id": prop("string", "The tmux session ID (e.g. agent-boss-dev-arch2)"),
			"lines":      prop("number", "Number of lines to return (default: 50)"),
		}),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, err := parseArgs(req)
		if err != nil {
			return toolError(err.Error()), nil
		}
		sessionID := strArg(args, "session_id")
		if sessionID == "" {
			return toolError("session_id is required"), nil
		}
		lines := intArg(args, "lines", 50)

		if !tmuxSessionExists(sessionID) {
			return toolJSON(map[string]any{
				"session_id": sessionID,
				"status":     "missing",
				"lines":      []string{},
			}), nil
		}

		captured, err := tmuxCaptureLines(sessionID, lines)
		if err != nil {
			return toolError(fmt.Sprintf("capture output: %v", err)), nil
		}

		status := "running"
		if tmuxIsIdle(sessionID) {
			status = "idle"
		}

		return toolJSON(map[string]any{
			"session_id": sessionID,
			"status":     status,
			"lines":      captured,
		}), nil
	})
}

// --- Tool: list_sessions ---

func (o *observeServer) addToolListSessions(srv *mcp.Server) {
	srv.AddTool(&mcp.Tool{
		Name:        "list_sessions",
		Description: "List all active tmux sessions with their status (idle/running). Useful for discovering which agents are running.",
		InputSchema: jsonSchema([]string{}, map[string]map[string]any{
			"filter": prop("string", "Optional substring filter on session name"),
		}),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, err := parseArgs(req)
		if err != nil {
			return toolError(err.Error()), nil
		}
		filter := strArg(args, "filter")

		sessions, err := tmuxListAll()
		if err != nil {
			return toolError(fmt.Sprintf("list sessions: %v", err)), nil
		}

		type sessionInfo struct {
			SessionID string `json:"session_id"`
			Status    string `json:"status"`
		}
		var results []sessionInfo
		for _, s := range sessions {
			if filter != "" && !strings.Contains(s, filter) {
				continue
			}
			status := "running"
			if tmuxIsIdle(s) {
				status = "idle"
			}
			results = append(results, sessionInfo{SessionID: s, Status: status})
		}
		if results == nil {
			results = []sessionInfo{}
		}

		return toolJSON(map[string]any{
			"sessions": results,
			"total":    len(results),
		}), nil
	})
}

// --- Tool: get_recent_events ---

// spaceEvent mirrors the SpaceEvent type from the boss server for JSON decoding.
type spaceEvent struct {
	ID        string          `json:"id"`
	Space     string          `json:"space"`
	Type      string          `json:"type"`
	Agent     string          `json:"agent,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

func (o *observeServer) addToolGetRecentEvents(srv *mcp.Server) {
	srv.AddTool(&mcp.Tool{
		Name:        "get_recent_events",
		Description: "Get recent events from the boss event log for a space. Returns agent updates, messages, task changes, and more.",
		InputSchema: jsonSchema([]string{"space"}, map[string]map[string]any{
			"space":      prop("string", "The workspace name"),
			"limit":      prop("number", "Maximum number of events to return (default: 20)"),
			"event_type": prop("string", "Filter by event type (e.g. agent_updated, message_sent, task_moved)"),
		}),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, err := parseArgs(req)
		if err != nil {
			return toolError(err.Error()), nil
		}
		spaceName := strArg(args, "space")
		if spaceName == "" {
			return toolError("space is required"), nil
		}
		limit := intArg(args, "limit", 20)
		eventType := strArg(args, "event_type")

		apiPath := fmt.Sprintf("/spaces/%s/api/events", url.PathEscape(spaceName))
		body, err := o.get(apiPath)
		if err != nil {
			return toolError(fmt.Sprintf("fetch events: %v", err)), nil
		}

		var events []spaceEvent
		if err := json.Unmarshal(body, &events); err != nil {
			return toolError(fmt.Sprintf("decode events: %v", err)), nil
		}

		// Filter by event type if specified.
		if eventType != "" {
			filtered := events[:0]
			for _, ev := range events {
				if ev.Type == eventType {
					filtered = append(filtered, ev)
				}
			}
			events = filtered
		}

		// Return the most recent `limit` events (events are oldest-first from the API).
		if len(events) > limit {
			events = events[len(events)-limit:]
		}

		type eventSummary struct {
			ID        string    `json:"id"`
			Type      string    `json:"type"`
			Agent     string    `json:"agent,omitempty"`
			Timestamp time.Time `json:"timestamp"`
		}
		summaries := make([]eventSummary, len(events))
		for i, ev := range events {
			summaries[i] = eventSummary{
				ID:        ev.ID,
				Type:      ev.Type,
				Agent:     ev.Agent,
				Timestamp: ev.Timestamp,
			}
		}

		return toolJSON(map[string]any{
			"space":  spaceName,
			"events": summaries,
			"total":  len(summaries),
		}), nil
	})
}

// --- Tool: get_agent_status ---

func (o *observeServer) addToolGetAgentStatus(srv *mcp.Server) {
	srv.AddTool(&mcp.Tool{
		Name:        "get_agent_status",
		Description: "Get a combined health snapshot for an agent: coordinator status, session status, and last 10 lines of tmux output. One-call health check.",
		InputSchema: jsonSchema([]string{"space", "agent"}, map[string]map[string]any{
			"space": prop("string", "The workspace name"),
			"agent": prop("string", "The agent name"),
		}),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, err := parseArgs(req)
		if err != nil {
			return toolError(err.Error()), nil
		}
		spaceName := strArg(args, "space")
		agentName := strArg(args, "agent")
		if spaceName == "" || agentName == "" {
			return toolError("space and agent are required"), nil
		}

		// Fetch agent record from boss HTTP API.
		agentPath := fmt.Sprintf("/spaces/%s/agent/%s", url.PathEscape(spaceName), url.PathEscape(agentName))
		agentBody, err := o.get(agentPath)
		if err != nil {
			return toolError(fmt.Sprintf("fetch agent: %v", err)), nil
		}

		var agentData map[string]any
		if err := json.Unmarshal(agentBody, &agentData); err != nil {
			return toolError(fmt.Sprintf("decode agent: %v", err)), nil
		}

		// Extract session_id from the agent record.
		sessionID := ""
		if sid, ok := agentData["session_id"].(string); ok {
			sessionID = sid
		}

		sessionStatus := "unregistered"
		var recentOutput []string

		if sessionID != "" {
			if tmuxSessionExists(sessionID) {
				if tmuxIsIdle(sessionID) {
					sessionStatus = "idle"
				} else {
					sessionStatus = "running"
				}
				recentOutput, _ = tmuxCaptureLines(sessionID, 10)
			} else {
				sessionStatus = "missing"
			}
		}

		status := ""
		if s, ok := agentData["status"].(string); ok {
			status = s
		}
		summary := ""
		if s, ok := agentData["summary"].(string); ok {
			summary = s
		}
		lastUpdate := ""
		if t, ok := agentData["updated_at"].(string); ok {
			lastUpdate = t
		}

		if recentOutput == nil {
			recentOutput = []string{}
		}

		return toolJSON(map[string]any{
			"agent":          agentName,
			"space":          spaceName,
			"status":         status,
			"summary":        summary,
			"session_id":     sessionID,
			"session_status": sessionStatus,
			"last_update":    lastUpdate,
			"recent_output":  recentOutput,
		}), nil
	})
}

