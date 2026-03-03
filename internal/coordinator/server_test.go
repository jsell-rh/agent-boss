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
	return strings.TrimRight(parts[1], "/")
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
		Status:  StatusBlocked,
		Summary: "Waiting for API schema",
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
	agent, ok := ks.Agents["Api"]
	if !ok {
		t.Fatal("agent 'Api' not found")
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
	if intr.Agent != "Fe" {
		t.Errorf("agent = %q, want Fe", intr.Agent)
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
	if metrics.ByAgent["Api"] != 2 {
		t.Errorf("by_agent[Api] = %d, want 2", metrics.ByAgent["Api"])
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
	if metrics.ByAgent["Fe"] != 2 {
		t.Errorf("by_agent[Fe] = %d, want 2", metrics.ByAgent["Fe"])
	}
	if metrics.ByAgent["Cp"] != 1 {
		t.Errorf("by_agent[Cp] = %d, want 1", metrics.ByAgent["Cp"])
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
