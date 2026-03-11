package coordinator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
)

// postSpawn posts to /spaces/{space}/agent/{agent}/spawn with given headers and body.
func postSpawn(t *testing.T, base, space, agentName string, body map[string]interface{}, spawnerName string) *http.Response {
	t.Helper()
	data, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost,
		base+"/spaces/"+space+"/agent/"+agentName+"/spawn",
		bytes.NewReader(data))
	if err != nil {
		t.Fatalf("new spawn request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if spawnerName != "" {
		req.Header.Set("X-Agent-Name", spawnerName)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST spawn: %v", err)
	}
	return resp
}

// killTmuxSession cleans up a tmux session if it exists.
func killTmuxSession(session string) {
	exec.Command("tmux", "kill-session", "-t", session).Run() //nolint:errcheck
}

// tmuxServerRunning returns true if a tmux server is reachable.
func tmuxServerRunning() bool {
	err := exec.Command("tmux", "list-sessions").Run()
	return err == nil
}

// requireTmux skips the test if tmux is unavailable or no server is running.
func requireTmux(t *testing.T) {
	t.Helper()
	if !tmuxAvailable() {
		t.Skip("tmux binary not found")
	}
	if !tmuxServerRunning() {
		t.Skip("no tmux server running")
	}
}

// TestSpawnSetsParentFromSpawner verifies that when an agent spawns a child via
// POST /agent/{child}/spawn with X-Agent-Name: {spawner}, the child's Parent is
// set to the canonical spawner name.
func TestSpawnSetsParentFromSpawner(t *testing.T) {
	requireTmux(t)

	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "TestSpawnParent"

	// Register the spawner agent first.
	postJSON(t, base+"/spaces/"+space+"/agent/Manager", map[string]interface{}{
		"status":  "active",
		"summary": "Manager: active",
	})

	session := fmt.Sprintf("test-spawn-parent-%s", srv.Port()[1:])
	defer killTmuxSession(session)

	resp := postSpawn(t, base, space, "Worker", map[string]interface{}{
		"session_id": session,
		"command":      "sleep 30",
	}, "Manager")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("spawn: expected 202, got %d: %s", resp.StatusCode, body)
	}

	srv.mu.RLock()
	ks := srv.spaces[space]
	workerCanonical := resolveAgentName(ks, "Worker")
	worker := ks.agentStatus(workerCanonical)
	srv.mu.RUnlock()

	if worker == nil {
		t.Fatal("Worker agent record not found after spawn")
	}
	if worker.Parent == "" {
		t.Error("expected Worker.Parent to be set to manager's canonical name")
	}
	// Parent must not equal the agent itself.
	if strings.EqualFold(worker.Parent, workerCanonical) {
		t.Errorf("Worker.Parent must not be the agent itself, got %q", worker.Parent)
	}
}

// TestSpawnNoSpawnerNoParent verifies that spawning without X-Agent-Name does not
// set a parent on the spawned agent.
func TestSpawnNoSpawnerNoParent(t *testing.T) {
	requireTmux(t)

	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "TestSpawnNoParent"

	session := fmt.Sprintf("test-spawn-no-parent-%s", srv.Port()[1:])
	defer killTmuxSession(session)

	// Spawn without X-Agent-Name header (spawnerName = "").
	resp := postSpawn(t, base, space, "Orphan", map[string]interface{}{
		"session_id": session,
		"command":      "sleep 30",
	}, "")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("spawn: expected 202, got %d: %s", resp.StatusCode, body)
	}

	srv.mu.RLock()
	ks := srv.spaces[space]
	orphanCanonical := resolveAgentName(ks, "Orphan")
	orphan := ks.agentStatus(orphanCanonical)
	srv.mu.RUnlock()

	if orphan == nil {
		t.Fatal("Orphan agent record not found after spawn")
	}
	if orphan.Parent != "" {
		t.Errorf("expected Orphan.Parent to be empty, got %q", orphan.Parent)
	}
}

// TestSpawnSelfSpawnNoParent verifies that when X-Agent-Name equals the spawned
// agent's name, Parent is NOT set (no self-parent).
func TestSpawnSelfSpawnNoParent(t *testing.T) {
	requireTmux(t)

	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "TestSpawnSelf"

	session := fmt.Sprintf("test-spawn-self-%s", srv.Port()[1:])
	defer killTmuxSession(session)

	// X-Agent-Name == agentName → self-spawn, no parent should be set.
	resp := postSpawn(t, base, space, "Solo", map[string]interface{}{
		"session_id": session,
		"command":      "sleep 30",
	}, "Solo")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("spawn: expected 202, got %d: %s", resp.StatusCode, body)
	}

	srv.mu.RLock()
	ks := srv.spaces[space]
	canonical := resolveAgentName(ks, "Solo")
	solo := ks.agentStatus(canonical)
	srv.mu.RUnlock()

	if solo == nil {
		t.Fatal("Solo agent not found after spawn")
	}
	if solo.Parent != "" {
		t.Errorf("expected Solo.Parent to be empty (self-spawn), got %q", solo.Parent)
	}
}

// TestIgnitionWithParentParam verifies that GET /ignition?session_id=X&parent=Y&role=Z
// sets the agent's Parent and Role fields.
func TestIgnitionWithParentParam(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "TestIgnitionParent"

	// Register the parent agent first.
	postJSON(t, base+"/spaces/"+space+"/agent/Boss", map[string]interface{}{
		"status":  "active",
		"summary": "Boss: active",
	})

	// Call ignition with ?parent=Boss&role=Worker.
	url := base + "/spaces/" + space + "/ignition/Worker?session_id=fake-session&parent=Boss&role=Developer"
	code, body := getBody(t, url)
	if code != http.StatusOK {
		t.Fatalf("ignition: expected 200, got %d: %s", code, body)
	}

	srv.mu.RLock()
	ks := srv.spaces[space]
	canonical := resolveAgentName(ks, "Worker")
	worker := ks.agentStatus(canonical)
	srv.mu.RUnlock()

	if worker == nil {
		t.Fatal("Worker not found after ignition")
	}
	if worker.Parent == "" {
		t.Error("expected Worker.Parent to be set after ignition with ?parent=Boss")
	}
	if worker.Role == "" {
		t.Error("expected Worker.Role to be set after ignition with ?role=Developer")
	}
}

// TestIgnitionParentStickyNotOverwritten verifies that calling ignition with
// ?parent=NewParent does not overwrite an already-set Parent.
func TestIgnitionParentStickyNotOverwritten(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "TestIgnitionSticky"

	// Register parent agents.
	for _, name := range []string{"OriginalParent", "NewParent", "Child"} {
		postJSON(t, base+"/spaces/"+space+"/agent/"+name, map[string]interface{}{
			"status":  "active",
			"summary": name + ": active",
		})
	}

	// Set Child's parent to OriginalParent via ignition.
	url1 := base + "/spaces/" + space + "/ignition/Child?session_id=s1&parent=OriginalParent"
	code, body := getBody(t, url1)
	if code != http.StatusOK {
		t.Fatalf("ignition 1: expected 200, got %d: %s", code, body)
	}

	// Try to overwrite parent to NewParent via second ignition call.
	url2 := base + "/spaces/" + space + "/ignition/Child?session_id=s2&parent=NewParent"
	code, body = getBody(t, url2)
	if code != http.StatusOK {
		t.Fatalf("ignition 2: expected 200, got %d: %s", code, body)
	}

	srv.mu.RLock()
	ks := srv.spaces[space]
	canonical := resolveAgentName(ks, "Child")
	child := ks.agentStatus(canonical)
	srv.mu.RUnlock()

	if child == nil {
		t.Fatal("Child not found")
	}
	// Parent must remain OriginalParent (sticky).
	originalCanonical := resolveAgentName(ks, "OriginalParent")
	if child.Parent != originalCanonical {
		t.Errorf("expected sticky Parent=%q, got %q", originalCanonical, child.Parent)
	}
}

// TestIgnitionSelfParentRejected verifies that calling ignition with ?parent equal
// to the agent's own name returns 400.
func TestIgnitionSelfParentRejected(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "TestIgnitionSelfParent"

	url := base + "/spaces/" + space + "/ignition/Agent1?session_id=s1&parent=Agent1"
	code, body := getBody(t, url)
	if code != http.StatusBadRequest {
		t.Errorf("expected 400 for self-parent, got %d: %s", code, body)
	}
}

// TestIgnitionParentCycleRejected verifies that calling ignition with ?parent that
// would create a cycle returns 400.
func TestIgnitionParentCycleRejected(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "TestIgnitionCycle"

	// Register Alpha and Beta.
	postJSON(t, base+"/spaces/"+space+"/agent/Alpha", map[string]interface{}{
		"status":  "active",
		"summary": "Alpha: active",
	})
	postJSON(t, base+"/spaces/"+space+"/agent/Beta", map[string]interface{}{
		"status":  "active",
		"summary": "Beta: active",
	})

	// Set Alpha's parent = Beta via ignition (no cycle yet).
	url1 := base + "/spaces/" + space + "/ignition/Alpha?session_id=s1&parent=Beta"
	code, body := getBody(t, url1)
	if code != http.StatusOK {
		t.Fatalf("first ignition: expected 200, got %d: %s", code, body)
	}

	// Now try to set Beta's parent = Alpha → creates cycle Alpha→Beta→Alpha.
	url2 := base + "/spaces/" + space + "/ignition/Beta?session_id=s2&parent=Alpha"
	code, body = getBody(t, url2)
	if code != http.StatusBadRequest {
		t.Errorf("expected 400 for cycle-creating parent, got %d: %s", code, body)
	}
}

// TestIgnitionPostTemplateIncludesParent verifies that when an agent has a Parent set,
// the ignition response's POST template JSON includes the parent and role fields.
func TestIgnitionPostTemplateIncludesParent(t *testing.T) {
	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "TestIgnitionTemplate"

	// Register parent agent.
	postJSON(t, base+"/spaces/"+space+"/agent/Boss", map[string]interface{}{
		"status":  "active",
		"summary": "Boss: active",
	})

	// Ignite Worker with parent=Boss and role=Developer.
	url := base + "/spaces/" + space + "/ignition/Worker?session_id=s1&parent=Boss&role=Developer"
	code, body := getBody(t, url)
	if code != http.StatusOK {
		t.Fatalf("ignition: expected 200, got %d: %s", code, body)
	}

	// The "Your Last State" section must show the manager relationship.
	if !strings.Contains(body, "Manager: Boss") {
		t.Errorf("ignition response missing Manager in Your Last State; body snippet: %s",
			truncate(body, 500))
	}
}

// TestSpawnParentPropagatedToIgnition verifies the end-to-end: after spawn sets
// Parent (via X-Agent-Name), a subsequent ignition call reflects that parent
// in the "Your Last State" section.
func TestSpawnParentPropagatedToIgnition(t *testing.T) {
	requireTmux(t)

	srv, cleanup := mustStartServer(t)
	defer cleanup()
	base := serverBaseURL(srv)
	space := "TestSpawnToIgnition"

	// Register spawner.
	postJSON(t, base+"/spaces/"+space+"/agent/Spawner", map[string]interface{}{
		"status":  "active",
		"summary": "Spawner: active",
	})

	session := fmt.Sprintf("test-spawn-ignition-%s", srv.Port()[1:])
	defer killTmuxSession(session)

	// Spawn Child with X-Agent-Name: Spawner.
	resp := postSpawn(t, base, space, "Child", map[string]interface{}{
		"session_id": session,
		"command":      "sleep 30",
	}, "Spawner")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("spawn: expected 202, got %d: %s", resp.StatusCode, body)
	}

	// Verify parent was set on agent.
	srv.mu.RLock()
	ks := srv.spaces[space]
	childCanonical := resolveAgentName(ks, "Child")
	child := ks.agentStatus(childCanonical)
	srv.mu.RUnlock()

	if child == nil {
		t.Fatal("Child agent not found after spawn")
	}
	if child.Parent == "" {
		t.Fatal("Child.Parent not set after spawn — cannot verify ignition propagation")
	}

	// Now call ignition for Child and verify parent appears in the response.
	ignitionURL := base + "/spaces/" + space + "/ignition/Child"
	code, body := getBody(t, ignitionURL)
	if code != http.StatusOK {
		t.Fatalf("ignition: expected 200, got %d: %s", code, body)
	}
	if !strings.Contains(body, child.Parent) {
		t.Errorf("ignition response does not mention parent %q; got: %s",
			child.Parent, truncate(body, 300))
	}
}

// truncate returns at most n characters of s for safe error messages.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
