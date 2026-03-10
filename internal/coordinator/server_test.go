package coordinator

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mustStartServer(t *testing.T) (*Server, func()) {
	t.Helper()
	dataDir := t.TempDir()
	srv := NewServer(":0", dataDir)
	if err := srv.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	return srv, func() { srv.Stop() }
}

func serverBaseURL(srv *Server) string {
	return "http://localhost" + srv.Port()
}

func extractAgentName(url string) string {
	parts := strings.Split(url, "/agent/")
	if len(parts) < 2 {
		return ""
	}
	name := strings.TrimRight(parts[1], "/")
	// Strip sub-route (e.g. "bot1/register" -> "bot1")
	if idx := strings.Index(name, "/"); idx >= 0 {
		name = name[:idx]
	}
	return name
}

func postJSON(t *testing.T, url string, payload any) *http.Response {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("new request %s: %v", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if name := extractAgentName(url); name != "" {
		req.Header.Set("X-Agent-Name", name)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func postText(t *testing.T, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request %s: %v", url, err)
	}
	req.Header.Set("Content-Type", "text/plain")
	if name := extractAgentName(url); name != "" {
		req.Header.Set("X-Agent-Name", name)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func getBody(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestServerStartStop(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()

	if !srv.Running() {
		t.Fatal("expected server to be running")
	}
	srv.Stop()
	if srv.Running() {
		t.Fatal("expected server to be stopped")
	}
}

func TestPostAgentJSON(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	tests := 42
	update := AgentUpdate{
		Status:    StatusActive,
		Summary:   "Phase 1 complete. 42 tests.",
		Phase:     "1",
		TestCount: &tests,
		Items:     []string{"Delivered feature A", "Fixed bug B"},
		Questions: []string{"Should we use 200 or 202?"},
		NextSteps: "Awaiting next assignment.",
	}

	resp := postJSON(t, base+"/spaces/my-project/agent/api", update)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 202, got %d: %s", resp.StatusCode, body)
	}

	code, body := getBody(t, base+"/spaces/my-project/agent/api")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}

	var got AgentUpdate
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Status != StatusActive {
		t.Errorf("status = %q, want %q", got.Status, StatusActive)
	}
	if got.Summary != "Phase 1 complete. 42 tests." {
		t.Errorf("summary = %q", got.Summary)
	}
	if got.TestCount == nil || *got.TestCount != 42 {
		t.Errorf("test_count = %v, want 42", got.TestCount)
	}
	if len(got.Questions) != 1 || got.Questions[0] != "Should we use 200 or 202?" {
		t.Errorf("questions = %v", got.Questions)
	}
}

func TestPostAgentPlainText(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	resp := postText(t, base+"/spaces/hackathon/agent/frontend", "Working on login page\nSecond line")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 202, got %d: %s", resp.StatusCode, body)
	}

	code, body := getBody(t, base+"/spaces/hackathon/agent/frontend")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}

	var got AgentUpdate
	json.Unmarshal([]byte(body), &got)
	if got.Status != StatusActive {
		t.Errorf("status = %q, want %q", got.Status, StatusActive)
	}
	if got.Summary != "Working on login page" {
		t.Errorf("summary = %q", got.Summary)
	}
	if !strings.Contains(got.FreeText, "Second line") {
		t.Errorf("free_text missing second line: %q", got.FreeText)
	}
}

func TestRenderMarkdown(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	tests := 88
	postJSON(t, base+"/spaces/feature-123/agent/api", AgentUpdate{
		Status:    StatusDone,
		Summary:   "All endpoints delivered",
		TestCount: &tests,
		Items:     []string{"CRUD for sessions", "Health check"},
	})
	postJSON(t, base+"/spaces/feature-123/agent/cp", AgentUpdate{
		Status:   StatusBlocked,
		Summary:  "Waiting for API schema",
		Blockers: []string{"Need final OpenAPI spec"},
	})

	code, md := getBody(t, base+"/spaces/feature-123/raw")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}

	if !strings.Contains(md, "# feature-123") {
		t.Error("missing space title in markdown")
	}
	if !strings.Contains(md, "Session Dashboard") {
		t.Error("missing dashboard in markdown")
	}
	if !strings.Contains(md, "All endpoints delivered") {
		t.Error("missing API summary in markdown")
	}
	if !strings.Contains(md, "[?BOSS]") || strings.Contains(md, "[?BOSS]") {
		// questions would have [?BOSS], but this agent has none; check blockers render
	}
	if !strings.Contains(md, "Need final OpenAPI spec") {
		t.Error("missing blocker in markdown")
	}
	if !strings.Contains(md, "88") {
		t.Error("missing test count in markdown")
	}
}

func TestListSpaces(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	postJSON(t, base+"/spaces/alpha/agent/x", AgentUpdate{Status: StatusIdle, Summary: "idle"})
	postJSON(t, base+"/spaces/beta/agent/y", AgentUpdate{Status: StatusActive, Summary: "working"})

	code, body := getBody(t, base+"/spaces")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}

	var summaries []struct {
		Name       string `json:"name"`
		AgentCount int    `json:"agent_count"`
	}
	if err := json.Unmarshal([]byte(body), &summaries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("expected 2 spaces, got %d", len(summaries))
	}

	names := map[string]bool{}
	for _, s := range summaries {
		names[s.Name] = true
	}
	if !names["alpha"] || !names["beta"] {
		t.Errorf("missing expected spaces: %v", names)
	}
}

func TestPersistence(t *testing.T) {
	dataDir := t.TempDir()

	srv1 := NewServer(":0", dataDir)
	if err := srv1.Start(); err != nil {
		t.Fatal(err)
	}
	base1 := serverBaseURL(srv1)

	postJSON(t, base1+"/spaces/persist-test/agent/api", AgentUpdate{
		Status:  StatusDone,
		Summary: "Persisted data",
	})
	srv1.Stop()

	jsonFile := filepath.Join(dataDir, "persist-test.json")
	if _, err := os.Stat(jsonFile); os.IsNotExist(err) {
		t.Fatal("expected persist-test.json to exist")
	}

	srv2 := NewServer(":0", dataDir)
	if err := srv2.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv2.Stop()
	base2 := serverBaseURL(srv2)

	code, body := getBody(t, base2+"/spaces/persist-test/agent/api")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	var got AgentUpdate
	json.Unmarshal([]byte(body), &got)
	if got.Summary != "Persisted data" {
		t.Errorf("summary = %q, want %q", got.Summary, "Persisted data")
	}
}

func TestValidationRejectsInvalidStatus(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	resp := postJSON(t, base+"/spaces/test/agent/api", AgentUpdate{
		Status:  "invalid-status",
		Summary: "test",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestValidationRejectsEmptySummary(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	resp := postJSON(t, base+"/spaces/test/agent/api", AgentUpdate{
		Status:  StatusActive,
		Summary: "",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestDeleteAgent(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	postJSON(t, base+"/spaces/del-test/agent/api", AgentUpdate{
		Status:  StatusDone,
		Summary: "to be removed",
	})

	req, _ := http.NewRequest(http.MethodDelete, base+"/spaces/del-test/agent/api", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	code, body := getBody(t, base+"/spaces/del-test/agent/api")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if body != "{}" {
		t.Errorf("expected empty agent, got %q", body)
	}
}

func TestBackwardCompatRoutes(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	resp := postJSON(t, base+"/agent/legacy", AgentUpdate{
		Status:  StatusActive,
		Summary: "via legacy route",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 202, got %d: %s", resp.StatusCode, body)
	}

	code, body := getBody(t, base+"/spaces/default/agent/legacy")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	var got AgentUpdate
	json.Unmarshal([]byte(body), &got)
	if got.Summary != "via legacy route" {
		t.Errorf("summary = %q", got.Summary)
	}
}

func TestContracts(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	postJSON(t, base+"/spaces/contracts-test/agent/api", AgentUpdate{
		Status: StatusIdle, Summary: "seed",
	})

	resp := postText(t, base+"/spaces/contracts-test/contracts", "### Auth\nBearer token required.")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	code, body := getBody(t, base+"/spaces/contracts-test/contracts")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if !strings.Contains(body, "Bearer token required") {
		t.Error("contracts not stored")
	}

	code, md := getBody(t, base+"/spaces/contracts-test/raw")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if !strings.Contains(md, "Shared Contracts") {
		t.Error("contracts not rendered in markdown")
	}
}

func TestSectionsWithTable(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	update := AgentUpdate{
		Status:  StatusActive,
		Summary: "Comparison delivered",
		Sections: []Section{
			{
				Title: "Comparison Results",
				Table: &Table{
					Headers: []string{"Issue", "Severity", "Status"},
					Rows: [][]string{
						{"Missing field", "High", "Open"},
						{"Wrong type", "Medium", "Fixed"},
					},
				},
			},
		},
	}

	postJSON(t, base+"/spaces/table-test/agent/be", update)
	code, md := getBody(t, base+"/spaces/table-test/raw")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if !strings.Contains(md, "| Issue | Severity | Status |") {
		t.Error("table headers not rendered")
	}
	if !strings.Contains(md, "| Missing field | High | Open |") {
		t.Error("table rows not rendered")
	}
}

func TestAgentNameCaseInsensitive(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	postJSON(t, base+"/spaces/case-test/agent/API", AgentUpdate{
		Status: StatusActive, Summary: "posted as API",
	})

	code, body := getBody(t, base+"/spaces/case-test/agent/api")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	var got AgentUpdate
	json.Unmarshal([]byte(body), &got)
	if got.Summary != "posted as API" {
		t.Errorf("case-insensitive lookup failed: %q", got.Summary)
	}
}

func TestSpaceNotFoundReturns404(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	code, _ := getBody(t, base+"/spaces/nonexistent/raw")
	if code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", code)
	}
}

func TestQuestionsRenderedWithBossTag(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	postJSON(t, base+"/spaces/q-test/agent/api", AgentUpdate{
		Status:    StatusBlocked,
		Summary:   "Need decision",
		Questions: []string{"Should we use 200 or 202 for start?"},
	})

	code, md := getBody(t, base+"/spaces/q-test/raw")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if !strings.Contains(md, "[?BOSS] Should we use 200 or 202 for start?") {
		t.Error("question not rendered with [?BOSS] tag")
	}
}

func TestMultipleAgentsInOneSpace(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	for _, agent := range []string{"api", "cp", "sdk", "fe", "overlord"} {
		postJSON(t, base+"/spaces/multi/agent/"+agent, AgentUpdate{
			Status:  StatusActive,
			Summary: agent + " is working",
		})
	}

	code, body := getBody(t, base+"/spaces/multi/api/agents")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	var agents map[string]*AgentUpdate
	json.Unmarshal([]byte(body), &agents)
	if len(agents) != 5 {
		t.Errorf("expected 5 agents, got %d", len(agents))
	}
}

func TestProtocolInjectedOnNewSpace(t *testing.T) {
	dataDir := t.TempDir()

	srv := NewServer(":0", dataDir)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	base := serverBaseURL(srv)

	postJSON(t, base+"/spaces/local-reconciler/agent/review", AgentUpdate{
		Status:  StatusActive,
		Summary: "Reviewing local reconciler design",
	})

	code, md := getBody(t, base+"/spaces/local-reconciler/raw")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if !strings.Contains(md, "Shared Contracts") {
		t.Error("protocol not rendered in Shared Contracts section")
	}
	if !strings.Contains(md, "Space: `local-reconciler`") {
		t.Error("{SPACE} not substituted in protocol")
	}
	if !strings.Contains(md, "POST /spaces/local-reconciler/agent/{name}") {
		t.Error("{SPACE} not substituted in endpoint URLs")
	}
	if strings.Contains(md, "{SPACE}") {
		t.Error("raw {SPACE} placeholder still present in rendered output")
	}

	code, contracts := getBody(t, base+"/spaces/local-reconciler/contracts")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if !strings.Contains(contracts, "local-reconciler") {
		t.Error("contracts not populated with space name")
	}
}

func TestProtocolAlwaysInjected(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	postJSON(t, base+"/spaces/embedded-protocol/agent/test", AgentUpdate{
		Status: StatusIdle, Summary: "embedded protocol test",
	})

	code, md := getBody(t, base+"/spaces/embedded-protocol/raw")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if !strings.Contains(md, "Shared Contracts") {
		t.Error("Shared Contracts should always appear with embedded protocol")
	}
	if !strings.Contains(md, "Space: `embedded-protocol`") {
		t.Error("Embedded protocol should have space name substituted")
	}
}

func TestEmbeddedProtocolRespectsManualEdits(t *testing.T) {
	dataDir := t.TempDir()

	srv := NewServer(":0", dataDir)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	base := serverBaseURL(srv)

	postJSON(t, base+"/spaces/custom/agent/a", AgentUpdate{
		Status: StatusIdle, Summary: "seed",
	})

	postText(t, base+"/spaces/custom/contracts", "custom contracts override")

	postJSON(t, base+"/spaces/custom/agent/b", AgentUpdate{
		Status: StatusActive, Summary: "second agent",
	})

	_, contracts := getBody(t, base+"/spaces/custom/contracts")
	if !strings.Contains(contracts, "custom contracts override") {
		t.Error("embedded protocol should respect manual contract edits")
	}
	if strings.Contains(contracts, "Space: `custom`") {
		t.Errorf("embedded protocol should not overwrite manual contracts, got: %q", contracts)
	}
}

// TestProtocolHotReload is no longer relevant since protocol is embedded at compile time

func TestUpdatedAtTimestamp(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	before := time.Now().UTC().Add(-time.Second)
	postJSON(t, base+"/spaces/ts-test/agent/api", AgentUpdate{
		Status: StatusActive, Summary: "timestamp test",
	})
	after := time.Now().UTC().Add(time.Second)

	_, body := getBody(t, base+"/spaces/ts-test/agent/api")
	var got AgentUpdate
	json.Unmarshal([]byte(body), &got)

	if got.UpdatedAt.Before(before) || got.UpdatedAt.After(after) {
		t.Errorf("updated_at = %v, want between %v and %v", got.UpdatedAt, before, after)
	}
}

func TestDeleteSpace(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	postJSON(t, base+"/spaces/del-space/agent/api", AgentUpdate{
		Status: StatusDone, Summary: "to be nuked",
	})
	postJSON(t, base+"/spaces/del-space/agent/fe", AgentUpdate{
		Status: StatusActive, Summary: "also nuked",
	})

	req, _ := http.NewRequest(http.MethodDelete, base+"/spaces/del-space/", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	code, _ := getBody(t, base+"/spaces/del-space/raw")
	if code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", code)
	}
}

func TestDeleteSpaceNotFound(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	req, _ := http.NewRequest(http.MethodDelete, base+"/spaces/ghost/", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestSpaceJSONViaAcceptHeader(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	postJSON(t, base+"/spaces/json-test/agent/api", AgentUpdate{
		Status: StatusActive, Summary: "json view test",
	})

	req, _ := http.NewRequest(http.MethodGet, base+"/spaces/json-test/", nil)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var ks KnowledgeSpace
	if err := json.NewDecoder(resp.Body).Decode(&ks); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ks.Name != "json-test" {
		t.Errorf("name = %q, want %q", ks.Name, "json-test")
	}
	if len(ks.Agents) != 1 {
		t.Errorf("expected 1 agent, got %d", len(ks.Agents))
	}
	agent, ok := ks.Agents["api"]
	if !ok {
		t.Fatal("agent 'api' not found")
	}
	if agent.Summary != "json view test" {
		t.Errorf("summary = %q", agent.Summary)
	}
}

func TestSSEBroadcast(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/spaces/sse-test/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	received := make(chan string, 1)
	go func() {
		buf := make([]byte, 4096)
		n, _ := resp.Body.Read(buf)
		received <- string(buf[:n])
	}()

	time.Sleep(50 * time.Millisecond)

	postJSON(t, base+"/spaces/sse-test/agent/api", AgentUpdate{
		Status: StatusDone, Summary: "shipped",
	})

	select {
	case got := <-received:
		if !strings.Contains(got, "event: agent_updated") {
			t.Errorf("expected agent_updated event, got: %q", got)
		}
		if !strings.Contains(got, "shipped") {
			t.Errorf("expected summary in SSE data, got: %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Error("timed out waiting for SSE event")
	}
}

func TestSSEGlobalEndpoint(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	received := make(chan string, 1)
	go func() {
		buf := make([]byte, 4096)
		n, _ := resp.Body.Read(buf)
		received <- string(buf[:n])
	}()

	time.Sleep(50 * time.Millisecond)

	postJSON(t, base+"/spaces/any-space/agent/fe", AgentUpdate{
		Status: StatusActive, Summary: "working on UI",
	})

	select {
	case got := <-received:
		if !strings.Contains(got, "event: agent_updated") {
			t.Errorf("expected agent_updated event, got: %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Error("timed out waiting for SSE event on global endpoint")
	}
}

func TestClientDeleteAgent(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	postJSON(t, base+"/spaces/client-del/agent/api", AgentUpdate{
		Status: StatusDone, Summary: "to remove via client",
	})

	client := NewClient(base, "client-del")
	if err := client.DeleteAgent("api"); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}

	code, body := getBody(t, base+"/spaces/client-del/agent/api")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if body != "{}" {
		t.Errorf("expected empty agent, got %q", body)
	}
}

func TestClientDeleteSpace(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	postJSON(t, base+"/spaces/client-del-space/agent/api", AgentUpdate{
		Status: StatusIdle, Summary: "seed",
	})

	client := NewClient(base, "client-del-space")
	if err := client.DeleteSpace(); err != nil {
		t.Fatalf("DeleteSpace: %v", err)
	}

	code, _ := getBody(t, base+"/spaces/client-del-space/raw")
	if code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", code)
	}
}

func TestInterruptRecordedOnBossQuestion(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	postJSON(t, base+"/spaces/int-test/agent/FE", AgentUpdate{
		Status:    StatusBlocked,
		Summary:   "FE: needs direction",
		Branch:    "feat/frontend",
		PR:        "#640",
		Questions: []string{"[?BOSS] Should I rebase or start fresh?"},
	})

	code, body := getBody(t, base+"/spaces/int-test/factory/interrupts")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	var interrupts []Interrupt
	if err := json.Unmarshal([]byte(body), &interrupts); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(interrupts) != 1 {
		t.Fatalf("expected 1 interrupt, got %d", len(interrupts))
	}
	intr := interrupts[0]
	if intr.Type != InterruptDecision {
		t.Errorf("type = %q, want %q", intr.Type, InterruptDecision)
	}
	if intr.Agent != "FE" {
		t.Errorf("agent = %q, want FE", intr.Agent)
	}
	if intr.Space != "int-test" {
		t.Errorf("space = %q, want int-test", intr.Space)
	}
	if intr.Context["branch"] != "feat/frontend" {
		t.Errorf("context branch = %q", intr.Context["branch"])
	}
	if intr.Context["pr"] != "#640" {
		t.Errorf("context pr = %q", intr.Context["pr"])
	}
	if intr.Resolution != nil {
		t.Error("expected no resolution (pending)")
	}
}

func TestInterruptMetricsEndpoint(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	postJSON(t, base+"/spaces/metrics-test/agent/API", AgentUpdate{
		Status:    StatusActive,
		Summary:   "API: working",
		Questions: []string{"[?BOSS] Which approach?", "[?BOSS] What version?"},
	})

	code, body := getBody(t, base+"/spaces/metrics-test/factory/metrics")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	var metrics InterruptMetrics
	if err := json.Unmarshal([]byte(body), &metrics); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if metrics.TotalInterrupts != 2 {
		t.Errorf("total = %d, want 2", metrics.TotalInterrupts)
	}
	if metrics.PendingInterrupts != 2 {
		t.Errorf("pending = %d, want 2", metrics.PendingInterrupts)
	}
	if metrics.ByType["decision"] != 2 {
		t.Errorf("by_type[decision] = %d, want 2", metrics.ByType["decision"])
	}
	if metrics.ByAgent["API"] != 2 {
		t.Errorf("by_agent[API] = %d, want 2", metrics.ByAgent["API"])
	}
}

func TestInterruptLedgerPersistence(t *testing.T) {
	dataDir := t.TempDir()

	srv1 := NewServer(":0", dataDir)
	if err := srv1.Start(); err != nil {
		t.Fatal(err)
	}
	base1 := serverBaseURL(srv1)

	postJSON(t, base1+"/spaces/persist-int/agent/SDK", AgentUpdate{
		Status:    StatusBlocked,
		Summary:   "SDK: blocked",
		Questions: []string{"[?BOSS] Wait for API?"},
	})
	srv1.Stop()

	srv2 := NewServer(":0", dataDir)
	if err := srv2.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv2.Stop()
	base2 := serverBaseURL(srv2)

	code, body := getBody(t, base2+"/spaces/persist-int/factory/interrupts")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	var interrupts []Interrupt
	json.Unmarshal([]byte(body), &interrupts)
	if len(interrupts) != 1 {
		t.Fatalf("expected 1 interrupt after restart, got %d", len(interrupts))
	}
	if interrupts[0].Question != "[?BOSS] Wait for API?" {
		t.Errorf("question = %q", interrupts[0].Question)
	}
}

func TestInterruptEmptySpace(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	code, body := getBody(t, base+"/spaces/empty-int/factory/interrupts")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if strings.TrimSpace(body) != "[]" {
		t.Errorf("expected empty array, got %q", body)
	}

	code, body = getBody(t, base+"/spaces/empty-int/factory/metrics")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	var metrics InterruptMetrics
	json.Unmarshal([]byte(body), &metrics)
	if metrics.TotalInterrupts != 0 {
		t.Errorf("expected 0 interrupts, got %d", metrics.TotalInterrupts)
	}
}

func TestMultipleAgentsMultipleInterrupts(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	postJSON(t, base+"/spaces/multi-int/agent/FE", AgentUpdate{
		Status:    StatusBlocked,
		Summary:   "FE: needs help",
		Questions: []string{"[?BOSS] Rebase?", "[?BOSS] Which SDK?"},
	})
	postJSON(t, base+"/spaces/multi-int/agent/CP", AgentUpdate{
		Status:    StatusBlocked,
		Summary:   "CP: waiting",
		Questions: []string{"[?BOSS] Should CP proceed?"},
	})

	code, body := getBody(t, base+"/spaces/multi-int/factory/metrics")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	var metrics InterruptMetrics
	json.Unmarshal([]byte(body), &metrics)
	if metrics.TotalInterrupts != 3 {
		t.Errorf("total = %d, want 3", metrics.TotalInterrupts)
	}
	if metrics.ByAgent["FE"] != 2 {
		t.Errorf("by_agent[FE] = %d, want 2", metrics.ByAgent["FE"])
	}
	if metrics.ByAgent["CP"] != 1 {
		t.Errorf("by_agent[CP] = %d, want 1", metrics.ByAgent["CP"])
	}
}

func TestDeleteSpaceCleansUpFiles(t *testing.T) {
	dataDir := t.TempDir()
	srv := NewServer(":0", dataDir)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	base := serverBaseURL(srv)

	postJSON(t, base+"/spaces/file-cleanup/agent/api", AgentUpdate{
		Status: StatusDone, Summary: "test persistence cleanup",
	})

	jsonPath := filepath.Join(dataDir, "file-cleanup.json")
	mdPath := filepath.Join(dataDir, "file-cleanup.md")
	if _, err := os.Stat(jsonPath); os.IsNotExist(err) {
		t.Fatal("expected json file to exist before delete")
	}
	if _, err := os.Stat(mdPath); os.IsNotExist(err) {
		t.Fatal("expected md file to exist before delete")
	}

	req, _ := http.NewRequest(http.MethodDelete, base+"/spaces/file-cleanup/", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if _, err := os.Stat(jsonPath); !os.IsNotExist(err) {
		t.Error("expected json file to be deleted")
	}
	if _, err := os.Stat(mdPath); !os.IsNotExist(err) {
		t.Error("expected md file to be deleted")
	}
}

// ── Message system tests ──────────────────────────────────────────────

func postMessage(t *testing.T, baseURL, space, agent, sender, message string) *http.Response {
	t.Helper()
	url := baseURL + "/spaces/" + space + "/agent/" + agent + "/message"
	body := `{"message":"` + message + `"}`
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Name", sender)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST message: %v", err)
	}
	return resp
}

func TestMessagePostEndpoint(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	// First, create an agent
	postJSON(t, base+"/spaces/msg-test/agent/worker", AgentUpdate{
		Status:  StatusActive,
		Summary: "Working on task",
	})

	// Send a message to the agent
	resp := postMessage(t, base, "msg-test", "worker", "boss", "please review the PR")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	// Verify response contains delivery confirmation
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if result["status"] != "delivered" {
		t.Errorf("expected status=delivered, got %v", result["status"])
	}
	if result["recipient"] != "worker" {
		t.Errorf("expected recipient=worker, got %v", result["recipient"])
	}

	// Verify message is retrievable via GET agent JSON
	code, body := getBody(t, base+"/spaces/msg-test/agent/worker")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	var agent AgentUpdate
	json.Unmarshal([]byte(body), &agent)
	if len(agent.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(agent.Messages))
	}
	if agent.Messages[0].Message != "please review the PR" {
		t.Errorf("message = %q, want %q", agent.Messages[0].Message, "please review the PR")
	}
	if agent.Messages[0].Sender != "boss" {
		t.Errorf("sender = %q, want %q", agent.Messages[0].Sender, "boss")
	}
	if agent.Messages[0].ID == "" {
		t.Error("message ID should not be empty")
	}
}

func TestMessagePreservedOnAgentUpdate(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	// Create agent and send a message
	postJSON(t, base+"/spaces/preserve-test/agent/dev", AgentUpdate{
		Status:  StatusActive,
		Summary: "Working",
	})
	resp := postMessage(t, base, "preserve-test", "dev", "boss", "check the logs")
	resp.Body.Close()

	// Post an agent update (without messages field)
	resp2 := postJSON(t, base+"/spaces/preserve-test/agent/dev", AgentUpdate{
		Status:  StatusActive,
		Summary: "Still working",
		Items:   []string{"Fixed the bug"},
	})
	resp2.Body.Close()

	// Verify message is still there
	code, body := getBody(t, base+"/spaces/preserve-test/agent/dev")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	var agent AgentUpdate
	json.Unmarshal([]byte(body), &agent)
	if len(agent.Messages) != 1 {
		t.Fatalf("expected 1 message after update, got %d — messages were wiped", len(agent.Messages))
	}
	if agent.Messages[0].Message != "check the logs" {
		t.Errorf("message = %q, want %q", agent.Messages[0].Message, "check the logs")
	}
	// Verify the update itself was applied
	if agent.Summary != "Still working" {
		t.Errorf("summary = %q, want %q", agent.Summary, "Still working")
	}
}

func TestMessageRenderedInMarkdown(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	// Create agent and send messages
	postJSON(t, base+"/spaces/md-test/agent/api", AgentUpdate{
		Status:  StatusActive,
		Summary: "Implementing endpoints",
	})
	resp := postMessage(t, base, "md-test", "api", "boss", "prioritize the health check")
	resp.Body.Close()
	resp = postMessage(t, base, "md-test", "api", "frontend", "I need the /users endpoint first")
	resp.Body.Close()

	// GET /raw and verify messages appear in markdown
	code, md := getBody(t, base+"/spaces/md-test/raw")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if !strings.Contains(md, "#### Messages") {
		t.Error("markdown should contain '#### Messages' section")
	}
	if !strings.Contains(md, "prioritize the health check") {
		t.Error("markdown should contain first message text")
	}
	if !strings.Contains(md, "I need the /users endpoint first") {
		t.Error("markdown should contain second message text")
	}
	if !strings.Contains(md, "**boss**") {
		t.Error("markdown should contain sender name 'boss'")
	}
	if !strings.Contains(md, "**frontend**") {
		t.Error("markdown should contain sender name 'frontend'")
	}
}

func TestMessageValidation(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	// Create agent first
	postJSON(t, base+"/spaces/val-test/agent/worker", AgentUpdate{
		Status:  StatusActive,
		Summary: "Working",
	})

	// Test: missing X-Agent-Name header
	url := base + "/spaces/val-test/agent/worker/message"
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(`{"message":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	// deliberately NOT setting X-Agent-Name
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing X-Agent-Name: expected 400, got %d", resp.StatusCode)
	}

	// Test: empty message body
	resp = postMessage(t, base, "val-test", "worker", "boss", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("empty message: expected 400, got %d", resp.StatusCode)
	}

	// Test: whitespace-only message
	url = base + "/spaces/val-test/agent/worker/message"
	req, _ = http.NewRequest(http.MethodPost, url, strings.NewReader(`{"message":"   "}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Name", "boss")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("whitespace message: expected 400, got %d", resp.StatusCode)
	}

	// Test: GET method not allowed
	req, _ = http.NewRequest(http.MethodGet, url, nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET on message endpoint: expected 405, got %d", resp.StatusCode)
	}
}

func TestMessageLimit(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	// Create agent
	postJSON(t, base+"/spaces/limit-test/agent/worker", AgentUpdate{
		Status:  StatusActive,
		Summary: "Working",
	})

	// Send 55 messages
	for i := 0; i < 55; i++ {
		resp := postMessage(t, base, "limit-test", "worker", "boss",
			"message number "+strings.Repeat("x", 3)+string(rune('A'+i%26)))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("message %d: expected 200, got %d", i, resp.StatusCode)
		}
	}

	// All 55 unread messages must be retained (unread messages are never dropped).
	code, body := getBody(t, base+"/spaces/limit-test/agent/worker")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	var agent AgentUpdate
	json.Unmarshal([]byte(body), &agent)
	if len(agent.Messages) != 55 {
		t.Errorf("expected 55 unread messages (all retained), got %d", len(agent.Messages))
	}
}

func TestMessageSSEBroadcast(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	// Create agent
	postJSON(t, base+"/spaces/sse-msg-test/agent/worker", AgentUpdate{
		Status:  StatusActive,
		Summary: "Working",
	})

	// Connect SSE
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", base+"/spaces/sse-msg-test/events", nil)
	sseResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	defer sseResp.Body.Close()

	// Give SSE a moment to connect
	time.Sleep(100 * time.Millisecond)

	// Send a message
	resp := postMessage(t, base, "sse-msg-test", "worker", "boss", "check your inbox")
	resp.Body.Close()

	// Read SSE events — look for agent_message
	buf := make([]byte, 4096)
	n, _ := sseResp.Body.Read(buf)
	data := string(buf[:n])
	if !strings.Contains(data, "agent_message") {
		t.Error("SSE should broadcast 'agent_message' event")
	}
	if !strings.Contains(data, "check your inbox") {
		t.Error("SSE event should contain the message text")
	}
}

func TestMessageToNonexistentAgentCreatesAgent(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	// Send message to an agent that doesn't exist yet
	resp := postMessage(t, base, "ghost-test", "phantom", "boss", "wake up")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	// Verify agent was auto-created with the message
	code, body := getBody(t, base+"/spaces/ghost-test/agent/phantom")
	if code != http.StatusOK {
		t.Fatalf("expected 200 for auto-created agent, got %d", code)
	}
	var agent AgentUpdate
	json.Unmarshal([]byte(body), &agent)
	if agent.Status != StatusIdle {
		t.Errorf("auto-created agent status = %q, want %q", agent.Status, StatusIdle)
	}
	if len(agent.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(agent.Messages))
	}
	if agent.Messages[0].Message != "wake up" {
		t.Errorf("message = %q, want %q", agent.Messages[0].Message, "wake up")
	}
}

func TestIgnitionEndpoint(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	// Create a peer agent so ignition shows it
	postJSON(t, base+"/spaces/ignite-test/agent/peer", AgentUpdate{
		Status:  StatusActive,
		Summary: "Peer is working",
	})

	// GET ignition for a new agent
	code, body := getBody(t, base+"/spaces/ignite-test/ignition/newagent?session_id=test_session_123")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}

	// Verify key sections exist
	if !strings.Contains(body, "# Agent Ignition: newagent") {
		t.Error("missing ignition title")
	}
	if !strings.Contains(body, "You are **newagent**") {
		t.Error("missing agent identity")
	}
	if !strings.Contains(body, "test_session_123") {
		t.Error("missing tmux session in response")
	}
	if !strings.Contains(body, "Peer") {
		t.Error("missing peer agent in response")
	}
	if !strings.Contains(body, "/message") {
		t.Error("ignition should document the /message endpoint")
	}

	// Verify tmux session was registered
	agentCode, agentBody := getBody(t, base+"/spaces/ignite-test/agent/newagent")
	if agentCode != http.StatusOK {
		t.Fatalf("expected 200 for registered agent, got %d", agentCode)
	}
	var agent AgentUpdate
	json.Unmarshal([]byte(agentBody), &agent)
	if agent.SessionID != "test_session_123" {
		t.Errorf("session_id = %q, want %q", agent.SessionID, "test_session_123")
	}
}

func TestIgnitionShowsPendingMessages(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	// Create agent and send a message
	postJSON(t, base+"/spaces/ignite-msg-test/agent/worker", AgentUpdate{
		Status:  StatusIdle,
		Summary: "Idle",
	})
	resp := postMessage(t, base, "ignite-msg-test", "worker", "boss", "start working on feature X")
	resp.Body.Close()

	// GET ignition — should show pending messages
	code, body := getBody(t, base+"/spaces/ignite-msg-test/ignition/worker")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if !strings.Contains(body, "Pending Messages") {
		t.Error("ignition should show 'Pending Messages' section")
	}
	if !strings.Contains(body, "start working on feature X") {
		t.Error("ignition should show the pending message text")
	}
	if !strings.Contains(body, "**boss**") {
		t.Error("ignition should show the message sender")
	}
}

// TestIgnitionShowsTaskListEndpoint verifies that the ignition response includes
// the task list endpoint so agents know where to fetch their task queue.
func TestIgnitionShowsTaskListEndpoint(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	code, body := getBody(t, base+"/spaces/ignite-ep-test/ignition/myagent")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if !strings.Contains(body, "/spaces/ignite-ep-test/tasks") {
		t.Error("ignition should reference task list endpoint")
	}
}

func TestLineIsIdleIndicator(t *testing.T) {
	tests := []struct {
		name string
		line string
		want bool
	}{
		// Claude Code prompt (exact ">" inside box-drawing)
		{"claude code prompt bare", "│ > │", true},
		{"claude code prompt no box", ">", true},
		{"claude code prompt with space", "> ", true},
		{"claude code prompt inner space", "│ >  │", true},

		// Shell prompts
		{"bash dollar", "user@host:~/code$ ", true},
		{"bare dollar", "$", true},
		{"zsh percent", "% ", true},
		{"root hash", "root@box:/# ", true},
		{"fish prompt", "~/code ❯ ", true},
		{"angle bracket prompt", ">>> ", true},

		// Claude Code prompt with auto-suggestion
		{"claude code prompt bare chevron", "❯", true},
		{"claude code prompt with suggestion", "❯ give me something to work on", true},
		{"claude code prompt chevron space", "❯ ", true},

		// Claude Code / opencode hint lines
		{"shortcuts hint", "? for shortcuts", true},
		{"auto-compact", "  auto-compact enabled", true},
		{"auto-accept", "  auto-accept on", true},

		// Claude Code status bar (vim mode)
		{"insert mode", "  -- INSERT -- ⏵⏵ bypass permissions on (shift+tab to cycle)                                             current: 2.1.70 · latest: 2.1.70", true},
		{"normal mode", "  -- NORMAL --                                                                                            current: 2.1.70 · latest: 2.1.70", true},

		// OpenCode status bar
		{"opencode status bar", "                                  ctrl+t variants  tab agents  ctrl+p commands    • OpenCode 1.2.17", true},

		// OpenCode / generic idle keywords
		{"waiting for input", "Waiting for input...", true},
		{"ready", "Ready", true},
		{"type a message", "Type a message to begin", true},

		// Busy indicators — should NOT match
		{"running command output", "Building project...", false},
		{"file content", "func main() {", false},
		{"progress bar", "[=====>    ] 50%", false},
		{"error output", "Error: file not found", false},
		{"git diff line", "+++ b/file.go", false},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lineIsIdleIndicator(tt.line)
			if got != tt.want {
				t.Errorf("lineIsIdleIndicator(%q) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}

func TestIsShellPrompt(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"$", true},
		{"$ ", true},
		{"user@host:~$ ", true},
		{"%", true},
		{"zsh% ", true},
		{">", true},
		{">>> ", true},
		{"#", true},
		{"root@box:/# ", true},
		{"~/code ❯ ", true},
		{"❯", true},
		// Not prompts
		{"", false},
		{"hello world", false},
		{"func main() {", false},
		{"Building...", false},
	}

	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			got := isShellPrompt(tt.line)
			if got != tt.want {
				t.Errorf("isShellPrompt(%q) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}

// TestServerDataDirAutoCreate verifies the server creates DATA_DIR if it does not exist.
func TestServerDataDirAutoCreate(t *testing.T) {
	parent := t.TempDir()
	dataDir := filepath.Join(parent, "nested", "data")

	srv := NewServer(":0", dataDir)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start with missing dataDir: %v", err)
	}
	defer srv.Stop()

	if _, err := os.Stat(dataDir); err != nil {
		t.Errorf("expected dataDir to be created, got: %v", err)
	}
}

// TestServerMissingXAgentNameHeader verifies POST without X-Agent-Name returns 400 JSON error.
func TestServerMissingXAgentNameHeader(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	req, _ := http.NewRequest(http.MethodPost,
		base+"/spaces/s/agent/Bot",
		strings.NewReader(`{"status":"active","summary":"Bot: hi"}`))
	req.Header.Set("Content-Type", "application/json")
	// Intentionally no X-Agent-Name header

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("expected JSON Content-Type, got %q", ct)
	}
	var errResp map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Errorf("response body should be JSON: %v", err)
	}
	if errResp["error"] == "" {
		t.Error("expected non-empty 'error' field in JSON response")
	}
}

// TestServerMalformedJSONBody verifies POST with malformed JSON returns 400 JSON error without panic.
func TestServerMalformedJSONBody(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	req, _ := http.NewRequest(http.MethodPost,
		base+"/spaces/s/agent/Bot",
		strings.NewReader(`{not valid json`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Name", "Bot")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("expected JSON Content-Type on error, got %q", ct)
	}
}

// TestServerForbiddenCrossChannelPost verifies posting to another agent's channel returns 403 JSON.
func TestServerForbiddenCrossChannelPost(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	req, _ := http.NewRequest(http.MethodPost,
		base+"/spaces/s/agent/Alice",
		strings.NewReader(`{"status":"active","summary":"Bob: sneaky"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Name", "Bob") // posting to Alice's channel

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("expected JSON Content-Type on 403, got %q", ct)
	}
}

// TestAgentRegisterThenMessageThenJournalEvent is a cross-feature integration test:
// an agent registers, another sends it a message, verify both events appear in the journal.
func TestAgentRegisterThenMessageThenJournalEvent(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "crossfeature"

	// 1. Register agent Alpha
	resp := postJSON(t, base+"/spaces/"+space+"/agent/Alpha", AgentUpdate{
		Status:  StatusActive,
		Summary: "Alpha: ready",
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("register Alpha: expected 202, got %d", resp.StatusCode)
	}

	// 2. Send a message from Beta to Alpha
	code, body := postJSONWithSender(t,
		base+"/spaces/"+space+"/agent/Alpha/message",
		map[string]string{"message": "cross-feature test message"},
		"Beta",
	)
	if code != http.StatusOK {
		t.Fatalf("send message: expected 200, got %d: %s", code, body)
	}

	// 3. Journal should contain both agent_updated and message_sent events
	code2, evBody := getBody(t, base+"/spaces/"+space+"/api/events")
	if code2 != http.StatusOK {
		t.Fatalf("get events: expected 200, got %d", code2)
	}
	var events []SpaceEvent
	if err := json.Unmarshal([]byte(evBody), &events); err != nil {
		t.Fatalf("unmarshal events: %v", err)
	}

	hasAgentUpdated := false
	hasMessageSent := false
	for _, ev := range events {
		if ev.Type == EventAgentUpdated && strings.EqualFold(ev.Agent, "Alpha") {
			hasAgentUpdated = true
		}
		if ev.Type == EventMessageSent && strings.EqualFold(ev.Agent, "Alpha") {
			hasMessageSent = true
		}
	}
	if !hasAgentUpdated {
		t.Error("expected agent_updated event for Alpha in journal")
	}
	if !hasMessageSent {
		t.Error("expected message_sent event for Alpha in journal")
	}
}

// TestServerEmptyMessageBody verifies sending a message with empty content returns 400.
func TestServerEmptyMessageBody(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "emptymsg"

	// Create agent first
	resp := postJSON(t, base+"/spaces/"+space+"/agent/Recv", AgentUpdate{
		Status:  StatusActive,
		Summary: "Recv: ready",
	})
	resp.Body.Close()

	// Send empty message
	code, body := postJSONWithSender(t,
		base+"/spaces/"+space+"/agent/Recv/message",
		map[string]string{"message": ""},
		"Sender",
	)
	if code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty message, got %d: %s", code, body)
	}
}

// ── Hierarchy tests ──────────────────────────────────────────────────────────

func postAgentWithParent(t *testing.T, base, space, name, parent, role string) {
	t.Helper()
	resp := postJSON(t, base+"/spaces/"+space+"/agent/"+name, AgentUpdate{
		Status:  StatusActive,
		Summary: name + ": active",
		Parent:  parent,
		Role:    role,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("postAgentWithParent %s: got %d: %s", name, resp.StatusCode, body)
	}
}

func getHierarchy(t *testing.T, base, space string) *HierarchyTree {
	t.Helper()
	resp, err := http.Get(base + "/spaces/" + space + "/hierarchy")
	if err != nil {
		t.Fatalf("get hierarchy: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("get hierarchy: got %d: %s", resp.StatusCode, b)
	}
	var tree HierarchyTree
	if err := json.NewDecoder(resp.Body).Decode(&tree); err != nil {
		t.Fatalf("decode hierarchy: %v", err)
	}
	return &tree
}

func TestRebuildChildren(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "hier-rebuild"

	postAgentWithParent(t, base, space, "Manager", "", "manager")
	postAgentWithParent(t, base, space, "Worker1", "Manager", "worker")
	postAgentWithParent(t, base, space, "Worker2", "Manager", "worker")

	tree := getHierarchy(t, base, space)

	mgr, ok := tree.Nodes["Manager"]
	if !ok {
		t.Fatal("Manager node missing from hierarchy")
	}
	if len(mgr.Children) != 2 {
		t.Errorf("Manager.Children = %v, want [Worker1, Worker2]", mgr.Children)
	}
	for _, w := range []string{"Worker1", "Worker2"} {
		node, ok := tree.Nodes[w]
		if !ok {
			t.Fatalf("%s node missing from hierarchy", w)
		}
		if node.Parent != "Manager" {
			t.Errorf("%s.Parent = %q, want Manager", w, node.Parent)
		}
	}
}

func TestRebuildChildrenCycleRejected(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "hier-cycle"

	postAgentWithParent(t, base, space, "A", "", "")
	postAgentWithParent(t, base, space, "B", "A", "")

	// Now try to make A a child of B — should create A→B→A cycle → 400
	data, _ := json.Marshal(AgentUpdate{Status: StatusActive, Summary: "A: cycle attempt", Parent: "B"})
	req, _ := http.NewRequest(http.MethodPost, base+"/spaces/"+space+"/agent/A", strings.NewReader(string(data)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Name", "A")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("cycle post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for cycle, got %d", resp.StatusCode)
	}
}

func TestRebuildChildrenOrphanParent(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "hier-orphan"

	// Declare parent for non-existent agent — should be stored but not appear in Children
	postAgentWithParent(t, base, space, "Orphan", "NonExistentManager", "worker")

	tree := getHierarchy(t, base, space)
	node, ok := tree.Nodes["Orphan"]
	if !ok {
		t.Fatal("Orphan node missing from hierarchy")
	}
	if node.Parent != "NonExistentManager" {
		t.Errorf("Orphan.Parent = %q, want NonExistentManager", node.Parent)
	}
	// NonExistentManager should not be in nodes (not registered)
	if _, exists := tree.Nodes["NonExistentManager"]; exists {
		t.Error("NonExistentManager should not appear in hierarchy nodes (not registered)")
	}
}

func TestScopeSubtreeDelivery(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "hier-subtree"

	postAgentWithParent(t, base, space, "Manager", "", "manager")
	postAgentWithParent(t, base, space, "Worker1", "Manager", "worker")
	postAgentWithParent(t, base, space, "Worker2", "Manager", "worker")

	// Fan-out message to Manager subtree
	code, body := postJSONWithSender(t,
		base+"/spaces/"+space+"/agent/Manager/message?scope=subtree",
		map[string]string{"message": "team directive"},
		"Boss",
	)
	if code != http.StatusAccepted {
		t.Fatalf("subtree message: got %d: %s", code, body)
	}

	// Verify all 3 agents received the message
	time.Sleep(50 * time.Millisecond) // allow async goroutines to settle
	for _, name := range []string{"Manager", "Worker1", "Worker2"} {
		resp, err := http.Get(base + "/spaces/" + space + "/agent/" + name)
		if err != nil {
			t.Fatalf("get %s: %v", name, err)
		}
		defer resp.Body.Close()
		var ag AgentUpdate
		json.NewDecoder(resp.Body).Decode(&ag)
		found := false
		for _, m := range ag.Messages {
			if m.Message == "team directive" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s did not receive subtree message", name)
		}
	}
}

func TestScopeSubtreeLeafIsNoop(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "hier-leaf"

	postAgentWithParent(t, base, space, "Leaf", "", "worker")

	// Subtree to leaf agent (no children) — should succeed with 202
	code, body := postJSONWithSender(t,
		base+"/spaces/"+space+"/agent/Leaf/message?scope=subtree",
		map[string]string{"message": "leaf message"},
		"Boss",
	)
	if code != http.StatusAccepted {
		t.Fatalf("leaf subtree: got %d: %s", code, body)
	}

	// Verify leaf received message
	time.Sleep(50 * time.Millisecond)
	resp, err := http.Get(base + "/spaces/" + space + "/agent/Leaf")
	if err != nil {
		t.Fatalf("get Leaf: %v", err)
	}
	defer resp.Body.Close()
	var ag AgentUpdate
	json.NewDecoder(resp.Body).Decode(&ag)
	if len(ag.Messages) == 0 || ag.Messages[0].Message != "leaf message" {
		t.Errorf("Leaf did not receive message: %+v", ag.Messages)
	}
}

func TestParentEscalation(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "hier-escalate"

	postAgentWithParent(t, base, space, "Manager", "", "manager")
	postAgentWithParent(t, base, space, "Worker", "Manager", "worker")

	// Worker escalates to parent
	code, body := postJSONWithSender(t,
		base+"/spaces/"+space+"/agent/parent/message",
		map[string]string{"message": "escalation from worker"},
		"Worker",
	)
	if code != http.StatusOK {
		t.Fatalf("parent escalation: got %d: %s", code, body)
	}

	// Verify Manager received the message
	time.Sleep(50 * time.Millisecond)
	resp, err := http.Get(base + "/spaces/" + space + "/agent/Manager")
	if err != nil {
		t.Fatalf("get Manager: %v", err)
	}
	defer resp.Body.Close()
	var ag AgentUpdate
	json.NewDecoder(resp.Body).Decode(&ag)
	found := false
	for _, m := range ag.Messages {
		if m.Message == "escalation from worker" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Manager did not receive escalated message from Worker")
	}
}

func TestParentEscalationNoParent(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "hier-noparent"

	postAgentWithParent(t, base, space, "Rootless", "", "worker")

	code, body := postJSONWithSender(t,
		base+"/spaces/"+space+"/agent/parent/message",
		map[string]string{"message": "should fail"},
		"Rootless",
	)
	if code != http.StatusBadRequest {
		t.Errorf("expected 400 for escalation with no parent, got %d: %s", code, body)
	}
}

func TestFlatAgentsUnaffectedByHierarchy(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "hier-flat"

	// Create agents with no parent (flat workflow)
	resp1 := postJSON(t, base+"/spaces/"+space+"/agent/Alpha", AgentUpdate{Status: StatusActive, Summary: "Alpha: active"})
	resp1.Body.Close()
	resp2 := postJSON(t, base+"/spaces/"+space+"/agent/Beta", AgentUpdate{Status: StatusActive, Summary: "Beta: active"})
	resp2.Body.Close()

	// Direct message still works
	code, body := postJSONWithSender(t,
		base+"/spaces/"+space+"/agent/Alpha/message",
		map[string]string{"message": "hello"},
		"Beta",
	)
	if code != http.StatusOK {
		t.Errorf("flat direct message: got %d: %s", code, body)
	}

	// Hierarchy endpoint returns both as roots
	tree := getHierarchy(t, base, space)
	if len(tree.Roots) != 2 {
		t.Errorf("expected 2 roots for flat space, got %d: %v", len(tree.Roots), tree.Roots)
	}
	for _, r := range []string{"Alpha", "Beta"} {
		node, ok := tree.Nodes[r]
		if !ok {
			t.Fatalf("%s missing from flat hierarchy", r)
		}
		if node.Parent != "" {
			t.Errorf("%s.Parent = %q, want empty", r, node.Parent)
		}
	}
}

func TestHierarchyEndpoint(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "hier-endpoint"

	// Use Title-case names (resolveAgentName canonicalizes to Title case)
	postAgentWithParent(t, base, space, "Cto", "", "manager")
	postAgentWithParent(t, base, space, "Dev", "Cto", "worker")
	postAgentWithParent(t, base, space, "Sme", "Dev", "sme")

	tree := getHierarchy(t, base, space)
	if tree.Space != space {
		t.Errorf("tree.Space = %q, want %q", tree.Space, space)
	}
	if len(tree.Roots) != 1 || tree.Roots[0] != "Cto" {
		t.Errorf("tree.Roots = %v, want [Cto]", tree.Roots)
	}
	cto := tree.Nodes["Cto"]
	if cto == nil {
		t.Fatal("Cto node missing from hierarchy")
	}
	if cto.Depth != 0 {
		t.Errorf("Cto depth = %d, want 0", cto.Depth)
	}
	dev := tree.Nodes["Dev"]
	if dev == nil {
		t.Fatal("Dev node missing from hierarchy")
	}
	if dev.Depth != 1 {
		t.Errorf("Dev depth = %d, want 1", dev.Depth)
	}
	sme := tree.Nodes["Sme"]
	if sme == nil {
		t.Fatal("Sme node missing from hierarchy")
	}
	if sme.Depth != 2 {
		t.Errorf("Sme depth = %d, want 2", sme.Depth)
	}
	if len(cto.Children) != 1 || cto.Children[0] != "Dev" {
		t.Errorf("Cto.Children = %v, want [Dev]", cto.Children)
	}
}

func TestChildrenNotClientSettable(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "hier-children-immutable"

	// Agent posts with a bogus Children list
	data, _ := json.Marshal(map[string]interface{}{
		"status":   "active",
		"summary":  "Agent: trying to set children",
		"children": []string{"FakeChild1", "FakeChild2"},
	})
	req, _ := http.NewRequest(http.MethodPost, base+"/spaces/"+space+"/agent/Agent", strings.NewReader(string(data)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Name", "Agent")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()

	// Retrieve the agent and verify Children is empty (server rejected the client-supplied value)
	getResp, err := http.Get(base + "/spaces/" + space + "/agent/Agent")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer getResp.Body.Close()
	var ag AgentUpdate
	json.NewDecoder(getResp.Body).Decode(&ag)
	if len(ag.Children) != 0 {
		t.Errorf("Children should be empty (server-managed), got %v", ag.Children)
	}
}

// ---- Task Management Tests ----

func taskURL(base, space, rest string) string {
	if rest == "" {
		return base + "/spaces/" + space + "/tasks"
	}
	return base + "/spaces/" + space + "/tasks/" + rest
}

func postTaskJSON(t *testing.T, url string, payload any, agentName string) *http.Response {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Name", agentName)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func putTaskJSON(t *testing.T, url string, payload any, agentName string) *http.Response {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Name", agentName)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", url, err)
	}
	return resp
}

func deleteReq(t *testing.T, url, agentName string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if agentName != "" {
		req.Header.Set("X-Agent-Name", agentName)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", url, err)
	}
	return resp
}

func TestTaskCreate(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	resp := postTaskJSON(t, taskURL(base, "myspace", ""), map[string]any{
		"title":       "Fix SSE reconnect",
		"priority":    "high",
		"assigned_to": "DevAgent",
		"labels":      []string{"backend"},
	}, "ManagerAgent")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var task Task
	json.NewDecoder(resp.Body).Decode(&task)

	if task.ID != "TASK-001" {
		t.Errorf("expected TASK-001, got %q", task.ID)
	}
	if task.Status != TaskStatusBacklog {
		t.Errorf("expected backlog status, got %q", task.Status)
	}
	if task.CreatedBy != "ManagerAgent" {
		t.Errorf("created_by = %q, want ManagerAgent", task.CreatedBy)
	}
	if task.Priority != "high" {
		t.Errorf("priority = %q, want high", task.Priority)
	}
	if task.AssignedTo != "DevAgent" {
		t.Errorf("assigned_to = %q, want DevAgent", task.AssignedTo)
	}
}

func TestTaskCreateMissingTitle(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	resp := postTaskJSON(t, taskURL(base, "myspace", ""), map[string]any{
		"priority": "high",
	}, "ManagerAgent")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestTaskCreateMissingAgentName(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	data, _ := json.Marshal(map[string]any{"title": "Some task"})
	req, _ := http.NewRequest(http.MethodPost, taskURL(base, "myspace", ""), strings.NewReader(string(data)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestTaskListEmpty(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	code, body := getBody(t, taskURL(base, "emptyspace", ""))
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	var result struct {
		Tasks []Task `json:"tasks"`
		Total int    `json:"total"`
	}
	json.Unmarshal([]byte(body), &result)
	if result.Total != 0 {
		t.Errorf("expected 0 tasks, got %d", result.Total)
	}
}

func TestTaskListWithTasks(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "listspace"

	// Create 3 tasks
	for i, title := range []string{"Task A", "Task B", "Task C"} {
		resp := postTaskJSON(t, taskURL(base, space, ""), map[string]any{"title": title, "priority": "low"}, "Creator")
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create task %d: got %d", i, resp.StatusCode)
		}
	}

	code, body := getBody(t, taskURL(base, space, ""))
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	var result struct {
		Tasks []Task `json:"tasks"`
		Total int    `json:"total"`
	}
	json.Unmarshal([]byte(body), &result)
	if result.Total != 3 {
		t.Errorf("expected 3 tasks, got %d", result.Total)
	}
}

func TestTaskListFilterByStatus(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "filterspace"

	// Create task then move it
	resp := postTaskJSON(t, taskURL(base, space, ""), map[string]any{"title": "Movable"}, "Mgr")
	resp.Body.Close()
	postTaskJSON(t, taskURL(base, space, "TASK-001/move"), map[string]any{"status": "in_progress"}, "Mgr").Body.Close()

	// Filter by in_progress
	code, body := getBody(t, taskURL(base, space, "")+"?status=in_progress")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	var result struct {
		Tasks []Task `json:"tasks"`
		Total int    `json:"total"`
	}
	json.Unmarshal([]byte(body), &result)
	if result.Total != 1 || result.Tasks[0].Status != TaskStatusInProgress {
		t.Errorf("expected 1 in_progress task, got %+v", result)
	}

	// Filter by backlog — should be 0 now
	code, body = getBody(t, taskURL(base, space, "")+"?status=backlog")
	json.Unmarshal([]byte(body), &result)
	if result.Total != 0 {
		t.Errorf("expected 0 backlog tasks, got %d", result.Total)
	}
}

func TestTaskListFilterByAssignee(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "assignspace"

	postTaskJSON(t, taskURL(base, space, ""), map[string]any{"title": "For Alice", "assigned_to": "Alice"}, "Mgr").Body.Close()
	postTaskJSON(t, taskURL(base, space, ""), map[string]any{"title": "For Bob", "assigned_to": "Bob"}, "Mgr").Body.Close()

	code, body := getBody(t, taskURL(base, space, "")+"?assigned_to=Alice")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	var result struct {
		Tasks []Task `json:"tasks"`
		Total int    `json:"total"`
	}
	json.Unmarshal([]byte(body), &result)
	if result.Total != 1 || result.Tasks[0].AssignedTo != "Alice" {
		t.Errorf("expected 1 Alice task, got %+v", result)
	}
}

func TestTaskListFilterByLabel(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "labelspace"

	postTaskJSON(t, taskURL(base, space, ""), map[string]any{"title": "Backend task", "labels": []string{"backend", "api"}}, "Mgr").Body.Close()
	postTaskJSON(t, taskURL(base, space, ""), map[string]any{"title": "Frontend task", "labels": []string{"frontend"}}, "Mgr").Body.Close()

	code, body := getBody(t, taskURL(base, space, "")+"?label=backend")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	var result struct {
		Tasks []Task `json:"tasks"`
		Total int    `json:"total"`
	}
	json.Unmarshal([]byte(body), &result)
	if result.Total != 1 {
		t.Errorf("expected 1 backend task, got %d", result.Total)
	}
}

func TestTaskListFilterByPriority(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "priospace"

	postTaskJSON(t, taskURL(base, space, ""), map[string]any{"title": "Urgent one", "priority": "urgent"}, "Mgr").Body.Close()
	postTaskJSON(t, taskURL(base, space, ""), map[string]any{"title": "Low one", "priority": "low"}, "Mgr").Body.Close()

	code, body := getBody(t, taskURL(base, space, "")+"?priority=urgent")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	var result struct {
		Tasks []Task `json:"tasks"`
		Total int    `json:"total"`
	}
	json.Unmarshal([]byte(body), &result)
	if result.Total != 1 || result.Tasks[0].Priority != "urgent" {
		t.Errorf("expected 1 urgent task, got %+v", result)
	}
}

func TestTaskGet(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "getspace"

	postTaskJSON(t, taskURL(base, space, ""), map[string]any{"title": "Gettable task"}, "Mgr").Body.Close()

	code, body := getBody(t, taskURL(base, space, "TASK-001"))
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", code, body)
	}
	var task Task
	json.Unmarshal([]byte(body), &task)
	if task.ID != "TASK-001" {
		t.Errorf("expected TASK-001, got %q", task.ID)
	}
	if task.Title != "Gettable task" {
		t.Errorf("expected 'Gettable task', got %q", task.Title)
	}
}

func TestTaskGetNotFound(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	code, _ := getBody(t, taskURL(base, "myspace", "TASK-999"))
	if code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", code)
	}
}

func TestTaskUpdate(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "updatespace"

	postTaskJSON(t, taskURL(base, space, ""), map[string]any{"title": "Original title"}, "Mgr").Body.Close()

	newTitle := "Updated title"
	resp := putTaskJSON(t, taskURL(base, space, "TASK-001"), map[string]any{
		"title":    newTitle,
		"priority": "high",
	}, "Mgr")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var task Task
	json.NewDecoder(resp.Body).Decode(&task)
	if task.Title != newTitle {
		t.Errorf("title = %q, want %q", task.Title, newTitle)
	}
	if task.Priority != "high" {
		t.Errorf("priority = %q, want high", task.Priority)
	}
}

func TestTaskDelete(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "delspace"

	postTaskJSON(t, taskURL(base, space, ""), map[string]any{"title": "To delete"}, "Mgr").Body.Close()

	resp := deleteReq(t, taskURL(base, space, "TASK-001"), "Mgr")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	// Verify gone
	code, _ := getBody(t, taskURL(base, space, "TASK-001"))
	if code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", code)
	}
}

func TestTaskMove(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "movespace"

	postTaskJSON(t, taskURL(base, space, ""), map[string]any{"title": "Movable"}, "Mgr").Body.Close()

	resp := postTaskJSON(t, taskURL(base, space, "TASK-001/move"), map[string]any{"status": "in_progress"}, "DevAgent")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var task Task
	json.NewDecoder(resp.Body).Decode(&task)
	if task.Status != TaskStatusInProgress {
		t.Errorf("status = %q, want in_progress", task.Status)
	}
}

func TestTaskMoveInvalidStatus(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "movevalspace"

	postTaskJSON(t, taskURL(base, space, ""), map[string]any{"title": "Task"}, "Mgr").Body.Close()

	resp := postTaskJSON(t, taskURL(base, space, "TASK-001/move"), map[string]any{"status": "nonexistent"}, "DevAgent")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestTaskAssign(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "assigntestspace"

	postTaskJSON(t, taskURL(base, space, ""), map[string]any{"title": "Assignable"}, "Mgr").Body.Close()

	resp := postTaskJSON(t, taskURL(base, space, "TASK-001/assign"), map[string]any{"assigned_to": "WorkerBot"}, "Mgr")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var task Task
	json.NewDecoder(resp.Body).Decode(&task)
	if task.AssignedTo != "WorkerBot" {
		t.Errorf("assigned_to = %q, want WorkerBot", task.AssignedTo)
	}
}

func TestTaskComment(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "commentspace"

	postTaskJSON(t, taskURL(base, space, ""), map[string]any{"title": "Commentable"}, "Mgr").Body.Close()

	resp := postTaskJSON(t, taskURL(base, space, "TASK-001/comment"), map[string]any{"body": "Started investigation."}, "DevAgent")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var task Task
	json.NewDecoder(resp.Body).Decode(&task)
	if len(task.Comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(task.Comments))
	}
	if task.Comments[0].Author != "DevAgent" {
		t.Errorf("comment author = %q, want DevAgent", task.Comments[0].Author)
	}
	if task.Comments[0].Body != "Started investigation." {
		t.Errorf("comment body = %q", task.Comments[0].Body)
	}
}

func TestTaskCommentEmptyBody(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "commentvalspace"

	postTaskJSON(t, taskURL(base, space, ""), map[string]any{"title": "Task"}, "Mgr").Body.Close()

	resp := postTaskJSON(t, taskURL(base, space, "TASK-001/comment"), map[string]any{"body": "  "}, "DevAgent")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty comment, got %d", resp.StatusCode)
	}
}

func TestTaskSSEBroadcast(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "ssespace"

	// Subscribe to SSE before creating the task
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/spaces/"+space+"/events", nil)
	sseResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	defer sseResp.Body.Close()

	// Create a task — should trigger SSE task_updated event
	postTaskJSON(t, taskURL(base, space, ""), map[string]any{"title": "SSE test task"}, "Mgr").Body.Close()

	// Read SSE stream until we see task_updated or timeout
	buf := make([]byte, 4096)
	sseResp.Body.Read(buf)
	got := string(buf)
	if !strings.Contains(got, "task_updated") {
		t.Errorf("expected task_updated SSE event, got: %q", got)
	}
}

func TestTaskJournalReplay(t *testing.T) {
	dataDir := t.TempDir()
	space := "replayspace"

	// First server: create tasks and mutate them
	srv1 := NewServer(":0", dataDir)
	if err := srv1.Start(); err != nil {
		t.Fatalf("start srv1: %v", err)
	}
	base1 := serverBaseURL(srv1)

	postTaskJSON(t, taskURL(base1, space, ""), map[string]any{"title": "Replay task", "priority": "high"}, "Mgr").Body.Close()
	postTaskJSON(t, taskURL(base1, space, "TASK-001/move"), map[string]any{"status": "in_progress"}, "Mgr").Body.Close()
	postTaskJSON(t, taskURL(base1, space, "TASK-001/assign"), map[string]any{"assigned_to": "Worker"}, "Mgr").Body.Close()
	postTaskJSON(t, taskURL(base1, space, "TASK-001/comment"), map[string]any{"body": "Working on it."}, "Worker").Body.Close()
	srv1.Stop()

	// Second server: replay from journal
	srv2 := NewServer(":0", dataDir)
	if err := srv2.Start(); err != nil {
		t.Fatalf("start srv2: %v", err)
	}
	defer srv2.Stop()
	base2 := serverBaseURL(srv2)

	code, body := getBody(t, taskURL(base2, space, "TASK-001"))
	if code != http.StatusOK {
		t.Fatalf("expected 200 after replay, got %d; body: %s", code, body)
	}
	var task Task
	json.Unmarshal([]byte(body), &task)
	if task.Status != TaskStatusInProgress {
		t.Errorf("replayed status = %q, want in_progress", task.Status)
	}
	if task.AssignedTo != "Worker" {
		t.Errorf("replayed assigned_to = %q, want Worker", task.AssignedTo)
	}
	if len(task.Comments) != 1 {
		t.Errorf("replayed comments = %d, want 1", len(task.Comments))
	}
}

func TestTaskSequentialIDs(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "idspace"

	var ids []string
	for i := 0; i < 5; i++ {
		resp := postTaskJSON(t, taskURL(base, space, ""), map[string]any{"title": "Task"}, "Mgr")
		var task Task
		json.NewDecoder(resp.Body).Decode(&task)
		resp.Body.Close()
		ids = append(ids, task.ID)
	}

	expected := []string{"TASK-001", "TASK-002", "TASK-003", "TASK-004", "TASK-005"}
	for i, id := range ids {
		if id != expected[i] {
			t.Errorf("task[%d] ID = %q, want %q", i, id, expected[i])
		}
	}
}

func TestTaskUpdateNotFound(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	resp := putTaskJSON(t, taskURL(base, "myspace", "TASK-999"), map[string]any{"title": "Ghost"}, "Mgr")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestTaskDeleteNotFound(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	resp := deleteReq(t, taskURL(base, "myspace", "TASK-999"), "Mgr")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

// Phase 3 tests: TASK-NNN auto-link, ignition task queue injection

func TestTaskAutoLinkInMarkdown(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "autolink-test"

	// Create a task so TASK-001 exists
	resp := postTaskJSON(t, taskURL(base, space, ""), map[string]any{
		"title":  "Implement feature",
		"status": "in_progress",
	}, "Dev")
	var task Task
	json.NewDecoder(resp.Body).Decode(&task)
	resp.Body.Close()
	if task.ID != "TASK-001" {
		t.Fatalf("expected TASK-001, got %q", task.ID)
	}

	// Post an agent update referencing TASK-001 in items
	postJSON(t, base+"/spaces/"+space+"/agent/Dev", AgentUpdate{
		Status:  StatusActive,
		Summary: "Dev: working",
		Items:   []string{"Implementing TASK-001: add auto-link support", "Also see TASK-001 for context"},
	})

	// Read the rendered markdown
	_, body := getBody(t, base+"/spaces/"+space+"/raw")

	// TASK-001 should be rendered as a markdown link with its title
	if !strings.Contains(body, "[TASK-001:") {
		t.Error("rendered markdown should contain [TASK-001: ...] link syntax")
	}
	// The link should point to the task URL
	wantLink := "/spaces/" + space + "/tasks/TASK-001"
	if !strings.Contains(body, wantLink) {
		t.Errorf("rendered markdown should contain task link %q", wantLink)
	}
}

func TestTaskAutoLinkOnlyForExistingTasks(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "autolink-exist-test"

	// Create TASK-001 so the space has tasks, but reference TASK-999 which doesn't exist
	r := postTaskJSON(t, taskURL(base, space, ""), map[string]any{"title": "Real task", "status": "backlog"}, "Dev")
	r.Body.Close()

	// Post an agent update referencing a non-existent task ID
	postJSON(t, base+"/spaces/"+space+"/agent/Dev", AgentUpdate{
		Status:  StatusActive,
		Summary: "Dev: working",
		Items:   []string{"Referencing TASK-999 which does not exist"},
	})

	_, body := getBody(t, base+"/spaces/"+space+"/raw")

	// TASK-999 should NOT be turned into a link (task doesn't exist in this space)
	if strings.Contains(body, "[TASK-999](") {
		t.Error("rendered markdown should NOT auto-link TASK-999 when task does not exist in space")
	}
	// TASK-001 auto-link should not appear (was not referenced)
	if strings.Contains(body, "[TASK-001:") {
		t.Error("TASK-001 should not appear as an auto-link since it was not referenced in items")
	}
}

func TestIgnitionIncludesAssignedTasks(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "ignition-tasks-test"

	// Create tasks and assign one to "worker"
	resp1 := postTaskJSON(t, taskURL(base, space, ""), map[string]any{
		"title":       "Build the thing",
		"status":      "in_progress",
		"assigned_to": "worker",
	}, "Mgr")
	var task1 Task
	json.NewDecoder(resp1.Body).Decode(&task1)
	resp1.Body.Close()
	if task1.ID != "TASK-001" {
		t.Fatalf("expected TASK-001, got %q", task1.ID)
	}

	// Create a second task assigned to someone else
	resp2 := postTaskJSON(t, taskURL(base, space, ""), map[string]any{
		"title":       "Other task",
		"status":      "backlog",
		"assigned_to": "other-agent",
	}, "Mgr")
	resp2.Body.Close()

	// Ignite "worker" — should see their assigned task
	code, body := getBody(t, base+"/spaces/"+space+"/ignition/worker")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}

	if !strings.Contains(body, "TASK-001") {
		t.Error("ignition should include TASK-001 assigned to worker")
	}
	if !strings.Contains(body, "Build the thing") {
		t.Error("ignition should include the task title")
	}
	// Should NOT show other agent's task
	if strings.Contains(body, "Other task") {
		t.Error("ignition should NOT include tasks assigned to other agents")
	}
}

func TestIgnitionNoAssignedTasks(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "ignition-notasks-test"

	// Create a task assigned to someone else
	resp := postTaskJSON(t, taskURL(base, space, ""), map[string]any{
		"title":       "Unrelated task",
		"status":      "backlog",
		"assigned_to": "other",
	}, "Mgr")
	resp.Body.Close()

	// Ignite "worker" — no tasks assigned to them
	code, body := getBody(t, base+"/spaces/"+space+"/ignition/worker")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}

	// Should not crash or include wrong tasks
	if strings.Contains(body, "Unrelated task") {
		t.Error("ignition should NOT include tasks assigned to other agents")
	}
}

func TestIgnitionMentionsTasksEndpoint(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "ignition-tasks-endpoint-test"

	_, body := getBody(t, base+"/spaces/"+space+"/ignition/myagent")

	// Ignition should mention the /tasks endpoint for discoverability
	if !strings.Contains(body, "/tasks") {
		t.Error("ignition response should mention the /tasks endpoint")
	}
}

func TestTaskAutoLinkMultipleReferences(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "autolink-multi-test"

	// Create two tasks
	r1 := postTaskJSON(t, taskURL(base, space, ""), map[string]any{"title": "First task", "status": "backlog"}, "Dev")
	r1.Body.Close()
	r2 := postTaskJSON(t, taskURL(base, space, ""), map[string]any{"title": "Second task", "status": "in_progress"}, "Dev")
	r2.Body.Close()

	// Post update referencing both tasks
	postJSON(t, base+"/spaces/"+space+"/agent/Dev", AgentUpdate{
		Status:  StatusActive,
		Summary: "Dev: working",
		Items:   []string{"Completed TASK-001, now working on TASK-002"},
	})

	_, body := getBody(t, base+"/spaces/"+space+"/raw")

	link1 := "/spaces/" + space + "/tasks/TASK-001"
	link2 := "/spaces/" + space + "/tasks/TASK-002"
	if !strings.Contains(body, link1) {
		t.Errorf("markdown should auto-link TASK-001: missing %q", link1)
	}
	if !strings.Contains(body, link2) {
		t.Errorf("markdown should auto-link TASK-002: missing %q", link2)
	}
}

func TestTaskCreateNotifiesAssignee(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "notifycreatespace"

	resp := postTaskJSON(t, taskURL(base, space, ""), map[string]any{
		"title":       "Fix the bug",
		"assigned_to": "WorkerBot",
	}, "Boss")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	// WorkerBot should now have a message in their agent channel.
	code, body := getBody(t, base+"/spaces/"+space+"/agent/WorkerBot/messages")
	if code != http.StatusOK {
		t.Fatalf("expected 200 from messages endpoint, got %d", code)
	}
	if !strings.Contains(body, "TASK-001") {
		t.Errorf("expected TASK-001 in notification message, got: %s", body)
	}
	if !strings.Contains(body, "Fix the bug") {
		t.Errorf("expected task title in notification message, got: %s", body)
	}
}

func TestTaskAssignNotifiesNewAssignee(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "notifyassignspace"

	postTaskJSON(t, taskURL(base, space, ""), map[string]any{"title": "Important work"}, "Mgr").Body.Close()

	resp := postTaskJSON(t, taskURL(base, space, "TASK-001/assign"), map[string]any{"assigned_to": "DevAgent"}, "Mgr")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	code, body := getBody(t, base+"/spaces/"+space+"/agent/DevAgent/messages")
	if code != http.StatusOK {
		t.Fatalf("expected 200 from messages endpoint, got %d", code)
	}
	if !strings.Contains(body, "TASK-001") {
		t.Errorf("expected TASK-001 in notification, got: %s", body)
	}
}

func TestTaskUpdateNotifiesNewAssignee(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "notifyupdatespace"

	postTaskJSON(t, taskURL(base, space, ""), map[string]any{"title": "Reassigned work"}, "Mgr").Body.Close()

	newAssignee := "NewOwner"
	resp := putTaskJSON(t, taskURL(base, space, "TASK-001"), map[string]any{"assigned_to": newAssignee}, "Mgr")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	code, body := getBody(t, base+"/spaces/"+space+"/agent/NewOwner/messages")
	if code != http.StatusOK {
		t.Fatalf("expected 200 from messages endpoint, got %d", code)
	}
	if !strings.Contains(body, "TASK-001") {
		t.Errorf("expected TASK-001 in notification, got: %s", body)
	}
}

func TestTaskAssignNoNotifyWhenSameAssignee(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "notifysameassignspace"

	postTaskJSON(t, taskURL(base, space, ""), map[string]any{
		"title": "Already assigned", "assigned_to": "DevAgent",
	}, "Mgr").Body.Close()

	// Re-assign to the same agent — should NOT send duplicate notification.
	resp := postTaskJSON(t, taskURL(base, space, "TASK-001/assign"), map[string]any{"assigned_to": "DevAgent"}, "Mgr")
	defer resp.Body.Close()

	code, body := getBody(t, base+"/spaces/"+space+"/agent/DevAgent/messages")
	if code != http.StatusOK {
		t.Fatalf("expected 200 from messages endpoint, got %d", code)
	}

	// Only 1 message (from initial create), not 2.
	var result struct {
		Messages []AgentMessage `json:"messages"`
	}
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&result); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(result.Messages) != 1 {
		t.Errorf("expected 1 message (from create), got %d — re-assign to same agent should not duplicate", len(result.Messages))
	}
}

func TestTaskParentChildRegistration(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "parentchildspace"

	// Create parent task.
	parentResp := postTaskJSON(t, taskURL(base, space, ""), map[string]any{"title": "Parent Epic"}, "Mgr")
	defer parentResp.Body.Close()
	var parent Task
	json.NewDecoder(parentResp.Body).Decode(&parent)
	if parent.ID != "TASK-001" {
		t.Fatalf("expected parent TASK-001, got %s", parent.ID)
	}

	// Create subtask referencing parent.
	childResp := postTaskJSON(t, taskURL(base, space, ""), map[string]any{
		"title":       "Child subtask",
		"parent_task": "TASK-001",
	}, "Mgr")
	defer childResp.Body.Close()
	var child Task
	json.NewDecoder(childResp.Body).Decode(&child)
	if child.ParentTask != "TASK-001" {
		t.Errorf("child.ParentTask = %q, want TASK-001", child.ParentTask)
	}

	// Fetch parent and verify subtask is registered.
	code, body := getBody(t, base+"/spaces/"+space+"/tasks/TASK-001")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	var fetched Task
	json.NewDecoder(strings.NewReader(body)).Decode(&fetched)
	if len(fetched.Subtasks) != 1 || fetched.Subtasks[0] != "TASK-002" {
		t.Errorf("parent.Subtasks = %v, want [TASK-002]", fetched.Subtasks)
	}
}

func TestIgnitionAssignedTasksMultipleStatuses(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "ignition-inprogress-test"

	// Assign tasks with different statuses to "agent1"
	r1 := postTaskJSON(t, taskURL(base, space, ""), map[string]any{
		"title":       "In-progress work",
		"status":      "in_progress",
		"assigned_to": "agent1",
		"priority":    "high",
	}, "Mgr")
	r1.Body.Close()
	r2 := postTaskJSON(t, taskURL(base, space, ""), map[string]any{
		"title":       "Backlog item",
		"status":      "backlog",
		"assigned_to": "agent1",
	}, "Mgr")
	r2.Body.Close()

	code, body := getBody(t, base+"/spaces/"+space+"/ignition/agent1")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}

	if !strings.Contains(body, "In-progress work") {
		t.Error("ignition should show in_progress task assigned to agent1")
	}
	if !strings.Contains(body, "Backlog item") {
		t.Error("ignition should show backlog task assigned to agent1")
	}
}

func TestTaskSubtasksEndpoint(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "subtasksendpointspace"

	// Create parent task.
	parentResp := postTaskJSON(t, taskURL(base, space, ""), map[string]any{"title": "Parent Epic"}, "Mgr")
	defer parentResp.Body.Close()
	if parentResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", parentResp.StatusCode)
	}

	// Create subtask via /tasks/TASK-001/subtasks.
	childResp := postTaskJSON(t, taskURL(base, space, "TASK-001/subtasks"), map[string]any{
		"title": "Child subtask",
	}, "Mgr")
	defer childResp.Body.Close()
	if childResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 for subtask creation, got %d", childResp.StatusCode)
	}
	var child Task
	if err := json.NewDecoder(childResp.Body).Decode(&child); err != nil {
		t.Fatalf("decode child: %v", err)
	}
	if child.ParentTask != "TASK-001" {
		t.Errorf("child.ParentTask = %q, want TASK-001", child.ParentTask)
	}
	if child.ID != "TASK-002" {
		t.Errorf("child.ID = %q, want TASK-002", child.ID)
	}

	// Parent should now list TASK-002 as a subtask.
	code, body := getBody(t, base+"/spaces/"+space+"/tasks/TASK-001")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	var parent Task
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&parent); err != nil {
		t.Fatalf("decode parent: %v", err)
	}
	if len(parent.Subtasks) != 1 || parent.Subtasks[0] != "TASK-002" {
		t.Errorf("parent.Subtasks = %v, want [TASK-002]", parent.Subtasks)
	}
}

func TestTaskSubtasksEndpointNotFound(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "subtasksnotfoundspace"

	// Attempt to create subtask of nonexistent parent.
	resp := postTaskJSON(t, taskURL(base, space, "TASK-999/subtasks"), map[string]any{
		"title": "Orphan",
	}, "Mgr")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for missing parent, got %d", resp.StatusCode)
	}
}

// TestNotificationOnMessage verifies that sending a message creates a notification.
func TestNotificationOnMessage(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "notif-msg-test"

	// Register recipient agent.
	postJSON(t, base+"/spaces/"+space+"/agent/Bot", AgentUpdate{
		Status:  StatusActive,
		Summary: "Bot: ready",
	})

	// Send a message from Sender to Bot.
	req, _ := http.NewRequest(http.MethodPost, base+"/spaces/"+space+"/agent/Bot/message",
		strings.NewReader(`{"message":"hello from sender"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Name", "Sender")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("send message: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	// Fetch Bot's agent state and verify notification was created.
	code, body := getBody(t, base+"/spaces/"+space+"/agent/Bot")
	if code != http.StatusOK {
		t.Fatalf("GET agent: expected 200, got %d", code)
	}
	var ag AgentUpdate
	if err := json.Unmarshal([]byte(body), &ag); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(ag.Notifications) == 0 {
		t.Fatal("expected at least one notification, got none")
	}
	n := ag.Notifications[len(ag.Notifications)-1]
	if n.Type != NotifTypeMessage {
		t.Errorf("notification type = %q, want %q", n.Type, NotifTypeMessage)
	}
	if n.From != "Sender" {
		t.Errorf("notification from = %q, want %q", n.From, "Sender")
	}
	if n.Read {
		t.Error("notification should be unread after message send")
	}
}

// TestNotificationMarkedReadOnStatusPost verifies that notifications are marked read
// when the agent posts a status update.
func TestNotificationMarkedReadOnStatusPost(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "notif-read-test"

	// Register recipient.
	postJSON(t, base+"/spaces/"+space+"/agent/Worker", AgentUpdate{
		Status:  StatusActive,
		Summary: "Worker: ready",
	})

	// Send message to create notification.
	req, _ := http.NewRequest(http.MethodPost, base+"/spaces/"+space+"/agent/Worker/message",
		strings.NewReader(`{"message":"do the thing"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Name", "Boss")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	// Worker posts a status update (simulates check-in after nudge).
	postJSON(t, base+"/spaces/"+space+"/agent/Worker", AgentUpdate{
		Status:  StatusActive,
		Summary: "Worker: working on the thing",
	})

	// Verify notification is now marked read.
	code, body := getBody(t, base+"/spaces/"+space+"/agent/Worker")
	if code != http.StatusOK {
		t.Fatalf("GET agent: expected 200, got %d", code)
	}
	var ag AgentUpdate
	if err := json.Unmarshal([]byte(body), &ag); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(ag.Notifications) == 0 {
		t.Fatal("expected notifications to be preserved after status post")
	}
	for _, n := range ag.Notifications {
		if !n.Read {
			t.Errorf("notification %q should be marked read after agent status post", n.ID)
		}
	}
}

// TestNotificationInRaw verifies that unread notifications appear at the top of the /raw section.
func TestNotificationInRaw(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "notif-raw-test"

	// Register agent.
	postJSON(t, base+"/spaces/"+space+"/agent/Alpha", AgentUpdate{
		Status:  StatusActive,
		Summary: "Alpha: ready",
	})

	// Send message to trigger notification.
	req, _ := http.NewRequest(http.MethodPost, base+"/spaces/"+space+"/agent/Alpha/message",
		strings.NewReader(`{"message":"urgent task for you"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Name", "Mgr")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	// Read /raw and verify notifications section appears before messages section within the agent's subsection.
	code, body := getBody(t, base+"/spaces/"+space+"/raw")
	if code != http.StatusOK {
		t.Fatalf("GET /raw: expected 200, got %d", code)
	}
	// Find the agent's subsection by looking after "### Alpha"
	agentSectionIdx := strings.Index(body, "### Alpha")
	if agentSectionIdx < 0 {
		t.Fatal("expected '### Alpha' section in /raw output")
	}
	agentSection := body[agentSectionIdx:]
	notifIdx := strings.Index(agentSection, "#### Notifications")
	msgIdx := strings.Index(agentSection, "#### Messages")
	if notifIdx < 0 {
		t.Error("expected '#### Notifications' section in agent's /raw section")
	}
	if msgIdx < 0 {
		t.Error("expected '#### Messages' section in agent's /raw section")
	}
	if notifIdx >= 0 && msgIdx >= 0 && notifIdx > msgIdx {
		t.Error("Notifications section should appear before Messages section in /raw agent section")
	}
}

// TestNotificationInIgnition verifies unread notifications are surfaced in the ignition response.
func TestNotificationInIgnition(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "notif-ignition-test"

	// Register agent and create a notification via message.
	postJSON(t, base+"/spaces/"+space+"/agent/Gamma", AgentUpdate{
		Status:  StatusIdle,
		Summary: "Gamma: waiting",
	})
	req, _ := http.NewRequest(http.MethodPost, base+"/spaces/"+space+"/agent/Gamma/message",
		strings.NewReader(`{"message":"wake up and do TASK-042"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Name", "Boss")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	// Fetch ignition — should include pending notifications.
	code, body := getBody(t, base+"/spaces/"+space+"/ignition/Gamma")
	if code != http.StatusOK {
		t.Fatalf("GET ignition: expected 200, got %d", code)
	}
	if !strings.Contains(body, "Pending Notifications") {
		t.Error("ignition response should contain 'Pending Notifications' section")
	}
	if !strings.Contains(body, "New message from Boss") {
		t.Error("ignition response should contain notification title 'New message from Boss'")
	}
}

// TestNotificationTaskAssign verifies that task assignment creates a typed notification.
func TestNotificationTaskAssign(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "notif-task-assign-test"

	// Register the assignee agent.
	postJSON(t, base+"/spaces/"+space+"/agent/DevAgent", AgentUpdate{
		Status:  StatusIdle,
		Summary: "DevAgent: ready",
	})

	// Create a task assigned to DevAgent.
	resp := postTaskJSON(t, taskURL(base, space, ""), map[string]any{
		"title":       "Implement feature X",
		"assigned_to": "DevAgent",
		"status":      "in_progress",
	}, "Boss")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create task: expected 201, got %d: %s", resp.StatusCode, body)
	}

	// Small delay for async notification delivery.
	time.Sleep(50 * time.Millisecond)

	// Verify DevAgent received a task_assigned notification.
	code, body := getBody(t, base+"/spaces/"+space+"/agent/DevAgent")
	if code != http.StatusOK {
		t.Fatalf("GET agent: expected 200, got %d", code)
	}
	var ag AgentUpdate
	if err := json.Unmarshal([]byte(body), &ag); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var found bool
	for _, n := range ag.Notifications {
		if n.Type == NotifTypeTaskAssign {
			found = true
			if n.TaskID == "" {
				t.Error("task_assigned notification should have task_id set")
			}
			if n.From == "" {
				t.Error("task_assigned notification should have from set")
			}
		}
	}
	if !found {
		t.Errorf("expected task_assigned notification in %+v", ag.Notifications)
	}
}

// TestNotificationPruning verifies that notifications are pruned to 20 max.
func TestNotificationPruning(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "notif-prune-test"

	// Register agent.
	postJSON(t, base+"/spaces/"+space+"/agent/Pruned", AgentUpdate{
		Status:  StatusActive,
		Summary: "Pruned: ready",
	})

	// Send 25 messages to create 25 notifications (exceeds 20 limit).
	for i := 0; i < 25; i++ {
		req, _ := http.NewRequest(http.MethodPost, base+"/spaces/"+space+"/agent/Pruned/message",
			strings.NewReader(`{"message":"msg"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Agent-Name", "Spammer")
		resp, _ := http.DefaultClient.Do(req)
		resp.Body.Close()
	}

	// Fetch agent and verify notification count is at most 20.
	code, body := getBody(t, base+"/spaces/"+space+"/agent/Pruned")
	if code != http.StatusOK {
		t.Fatalf("GET agent: expected 200, got %d", code)
	}
	var ag AgentUpdate
	if err := json.Unmarshal([]byte(body), &ag); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(ag.Notifications) > 20 {
		t.Errorf("expected at most 20 notifications after pruning, got %d", len(ag.Notifications))
	}
}

// TestSPAFallback verifies that deep-linked frontend paths (e.g. /SpaceName,
// /SpaceName/kanban) are handled by handleRoot instead of returning Go's
// default "404 page not found" response. This allows Vue Router to handle
// client-side navigation when the user navigates directly to a URL.
//
// In tests there is no compiled frontend, so all these paths return a 404
// with "frontend not available" — but that's still correct: they all go
// through handleRoot rather than getting the raw http.NotFound response.
func TestSPAFallback(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	// Fetch what the root path returns — this is our baseline SPA response.
	_, rootBody := getBody(t, base+"/")

	spaPaths := []string{
		"/AgentBossDevTeam",
		"/AgentBossDevTeam/kanban",
		"/some-space/agent/foo",
		"/unknown-deep-path",
	}
	for _, path := range spaPaths {
		_, body := getBody(t, base+path)
		// Deep-linked paths must return the same body as "/" — either the SPA
		// index.html (in production) or "frontend not available" (in tests).
		// They must NOT return Go's default "404 page not found\n".
		if body != rootBody {
			t.Errorf("GET %s: expected same body as /, got: %.100s", path, body)
		}
	}
}


// TestIgnitionCollaborationNorms verifies that the ignition response includes
// the Collaboration Norms and Work Loop sections (TASK-066).
func TestIgnitionCollaborationNorms(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	code, body := getBody(t, base+"/spaces/collab-norm-test/ignition/agent1?session_id=sess1")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}

	checks := []string{
		"## Collaboration Norms",
		"**Communication**",
		"**Team Formation**",
		"**Task Discipline**",
		"**Hierarchy & Escalation**",
		"## Work Loop",
		"Read messages:",
		"POST status update",
	}
	for _, want := range checks {
		if !strings.Contains(body, want) {
			t.Errorf("ignition missing %q", want)
		}
	}
}

// TestSpawnInitialMessage verifies that spawn with initial_message queues
// the message in the agent's inbox (TASK-068).
func TestSpawnInitialMessage(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	// Create agent first so we have an entry to spawn into (no real tmux in tests).
	// We test the message delivery path via deliverInternalMessage directly.
	space := "spawn-msg-test"
	agentName := "bot1"

	// Deliver an internal message (simulates what spawn does with initial_message).
	srv.deliverInternalMessage(space, agentName, "manager", "Start working on TASK-100")

	// Fetch messages to verify delivery.
	code, body := getBody(t, base+"/spaces/"+space+"/agent/"+agentName+"/messages")
	if code != http.StatusOK {
		t.Fatalf("GET messages: expected 200, got %d", code)
	}
	if !strings.Contains(body, "Start working on TASK-100") {
		t.Errorf("initial_message not found in agent messages: %s", body)
	}
	if !strings.Contains(body, "manager") {
		t.Errorf("sender not found in agent messages: %s", body)
	}
}

// TestStaleTaskDetection verifies that is_stale is computed correctly (TASK-070).
func TestStaleTaskDetection(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)

	space := "stale-task-test"

	// Create an in_progress task with a recent update — should NOT be stale.
	createResp := postJSON(t, base+"/spaces/"+space+"/tasks", map[string]any{
		"title":       "Active task",
		"status":      "in_progress",
		"assigned_to": "worker",
	})
	createResp.Body.Close()

	code, body := getBody(t, base+"/spaces/"+space+"/tasks")
	if code != http.StatusOK {
		t.Fatalf("list tasks: expected 200, got %d", code)
	}
	if strings.Contains(body, `"is_stale":true`) {
		t.Error("recently updated in_progress task should not be stale")
	}

	// Manually inject a stale task (updated >1h ago) using the server internals.
	srv.mu.Lock()
	ks := srv.getOrCreateSpaceLocked(space)
	staleTime := time.Now().UTC().Add(-2 * time.Hour)
	ks.Tasks["TASK-STALE"] = &Task{
		ID:        "TASK-STALE",
		Space:     space,
		Title:     "Stale task",
		Status:    TaskStatusInProgress,
		UpdatedAt: staleTime,
		CreatedAt: staleTime,
	}
	srv.mu.Unlock()

	// GET the stale task — should have is_stale:true.
	code, body = getBody(t, base+"/spaces/"+space+"/tasks/TASK-STALE")
	if code != http.StatusOK {
		t.Fatalf("get stale task: expected 200, got %d", code)
	}
	if !strings.Contains(body, `"is_stale":true`) {
		t.Errorf("task with >1h old in_progress update should be stale; body: %s", body)
	}

	// Also verify it appears in list results.
	code, body = getBody(t, base+"/spaces/"+space+"/tasks")
	if code != http.StatusOK {
		t.Fatalf("list tasks: expected 200, got %d", code)
	}
	if !strings.Contains(body, `"is_stale":true`) {
		t.Errorf("stale task should appear with is_stale:true in list results")
	}

	// Verify done tasks are never stale (even if old).
	srv.mu.Lock()
	doneTime := time.Now().UTC().Add(-3 * time.Hour)
	ks.Tasks["TASK-DONE-OLD"] = &Task{
		ID:        "TASK-DONE-OLD",
		Space:     space,
		Title:     "Old done task",
		Status:    TaskStatusDone,
		UpdatedAt: doneTime,
		CreatedAt: doneTime,
	}
	srv.mu.Unlock()

	code, body = getBody(t, base+"/spaces/"+space+"/tasks/TASK-DONE-OLD")
	if code != http.StatusOK {
		t.Fatalf("get done task: expected 200, got %d", code)
	}
	if strings.Contains(body, `"is_stale":true`) {
		t.Error("done task should never be stale regardless of age")
	}
}

// TestSkipPermissionsDefaultOff verifies that the global allowSkipPermissions flag
// defaults to false and that setting BOSS_ALLOW_SKIP_PERMISSIONS=true enables it.
func TestSkipPermissionsDefaultOff(t *testing.T) {
	// Default: flag must be off.
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	if srv.allowSkipPermissions {
		t.Error("allowSkipPermissions should be false by default")
	}
}

func TestSkipPermissionsEnvVar(t *testing.T) {
	t.Setenv("BOSS_ALLOW_SKIP_PERMISSIONS", "true")
	dataDir := t.TempDir()
	srv := NewServer(":0", dataDir)
	if !srv.allowSkipPermissions {
		t.Error("allowSkipPermissions should be true when BOSS_ALLOW_SKIP_PERMISSIONS=true")
	}
}

// TestTaskAssignNudgesAgent verifies that assigning a task schedules a tmux nudge
// for the assigned agent so they are prompted to check in. GH-81 regression test.
func TestTaskAssignNudgesAgent(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "nudgetaskspace"

	// Create a task and assign it to an agent.
	postTaskJSON(t, taskURL(base, space, ""), map[string]any{
		"title": "Work item", "assigned_to": "Worker",
	}, "Boss").Body.Close()

	// Nudge should be queued for Worker. resolveAgentName preserves input case for
	// unregistered agents, so check case-insensitively.
	srv.nudgeMu.Lock()
	var found bool
	for k := range srv.nudgePending {
		if strings.EqualFold(k, space+"/Worker") {
			found = true
			break
		}
	}
	srv.nudgeMu.Unlock()
	if !found {
		t.Error("expected nudge to be queued for Worker after task assignment, but nudgePending was empty")
	}
}
