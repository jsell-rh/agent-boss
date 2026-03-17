package coordinator

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// ambientAPIMock simulates the ACP backend API with realistic response shapes.
// It stores sessions in-memory and validates request bodies the same way the
// real backend does (rejects "environmentVariables", rejects "://" in env values).
type ambientAPIMock struct {
	mu       sync.Mutex
	sessions map[string]backendSessionCR
	calls    []apiCall
	nextID   int

	// sessionsPath is the expected URL prefix, e.g. "/api/projects/test-project/agentic-sessions"
	sessionsPath string
}

type apiCall struct {
	Method string
	Path   string
}

func newAmbientAPIMock(project string) *ambientAPIMock {
	return &ambientAPIMock{
		sessions:     make(map[string]backendSessionCR),
		sessionsPath: "/api/projects/" + project + "/agentic-sessions",
	}
}

func (m *ambientAPIMock) record(method, path string) {
	m.calls = append(m.calls, apiCall{Method: method, Path: path})
}

func (m *ambientAPIMock) findCalls(method, pathContains string) []apiCall {
	var result []apiCall
	for _, c := range m.calls {
		if c.Method == method && strings.Contains(c.Path, pathContains) {
			result = append(result, c)
		}
	}
	return result
}

func (m *ambientAPIMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.record(r.Method, r.URL.Path)

	path := r.URL.Path

	// POST /agentic-sessions — create
	if path == m.sessionsPath && r.Method == http.MethodPost {
		m.handleCreate(w, r)
		return
	}

	// GET /agentic-sessions — list
	if path == m.sessionsPath && r.Method == http.MethodGet {
		m.handleList(w)
		return
	}

	// Routes with session ID: /agentic-sessions/{id}[/...]
	if strings.HasPrefix(path, m.sessionsPath+"/") {
		rest := strings.TrimPrefix(path, m.sessionsPath+"/")
		parts := strings.SplitN(rest, "/", 2)
		sessionID := parts[0]
		suffix := ""
		if len(parts) > 1 {
			suffix = parts[1]
		}

		switch {
		case suffix == "" && r.Method == http.MethodGet:
			m.handleGet(w, sessionID)
		case suffix == "" && r.Method == http.MethodDelete:
			m.handleDelete(w, sessionID)
		case suffix == "export" && r.Method == http.MethodGet:
			m.handleExport(w, sessionID)
		case suffix == "agui/run" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusAccepted)
		case suffix == "agui/interrupt" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
		return
	}

	http.NotFound(w, r)
}

func (m *ambientAPIMock) handleCreate(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}

	// Reject "environmentVariables" — real backend only accepts "envVars".
	if _, has := body["environmentVariables"]; has {
		http.Error(w, `{"error":"unknown field: environmentVariables"}`, http.StatusBadRequest)
		return
	}

	// Reject env var values containing "://" — real backend validation.
	if envVars, ok := body["envVars"].(map[string]interface{}); ok {
		for k, v := range envVars {
			if vs, ok := v.(string); ok && strings.Contains(vs, "://") {
				http.Error(w, fmt.Sprintf(`{"error":"envVars.%s: value contains disallowed '://'"}`, k), http.StatusBadRequest)
				return
			}
		}
	}

	m.nextID++
	name := fmt.Sprintf("session-%03d", m.nextID)

	cr := backendSessionCR{}
	cr.Metadata.Name = name
	cr.Status.Phase = "Pending"
	if dn, ok := body["displayName"].(string); ok {
		cr.Spec.DisplayName = dn
	}
	if labels, ok := body["labels"].(map[string]interface{}); ok {
		cr.Metadata.Labels = make(map[string]string)
		for k, v := range labels {
			if vs, ok := v.(string); ok {
				cr.Metadata.Labels[k] = vs
			}
		}
	}

	m.sessions[name] = cr

	// Real backend returns {"name":"...", "uid":"..."} — not CR metadata.
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"name": name, "uid": "uid-" + name})
}

func (m *ambientAPIMock) handleList(w http.ResponseWriter) {
	var items []backendSessionCR
	for _, cr := range m.sessions {
		items = append(items, cr)
	}
	// Real backend wraps in {"items": [...]}, NOT a bare array.
	json.NewEncoder(w).Encode(backendSessionList{Items: items})
}

func (m *ambientAPIMock) handleGet(w http.ResponseWriter, id string) {
	cr, ok := m.sessions[id]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	// Return full CR shape.
	json.NewEncoder(w).Encode(cr)
}

func (m *ambientAPIMock) handleDelete(w http.ResponseWriter, id string) {
	delete(m.sessions, id)
	w.WriteHeader(http.StatusNoContent)
}

func (m *ambientAPIMock) handleExport(w http.ResponseWriter, id string) {
	if _, ok := m.sessions[id]; !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	// Real backend: legacyMessages is always absent; content lives in aguiEvents only.
	snapshot, _ := json.Marshal(map[string]interface{}{
		"type": "MESSAGES_SNAPSHOT",
		"messages": []map[string]string{
			{"role": "user", "content": "hello from test"},
			{"role": "assistant", "content": "response from agent"},
		},
	})
	json.NewEncoder(w).Encode(ambientExportResponse{
		AguiEvents: []json.RawMessage{snapshot},
	})
}

// setPhase updates a session's phase in the mock store.
func (m *ambientAPIMock) setPhase(id, phase string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cr, ok := m.sessions[id]; ok {
		cr.Status.Phase = phase
		m.sessions[id] = cr
	}
}

// mustStartAmbientServer creates a Server with an AmbientSessionBackend wired
// to the given mock, sets it as the default backend, and returns the server + base URL.
func mustStartAmbientServer(t *testing.T, mock *ambientAPIMock) (*Server, string) {
	t.Helper()
	srv, cleanup := mustStartServer(t)
	t.Cleanup(cleanup)

	mockTS := httptest.NewServer(mock)
	t.Cleanup(mockTS.Close)

	srv.backends["ambient"] = NewAmbientSessionBackend(AmbientBackendConfig{
		APIURL:                 mockTS.URL,
		Token:                  "test-token",
		Project:                "test-project",
		CoordinatorExternalURL: "https://boss.example.com",
	})
	srv.defaultBackend = "ambient"

	return srv, serverBaseURL(srv)
}

// --- Group 1: Spawn ---

func TestHandlerAmbientSpawn(t *testing.T) {
	mock := newAmbientAPIMock("test-project")
	srv, base := mustStartAmbientServer(t, mock)
	_ = srv

	resp := postJSON(t, base+"/spaces/e2e/agent/worker1/spawn", map[string]any{
		"backend": "ambient",
	})
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", resp.StatusCode, body)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	sessionID, ok := result["session_id"].(string)
	if !ok || sessionID == "" {
		t.Fatalf("expected non-empty session_id, got %v", result["session_id"])
	}
	if !strings.HasPrefix(sessionID, "session-") {
		t.Errorf("session_id %q doesn't match mock naming pattern", sessionID)
	}

	// Verify mock received correct labels and fields.
	mock.mu.Lock()
	defer mock.mu.Unlock()
	creates := mock.findCalls(http.MethodPost, "agentic-sessions")
	if len(creates) != 1 {
		t.Fatalf("expected 1 create call, got %d", len(creates))
	}
	cr, ok := mock.sessions[sessionID]
	if !ok {
		t.Fatalf("session %q not found in mock", sessionID)
	}
	if cr.Metadata.Labels["managed-by"] != "agent-boss" {
		t.Errorf("expected managed-by=agent-boss, got %q", cr.Metadata.Labels["managed-by"])
	}
	if cr.Metadata.Labels["boss-agent"] != "worker1" {
		t.Errorf("expected boss-agent=worker1, got %q", cr.Metadata.Labels["boss-agent"])
	}
	if cr.Metadata.Labels["boss-space"] != "e2e" {
		t.Errorf("expected boss-space=e2e, got %q", cr.Metadata.Labels["boss-space"])
	}
	if cr.Spec.DisplayName != "worker1" {
		t.Errorf("expected displayName=worker1, got %q", cr.Spec.DisplayName)
	}
}

func TestHandlerAmbientSpawnEnvVarSplit(t *testing.T) {
	// Intercept the raw request body to verify env var splitting.
	var capturedBody map[string]interface{}
	var mu sync.Mutex

	project := "test-project"
	sessionsPath := "/api/projects/" + project + "/agentic-sessions"

	mux := http.NewServeMux()
	mux.HandleFunc(sessionsPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			mu.Lock()
			json.NewDecoder(r.Body).Decode(&capturedBody)
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{"name": "sess-env-test"})
			return
		}
		// GET for list (used by Available() check)
		json.NewEncoder(w).Encode(backendSessionList{})
	})
	mux.HandleFunc(sessionsPath+"/", func(w http.ResponseWriter, r *http.Request) {
		// GET for individual session (SessionExists check)
		json.NewEncoder(w).Encode(backendCR("sess-env-test", "Running", "", nil))
	})

	mockTS := httptest.NewServer(mux)
	defer mockTS.Close()

	srv, cleanup := mustStartServer(t)
	defer cleanup()
	srv.backends["ambient"] = NewAmbientSessionBackend(AmbientBackendConfig{
		APIURL:                 mockTS.URL,
		Token:                  "test-token",
		Project:                project,
		CoordinatorExternalURL: "https://boss.example.com",
	})
	srv.defaultBackend = "ambient"
	base := serverBaseURL(srv)

	resp := postJSON(t, base+"/spaces/envtest/agent/splitter/spawn", map[string]any{
		"backend": "ambient",
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}

	mu.Lock()
	defer mu.Unlock()
	envVars, ok := capturedBody["envVars"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected envVars in request body, got %v", capturedBody["envVars"])
	}

	// BOSS_URL should have been split into BOSS_URL_SCHEME + BOSS_URL_HOST
	if _, has := envVars["BOSS_URL"]; has {
		t.Error("BOSS_URL should not be present (contains ://, must be split)")
	}
	if envVars["BOSS_URL_SCHEME"] != "https" {
		t.Errorf("expected BOSS_URL_SCHEME=https, got %v", envVars["BOSS_URL_SCHEME"])
	}
	if envVars["BOSS_URL_HOST"] != "boss.example.com" {
		t.Errorf("expected BOSS_URL_HOST=boss.example.com, got %v", envVars["BOSS_URL_HOST"])
	}
}

func TestHandlerAmbientSpawnLabelValidation(t *testing.T) {
	mock := newAmbientAPIMock("test-project")
	_, base := mustStartAmbientServer(t, mock)

	resp := postJSON(t, base+"/spaces/has spaces/agent/worker1/spawn", map[string]any{
		"backend": "ambient",
	})
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	// Should fail because "has spaces" is not a valid K8s label value.
	if resp.StatusCode == http.StatusAccepted {
		t.Fatal("expected error for space name with spaces, got 202")
	}
	if !strings.Contains(string(body), "not a valid Kubernetes label") {
		t.Errorf("expected label validation error, got: %s", body)
	}

	// Verify no create call was made to the mock.
	mock.mu.Lock()
	defer mock.mu.Unlock()
	creates := mock.findCalls(http.MethodPost, "agentic-sessions")
	if len(creates) != 0 {
		t.Errorf("expected 0 create calls (validation should prevent API call), got %d", len(creates))
	}
}

// --- Group 2: Stop / Restart / Interrupt ---

func TestHandlerAmbientStop(t *testing.T) {
	mock := newAmbientAPIMock("test-project")
	srv, base := mustStartAmbientServer(t, mock)

	// Spawn an agent first.
	resp := postJSON(t, base+"/spaces/e2e/agent/stopper/spawn", map[string]any{"backend": "ambient"})
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("spawn: expected 202, got %d: %s", resp.StatusCode, respBody)
	}
	var spawnResult map[string]interface{}
	json.Unmarshal(respBody, &spawnResult)
	sessionID := spawnResult["session_id"].(string)

	// Set session to Running so SessionExists returns true.
	mock.setPhase(sessionID, "Running")

	// Stop the agent.
	req, _ := http.NewRequest(http.MethodPost, base+"/spaces/e2e/agent/stopper/stop", nil)
	req.Header.Set("X-Agent-Name", "stopper")
	stopResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST stop: %v", err)
	}
	defer stopResp.Body.Close()
	stopBody, _ := io.ReadAll(stopResp.Body)

	if stopResp.StatusCode != http.StatusOK {
		t.Fatalf("stop: expected 200, got %d: %s", stopResp.StatusCode, stopBody)
	}

	// Verify agent status is done and session_id is cleared.
	srv.mu.RLock()
	ks := srv.spaces["e2e"]
	agent := ks.agentStatus("stopper")
	srv.mu.RUnlock()
	if agent.Status != StatusDone {
		t.Errorf("expected agent status=done, got %q", agent.Status)
	}
	if agent.SessionID != "" {
		t.Errorf("expected session_id cleared, got %q", agent.SessionID)
	}

	// Verify mock received DELETE.
	mock.mu.Lock()
	deletes := mock.findCalls(http.MethodDelete, sessionID)
	mock.mu.Unlock()
	if len(deletes) != 1 {
		t.Errorf("expected 1 DELETE call, got %d", len(deletes))
	}
}

func TestHandlerAmbientRestart(t *testing.T) {
	mock := newAmbientAPIMock("test-project")
	_, base := mustStartAmbientServer(t, mock)

	// Spawn agent (gets session-A).
	resp := postJSON(t, base+"/spaces/e2e/agent/restarter/spawn", map[string]any{"backend": "ambient"})
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("spawn: expected 202, got %d: %s", resp.StatusCode, body)
	}
	var spawnResult map[string]interface{}
	json.Unmarshal(body, &spawnResult)
	sessionA := spawnResult["session_id"].(string)

	// Set session to Running.
	mock.setPhase(sessionA, "Running")

	// Restart the agent.
	resp2 := postJSON(t, base+"/spaces/e2e/agent/restarter/restart", map[string]any{})
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(resp2.Body)

	if resp2.StatusCode != http.StatusAccepted {
		t.Fatalf("restart: expected 202, got %d: %s", resp2.StatusCode, body2)
	}

	var restartResult map[string]interface{}
	json.Unmarshal(body2, &restartResult)
	sessionB, ok := restartResult["session_id"].(string)
	if !ok || sessionB == "" {
		t.Fatalf("expected non-empty session_id in restart response, got %v", restartResult["session_id"])
	}
	if sessionB == sessionA {
		t.Errorf("expected new session_id, got same as original: %q", sessionB)
	}

	// Verify mock received DELETE for session-A, then POST for new session.
	mock.mu.Lock()
	defer mock.mu.Unlock()
	deletes := mock.findCalls(http.MethodDelete, sessionA)
	if len(deletes) != 1 {
		t.Errorf("expected 1 DELETE for %s, got %d", sessionA, len(deletes))
	}
	creates := mock.findCalls(http.MethodPost, "agentic-sessions")
	if len(creates) < 2 {
		t.Errorf("expected at least 2 POST calls (spawn + restart), got %d", len(creates))
	}
}

func TestHandlerAmbientInterrupt(t *testing.T) {
	mock := newAmbientAPIMock("test-project")
	_, base := mustStartAmbientServer(t, mock)

	// Spawn agent.
	resp := postJSON(t, base+"/spaces/e2e/agent/interruptee/spawn", map[string]any{"backend": "ambient"})
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("spawn: expected 202, got %d: %s", resp.StatusCode, body)
	}
	var spawnResult map[string]interface{}
	json.Unmarshal(body, &spawnResult)
	sessionID := spawnResult["session_id"].(string)

	// Set session to Running.
	mock.setPhase(sessionID, "Running")

	// Interrupt the agent.
	req, _ := http.NewRequest(http.MethodPost, base+"/spaces/e2e/agent/interruptee/interrupt", nil)
	req.Header.Set("X-Agent-Name", "interruptee")
	intResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST interrupt: %v", err)
	}
	defer intResp.Body.Close()

	if intResp.StatusCode != http.StatusOK {
		intBody, _ := io.ReadAll(intResp.Body)
		t.Fatalf("interrupt: expected 200, got %d: %s", intResp.StatusCode, intBody)
	}

	// Verify mock received POST to /agui/interrupt.
	mock.mu.Lock()
	defer mock.mu.Unlock()
	interrupts := mock.findCalls(http.MethodPost, "agui/interrupt")
	if len(interrupts) != 1 {
		t.Errorf("expected 1 interrupt call, got %d", len(interrupts))
	}
}

// --- Group 3: Status and Observability ---

func TestHandlerAmbientSessionStatus(t *testing.T) {
	mock := newAmbientAPIMock("test-project")
	_, base := mustStartAmbientServer(t, mock)

	// Spawn an agent.
	resp := postJSON(t, base+"/spaces/e2e/agent/observer/spawn", map[string]any{"backend": "ambient"})
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("spawn: expected 202, got %d: %s", resp.StatusCode, body)
	}
	var spawnResult map[string]interface{}
	json.Unmarshal(body, &spawnResult)
	sessionID := spawnResult["session_id"].(string)

	// Set session to Running.
	mock.setPhase(sessionID, "Running")

	// GET session-status.
	code, statusBody := getBody(t, base+"/spaces/e2e/api/session-status")
	if code != http.StatusOK {
		t.Fatalf("session-status: expected 200, got %d: %s", code, statusBody)
	}

	var statuses []agentSessionStatus
	if err := json.Unmarshal([]byte(statusBody), &statuses); err != nil {
		t.Fatalf("unmarshal session-status: %v", err)
	}

	if len(statuses) == 0 {
		t.Fatal("expected at least 1 agent in session-status response")
	}

	found := false
	for _, s := range statuses {
		if s.Agent == "observer" {
			found = true
			if !s.Registered {
				t.Error("expected registered=true")
			}
			if !s.Exists {
				t.Error("expected exists=true")
			}
			break
		}
	}
	if !found {
		t.Errorf("agent 'observer' not found in session-status: %+v", statuses)
	}
}

func TestHandlerAmbientIntrospect(t *testing.T) {
	mock := newAmbientAPIMock("test-project")
	_, base := mustStartAmbientServer(t, mock)

	// Spawn an agent.
	resp := postJSON(t, base+"/spaces/e2e/agent/inspector/spawn", map[string]any{"backend": "ambient"})
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("spawn: expected 202, got %d: %s", resp.StatusCode, body)
	}
	var spawnResult map[string]interface{}
	json.Unmarshal(body, &spawnResult)
	sessionID := spawnResult["session_id"].(string)

	// Set session to Running.
	mock.setPhase(sessionID, "Running")

	// GET introspect.
	code, introBody := getBody(t, base+"/spaces/e2e/agent/inspector/introspect")
	if code != http.StatusOK {
		t.Fatalf("introspect: expected 200, got %d: %s", code, introBody)
	}

	var introResp introspectResponse
	if err := json.Unmarshal([]byte(introBody), &introResp); err != nil {
		t.Fatalf("unmarshal introspect: %v", err)
	}

	if !introResp.SessionExists {
		t.Error("expected session_exists=true")
	}
	if introResp.Agent != "inspector" {
		t.Errorf("expected agent=inspector, got %q", introResp.Agent)
	}
	// Lines should be populated from the MESSAGES_SNAPSHOT fallback in aguiEvents.
	if len(introResp.Lines) == 0 {
		t.Error("expected non-empty lines from aguiEvents MESSAGES_SNAPSHOT")
	}
	if len(introResp.Lines) > 0 && !strings.Contains(introResp.Lines[0], "hello from test") {
		t.Errorf("expected first line to contain test content, got %q", introResp.Lines[0])
	}
}

// --- Group 4: Discovery ---

func TestHandlerAmbientAutoDiscover(t *testing.T) {
	mock := newAmbientAPIMock("test-project")
	srv, base := mustStartAmbientServer(t, mock)

	// Create an agent record without a session_id (simulate orphan).
	postJSON(t, base+"/spaces/e2e/agent/orphan1", map[string]any{
		"status":  "active",
		"summary": "orphan1: working",
	})

	// Inject a session into the mock with boss-agent=orphan1 label.
	mock.mu.Lock()
	mock.sessions["discovered-sess"] = backendCR("discovered-sess", "Running", "orphan1",
		map[string]string{"boss-agent": "orphan1", "managed-by": "agent-boss"})
	mock.mu.Unlock()

	// GET session-status triggers AutoDiscoverAll.
	code, statusBody := getBody(t, base+"/spaces/e2e/api/session-status")
	if code != http.StatusOK {
		t.Fatalf("session-status: expected 200, got %d: %s", code, statusBody)
	}

	// Verify the agent now has the discovered session_id.
	srv.mu.RLock()
	ks := srv.spaces["e2e"]
	agent := ks.agentStatus("orphan1")
	srv.mu.RUnlock()

	if agent == nil {
		t.Fatal("expected agent orphan1 to exist")
	}
	if agent.SessionID != "discovered-sess" {
		t.Errorf("expected session_id=discovered-sess, got %q", agent.SessionID)
	}
	if agent.BackendType != "ambient" {
		t.Errorf("expected backend_type=ambient, got %q", agent.BackendType)
	}
}
