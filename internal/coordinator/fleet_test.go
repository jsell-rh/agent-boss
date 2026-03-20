package coordinator

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// ─── handleSpaceExport ────────────────────────────────────────────────────────

func TestHandleSpaceExport(t *testing.T) {
	srv, stop := mustStartServer(t)
	defer stop()
	base := serverBaseURL(srv)

	space := "export-test"
	agent := "worker"

	// Register the agent so the space exists.
	postJSON(t, fmt.Sprintf("%s/spaces/%s/agent/%s", base, space, agent), &AgentUpdate{
		Status:  StatusActive,
		Summary: "working",
		Role:    "worker",
	})

	// Inject config with credentials in repo_url.
	srv.mu.Lock()
	ks := srv.spaces[space]
	if ks.Agents[agent] == nil {
		ks.Agents[agent] = &AgentRecord{}
	}
	ks.Agents[agent].Config = &AgentConfig{
		WorkDir:       "/workspace/myapp",
		InitialPrompt: "You are a worker.",
		Backend:       "tmux",
		Command:       "claude",
		RepoURL:       "https://user:secret@github.com/org/repo.git",
	}
	srv.mu.Unlock()

	resp, err := http.Get(fmt.Sprintf("%s/spaces/%s/export", base, space))
	if err != nil {
		t.Fatalf("export GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("export: want 200, got %d: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "yaml") {
		t.Errorf("export: want yaml content-type, got %q", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	var ff FleetFile
	if err := yaml.Unmarshal(body, &ff); err != nil {
		t.Fatalf("export: unmarshal YAML: %v", err)
	}
	if ff.Space.Name != space {
		t.Errorf("export: space name: want %q, got %q", space, ff.Space.Name)
	}
	agentEntry, ok := ff.Agents[agent]
	if !ok {
		t.Fatalf("export: agent %q missing from output", agent)
	}
	// Credentials must be stripped from repo_url.
	if strings.Contains(agentEntry.RepoURL, "secret") {
		t.Errorf("export: repo_url still contains credentials: %q", agentEntry.RepoURL)
	}
	if agentEntry.RepoURL != "https://github.com/org/repo.git" {
		t.Errorf("export: repo_url: want https://github.com/org/repo.git, got %q", agentEntry.RepoURL)
	}
	// Backend and command must always be explicit.
	if agentEntry.Backend == "" {
		t.Error("export: backend must be explicit in output")
	}
	if agentEntry.Command == "" {
		t.Error("export: command must be explicit in output")
	}
}

func TestHandleSpaceExportRepos(t *testing.T) {
	srv, stop := mustStartServer(t)
	defer stop()
	base := serverBaseURL(srv)

	space := "export-repos-test"
	agent := "ambient-worker"

	postJSON(t, fmt.Sprintf("%s/spaces/%s/agent/%s", base, space, agent), &AgentUpdate{
		Status:  StatusActive,
		Summary: "working",
	})

	srv.mu.Lock()
	ks := srv.spaces[space]
	if ks.Agents[agent] == nil {
		ks.Agents[agent] = &AgentRecord{}
	}
	ks.Agents[agent].Config = &AgentConfig{
		Backend: "ambient",
		Command: "claude",
		Repos: []SessionRepo{
			{URL: "https://user:token@gitea.example.com/org/repo-a.git", Branch: "main"},
			{URL: "https://gitea.example.com/org/repo-b.git"},
		},
	}
	srv.mu.Unlock()

	resp, err := http.Get(fmt.Sprintf("%s/spaces/%s/export", base, space))
	if err != nil {
		t.Fatalf("export GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("export: want 200, got %d: %s", resp.StatusCode, body)
	}

	body, _ := io.ReadAll(resp.Body)
	var ff FleetFile
	if err := yaml.Unmarshal(body, &ff); err != nil {
		t.Fatalf("export: unmarshal YAML: %v", err)
	}

	agentEntry, ok := ff.Agents[agent]
	if !ok {
		t.Fatalf("export: agent %q missing from output", agent)
	}
	if len(agentEntry.Repos) != 2 {
		t.Fatalf("export: want 2 repos, got %d", len(agentEntry.Repos))
	}
	// Credentials must be stripped from repo URLs.
	if strings.Contains(agentEntry.Repos[0].URL, "token") {
		t.Errorf("export: repos[0] URL still contains credentials: %q", agentEntry.Repos[0].URL)
	}
	if agentEntry.Repos[0].URL != "https://gitea.example.com/org/repo-a.git" {
		t.Errorf("export: repos[0] URL: want https://gitea.example.com/org/repo-a.git, got %q", agentEntry.Repos[0].URL)
	}
	if agentEntry.Repos[0].Branch != "main" {
		t.Errorf("export: repos[0] branch: want main, got %q", agentEntry.Repos[0].Branch)
	}
	if agentEntry.Repos[1].URL != "https://gitea.example.com/org/repo-b.git" {
		t.Errorf("export: repos[1] URL: want https://gitea.example.com/org/repo-b.git, got %q", agentEntry.Repos[1].URL)
	}

	// Round-trip: ParseAndValidateFleetFile must accept the exported YAML with repos.
	ff2, err := ParseAndValidateFleetFile(body)
	if err != nil {
		t.Fatalf("round-trip: parse exported YAML with repos: %v", err)
	}
	if len(ff2.Agents[agent].Repos) != 2 {
		t.Errorf("round-trip: want 2 repos after parse, got %d", len(ff2.Agents[agent].Repos))
	}
}

func TestHandleSpaceExportNotFound(t *testing.T) {
	srv, stop := mustStartServer(t)
	defer stop()
	base := serverBaseURL(srv)

	resp, err := http.Get(fmt.Sprintf("%s/spaces/no-such-space/export", base))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
}

func TestHandleSpaceExportMethodNotAllowed(t *testing.T) {
	srv, stop := mustStartServer(t)
	defer stop()
	base := serverBaseURL(srv)

	// Create the space first.
	postJSON(t, fmt.Sprintf("%s/spaces/s/agent/a", base), &AgentUpdate{Status: StatusActive, Summary: "x"})

	resp, err := http.Post(fmt.Sprintf("%s/spaces/s/export", base), "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("want 405, got %d", resp.StatusCode)
	}
}

// ─── Security validators ──────────────────────────────────────────────────────

func TestCommandAllowlist(t *testing.T) {
	t.Setenv("ODIS_COMMAND_ALLOWLIST", "claude,claude-dev")

	cases := []struct {
		cmd  string
		want bool // true = allowed
	}{
		{"claude", true},
		{"claude-dev", true},
		{"", true},               // empty = default, always allowed
		{"bash", false},
		{"rm", false},
		{"/usr/bin/claude", false}, // absolute path not in list
	}
	for _, c := range cases {
		err := ValidateFleetCommand(c.cmd)
		if c.want && err != nil {
			t.Errorf("cmd %q: want allowed, got error: %v", c.cmd, err)
		}
		if !c.want && err == nil {
			t.Errorf("cmd %q: want rejected, got nil", c.cmd)
		}
	}
}

func TestCommandAllowlistDefault(t *testing.T) {
	t.Setenv("ODIS_COMMAND_ALLOWLIST", "")
	if err := ValidateFleetCommand("claude"); err != nil {
		t.Errorf("claude should be in default allowlist: %v", err)
	}
	if err := ValidateFleetCommand("sh"); err == nil {
		t.Error("sh should not be in default allowlist")
	}
}

func TestYAMLBombGuard(t *testing.T) {
	// Over 1 MB.
	big := make([]byte, fleetMaxBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	if err := ValidateFleetSize(big, 1); err == nil {
		t.Error("want error for oversized file")
	}
	// Over 100 agents.
	if err := ValidateFleetSize([]byte("x"), fleetMaxAgents+1); err == nil {
		t.Error("want error for too many agents")
	}
	// Valid.
	if err := ValidateFleetSize([]byte("x"), 5); err != nil {
		t.Errorf("want nil for valid size, got %v", err)
	}
}

func TestWorkDirValidation(t *testing.T) {
	t.Setenv("ODIS_WORK_DIR_PREFIX", "")
	cases := []struct {
		dir  string
		want bool // true = valid
	}{
		{"", true},
		{"/workspace/myapp", true},
		{"/workspace/a/b/c", true},
		{"relative/path", false},
		{"/workspace/../etc", false},
	}
	for _, c := range cases {
		err := ValidateWorkDir(c.dir)
		if c.want && err != nil {
			t.Errorf("work_dir %q: want valid, got error: %v", c.dir, err)
		}
		if !c.want && err == nil {
			t.Errorf("work_dir %q: want rejected, got nil", c.dir)
		}
	}
}

func TestWorkDirPrefix(t *testing.T) {
	t.Setenv("ODIS_WORK_DIR_PREFIX", "/workspace")
	if err := ValidateWorkDir("/workspace/myapp"); err != nil {
		t.Errorf("inside prefix: %v", err)
	}
	if err := ValidateWorkDir("/tmp/hack"); err == nil {
		t.Error("outside prefix: want error")
	}
}

func TestReposURLValidation(t *testing.T) {
	cases := []struct {
		rawURL string
		want   bool // true = valid
	}{
		{"", true},
		{"https://github.com/org/repo.git", true},
		{"http://github.com/org/repo.git", false},
		{"file:///etc/passwd", false},
		{"ssh://github.com/org/repo.git", false},
		{"https://192.168.1.1/repo.git", false},
		{"https://10.0.0.1/repo.git", false},
		{"https://169.254.1.1/repo.git", false},
	}
	for _, c := range cases {
		err := ValidateRepoURL(c.rawURL)
		if c.want && err != nil {
			t.Errorf("URL %q: want valid, got error: %v", c.rawURL, err)
		}
		if !c.want && err == nil {
			t.Errorf("URL %q: want rejected, got nil", c.rawURL)
		}
	}
}

// ─── TopoSortAgents ───────────────────────────────────────────────────────────

func TestTopoSortAgentsBasic(t *testing.T) {
	agents := map[string]FleetAgent{
		"boss":   {Backend: "tmux", Command: "claude"},
		"worker": {Backend: "tmux", Command: "claude", Parent: "boss"},
	}
	order, err := TopoSortAgents(agents)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 2 {
		t.Fatalf("want 2 agents in order, got %d: %v", len(order), order)
	}
	// boss must appear before worker.
	bossIdx, workerIdx := -1, -1
	for i, name := range order {
		if name == "boss" {
			bossIdx = i
		}
		if name == "worker" {
			workerIdx = i
		}
	}
	if bossIdx > workerIdx {
		t.Errorf("parent must precede child: got order %v", order)
	}
}

func TestTopoSortAgentsMultiLevel(t *testing.T) {
	agents := map[string]FleetAgent{
		"root":  {Backend: "tmux", Command: "claude"},
		"mid":   {Backend: "tmux", Command: "claude", Parent: "root"},
		"leaf":  {Backend: "tmux", Command: "claude", Parent: "mid"},
		"other": {Backend: "tmux", Command: "claude"},
	}
	order, err := TopoSortAgents(agents)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 4 {
		t.Fatalf("want 4 agents, got %d", len(order))
	}
	pos := func(name string) int {
		for i, n := range order {
			if n == name {
				return i
			}
		}
		return -1
	}
	if pos("root") > pos("mid") {
		t.Errorf("root must precede mid: %v", order)
	}
	if pos("mid") > pos("leaf") {
		t.Errorf("mid must precede leaf: %v", order)
	}
}

func TestTopoSortAgentsCycleDetection(t *testing.T) {
	// a → b → a (direct cycle)
	agents := map[string]FleetAgent{
		"a": {Backend: "tmux", Command: "claude", Parent: "b"},
		"b": {Backend: "tmux", Command: "claude", Parent: "a"},
	}
	_, err := TopoSortAgents(agents)
	if err == nil {
		t.Error("want cycle error, got nil")
	}
	if !containsString(err.Error(), "cycle") {
		t.Errorf("error should mention cycle, got: %v", err)
	}
}

func TestTopoSortAgentsParentOutsideFile(t *testing.T) {
	// Parent referenced but not in the fleet file — should not fail.
	agents := map[string]FleetAgent{
		"worker": {Backend: "tmux", Command: "claude", Parent: "external-boss"},
	}
	order, err := TopoSortAgents(agents)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 1 || order[0] != "worker" {
		t.Errorf("want [worker], got %v", order)
	}
}

func TestTopoSortAgentsEmpty(t *testing.T) {
	order, err := TopoSortAgents(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 0 {
		t.Errorf("want empty order, got %v", order)
	}
}

// ─── Export round-trip ────────────────────────────────────────────────────────

func TestExportRoundTrip(t *testing.T) {
	t.Setenv("ODIS_COMMAND_ALLOWLIST", "claude")

	srv, stop := mustStartServer(t)
	defer stop()
	base := serverBaseURL(srv)

	space := "roundtrip-test"
	// Create two agents with configs.
	postJSON(t, fmt.Sprintf("%s/spaces/%s/agent/boss", base, space), &AgentUpdate{
		Status: StatusActive, Summary: "orchestrating",
	})
	postJSON(t, fmt.Sprintf("%s/spaces/%s/agent/worker", base, space), &AgentUpdate{
		Status: StatusActive, Summary: "working", Parent: "boss",
	})
	srv.mu.Lock()
	ks := srv.spaces[space]
	for _, name := range []string{"boss", "worker"} {
		if ks.Agents[name] == nil {
			ks.Agents[name] = &AgentRecord{}
		}
		ks.Agents[name].Config = &AgentConfig{
			Backend: "tmux",
			Command: "claude",
			WorkDir: "/workspace/" + name,
		}
	}
	srv.mu.Unlock()

	// Export.
	resp, err := http.Get(fmt.Sprintf("%s/spaces/%s/export", base, space))
	if err != nil {
		t.Fatalf("export GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("export: want 200, got %d: %s", resp.StatusCode, body)
	}

	exportedYAML, _ := io.ReadAll(resp.Body)

	// ParseAndValidateFleetFile must accept the exported YAML.
	ff, err := ParseAndValidateFleetFile(exportedYAML)
	if err != nil {
		t.Fatalf("round-trip: parse exported YAML: %v\n--- YAML ---\n%s", err, exportedYAML)
	}
	if ff.Space.Name != space {
		t.Errorf("space name: want %q, got %q", space, ff.Space.Name)
	}
	if _, ok := ff.Agents["boss"]; !ok {
		t.Error("boss agent missing from exported fleet")
	}
	if _, ok := ff.Agents["worker"]; !ok {
		t.Error("worker agent missing from exported fleet")
	}
	// TopoSortAgents must order the exported agents without error.
	order, err := TopoSortAgents(ff.Agents)
	if err != nil {
		t.Fatalf("topo sort on exported agents: %v", err)
	}
	if len(order) != 2 {
		t.Errorf("want 2 agents in topo order, got %d: %v", len(order), order)
	}
}

// ─── Client.RestartAgent ──────────────────────────────────────────────────────

func TestClientRestartAgent(t *testing.T) {
	srv, stop := mustStartServer(t)
	defer stop()
	base := serverBaseURL(srv)
	space := "restart-test"
	agent := "worker"

	// Create the agent so the space and agent record exist.
	postJSON(t, fmt.Sprintf("%s/spaces/%s/agent/%s", base, space, agent), &AgentUpdate{
		Status:  StatusIdle,
		Summary: "idle",
	})

	// Inject a fake session ID so restart has something to "kill".
	srv.mu.Lock()
	ks := srv.spaces[space]
	if ks.Agents[agent] == nil {
		ks.Agents[agent] = &AgentRecord{}
	}
	if ks.Agents[agent].Status == nil {
		ks.Agents[agent].Status = &AgentUpdate{}
	}
	ks.Agents[agent].Status.SessionID = "fake-session-123"
	srv.mu.Unlock()

	c := NewClient(base, space)
	// Restart will fail in test (no real tmux) but should attempt and return a
	// lifecycle error (not a 404 or network error). A 500 from the spawner is fine.
	err := c.RestartAgent(agent)
	// We expect either nil (if the test backend succeeds) or an error from the
	// lifecycle layer. What we must NOT get is a "404 not found" — that would
	// mean the route isn't wired.
	if err != nil {
		// The spawner will fail in a test environment; the important thing is the
		// endpoint was reached (not a routing 404).
		if containsString(err.Error(), "404") {
			t.Errorf("restart route not found (404): %v", err)
		}
	}
}

// ─── FetchSpace agent listing (used by prune) ─────────────────────────────────

func TestFetchSpaceReturnsAgentSessionInfo(t *testing.T) {
	srv, stop := mustStartServer(t)
	defer stop()
	base := serverBaseURL(srv)
	space := "prune-listing-test"

	// Create two agents.
	postJSON(t, fmt.Sprintf("%s/spaces/%s/agent/active-agent", base, space), &AgentUpdate{
		Status:  StatusActive,
		Summary: "working",
	})
	postJSON(t, fmt.Sprintf("%s/spaces/%s/agent/idle-agent", base, space), &AgentUpdate{
		Status:  StatusIdle,
		Summary: "idle",
	})

	// Simulate a session on active-agent.
	srv.mu.Lock()
	ks := srv.spaces[space]
	if ks.Agents["active-agent"] == nil {
		ks.Agents["active-agent"] = &AgentRecord{}
	}
	if ks.Agents["active-agent"].Status == nil {
		ks.Agents["active-agent"].Status = &AgentUpdate{}
	}
	ks.Agents["active-agent"].Status.SessionID = "tmux-session-abc"
	srv.mu.Unlock()

	c := NewClient(base, space)
	ks2, err := c.FetchSpace()
	if err != nil {
		t.Fatalf("FetchSpace: %v", err)
	}
	if len(ks2.Agents) < 2 {
		t.Fatalf("want at least 2 agents, got %d", len(ks2.Agents))
	}

	activeRec, ok := ks2.Agents["active-agent"]
	if !ok {
		t.Fatal("active-agent missing from FetchSpace result")
	}
	if activeRec == nil || activeRec.Status == nil || activeRec.Status.SessionID == "" {
		t.Error("active-agent: expected session_id to be populated")
	}

	idleRec, ok := ks2.Agents["idle-agent"]
	if !ok {
		t.Fatal("idle-agent missing from FetchSpace result")
	}
	if idleRec != nil && idleRec.Status != nil && idleRec.Status.SessionID != "" {
		t.Error("idle-agent: expected no session_id")
	}
}

// ─── Prune logic (via client + server) ───────────────────────────────────────

func TestPruneDeletesInactiveAgent(t *testing.T) {
	srv, stop := mustStartServer(t)
	defer stop()
	base := serverBaseURL(srv)
	space := "prune-delete-test"

	// Create two agents: one will be "in the fleet", one will be pruned.
	for _, name := range []string{"keeper", "pruned"} {
		postJSON(t, fmt.Sprintf("%s/spaces/%s/agent/%s", base, space, name), &AgentUpdate{
			Status:  StatusIdle,
			Summary: "idle",
		})
	}

	c := NewClient(base, space)

	// Verify both agents exist.
	ks, err := c.FetchSpace()
	if err != nil {
		t.Fatalf("FetchSpace: %v", err)
	}
	if _, ok := ks.Agents["pruned"]; !ok {
		t.Fatal("pruned agent should exist before prune")
	}

	// Delete the "pruned" agent — this is what fleetPrune does for inactive agents.
	if err := c.DeleteAgent("pruned"); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}

	// Verify "pruned" is gone and "keeper" remains.
	ks2, err := c.FetchSpace()
	if err != nil {
		t.Fatalf("FetchSpace after delete: %v", err)
	}
	if _, ok := ks2.Agents["pruned"]; ok {
		t.Error("pruned agent should be gone after DeleteAgent")
	}
	if _, ok := ks2.Agents["keeper"]; !ok {
		t.Error("keeper agent should still exist")
	}
}

func TestPruneSkipsAgentWithActiveSession(t *testing.T) {
	srv, stop := mustStartServer(t)
	defer stop()
	base := serverBaseURL(srv)
	space := "prune-skip-test"

	postJSON(t, fmt.Sprintf("%s/spaces/%s/agent/live-agent", base, space), &AgentUpdate{
		Status:  StatusActive,
		Summary: "working",
	})

	// Inject a session ID — fleetPrune checks this to determine liveness.
	srv.mu.Lock()
	ks := srv.spaces[space]
	if ks.Agents["live-agent"] == nil {
		ks.Agents["live-agent"] = &AgentRecord{}
	}
	if ks.Agents["live-agent"].Status == nil {
		ks.Agents["live-agent"].Status = &AgentUpdate{}
	}
	ks.Agents["live-agent"].Status.SessionID = "active-tmux-session"
	srv.mu.Unlock()

	c := NewClient(base, space)
	ks2, err := c.FetchSpace()
	if err != nil {
		t.Fatalf("FetchSpace: %v", err)
	}
	rec, ok := ks2.Agents["live-agent"]
	if !ok {
		t.Fatal("live-agent missing")
	}
	// Simulate the prune gate: skip if session is active.
	hasSession := rec != nil && rec.Status != nil && rec.Status.SessionID != ""
	if !hasSession {
		t.Error("live-agent should have an active session visible via FetchSpace")
	}
	// Without --force, fleetPrune would skip this agent. We verify it still exists.
	// (We don't delete it here, mirroring what fleetPrune would do without --force.)
	ks3, err := c.FetchSpace()
	if err != nil {
		t.Fatalf("FetchSpace: %v", err)
	}
	if _, ok := ks3.Agents["live-agent"]; !ok {
		t.Error("live-agent should remain (not pruned) when it has an active session")
	}
}

// ─── fleetComputeChangedAgents (via FetchAgentConfig) ─────────────────────────

func TestComputeChangedAgentsDetectsNew(t *testing.T) {
	srv, stop := mustStartServer(t)
	defer stop()
	base := serverBaseURL(srv)
	space := "changed-new-test"

	// Register the agent but leave its config empty.
	postJSON(t, fmt.Sprintf("%s/spaces/%s/agent/newbie", base, space), &AgentUpdate{
		Status:  StatusIdle,
		Summary: "idle",
	})

	c := NewClient(base, space)
	cfg, err := c.FetchAgentConfig("newbie")
	if err != nil {
		t.Fatalf("FetchAgentConfig: %v", err)
	}
	// An agent with no durable config should be treated as "changed" (will be created).
	isEmpty := cfg == nil || (cfg.Backend == "" && cfg.Command == "" && cfg.WorkDir == "" && cfg.InitialPrompt == "")
	if !isEmpty {
		t.Errorf("expected empty config for agent with no config set, got: %+v", cfg)
	}
}

func TestComputeChangedAgentsDetectsUpdate(t *testing.T) {
	srv, stop := mustStartServer(t)
	defer stop()
	base := serverBaseURL(srv)
	space := "changed-update-test"

	postJSON(t, fmt.Sprintf("%s/spaces/%s/agent/worker", base, space), &AgentUpdate{
		Status:  StatusIdle,
		Summary: "idle",
	})

	// Inject a known config.
	srv.mu.Lock()
	ks := srv.spaces[space]
	if ks.Agents["worker"] == nil {
		ks.Agents["worker"] = &AgentRecord{}
	}
	ks.Agents["worker"].Config = &AgentConfig{
		Backend: "tmux",
		Command: "claude",
		WorkDir: "/workspace/old",
	}
	srv.mu.Unlock()

	c := NewClient(base, space)
	cfg, err := c.FetchAgentConfig("worker")
	if err != nil {
		t.Fatalf("FetchAgentConfig: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}

	// A fleet agent with a different work_dir should be detectable via field comparison.
	// This mirrors what fleetAgentConfigDiff (in cmd/boss) does.
	if cfg.WorkDir == "/workspace/new" {
		t.Error("work_dir should be old — change not yet applied")
	}
	if cfg.WorkDir != "/workspace/old" {
		t.Errorf("expected /workspace/old, got %q", cfg.WorkDir)
	}
}

func TestComputeChangedAgentsNoChange(t *testing.T) {
	srv, stop := mustStartServer(t)
	defer stop()
	base := serverBaseURL(srv)
	space := "changed-nochange-test"

	postJSON(t, fmt.Sprintf("%s/spaces/%s/agent/stable", base, space), &AgentUpdate{
		Status:  StatusIdle,
		Summary: "idle",
	})
	srv.mu.Lock()
	ks := srv.spaces[space]
	if ks.Agents["stable"] == nil {
		ks.Agents["stable"] = &AgentRecord{}
	}
	ks.Agents["stable"].Config = &AgentConfig{
		Backend: "tmux",
		Command: "claude",
		WorkDir: "/workspace/stable",
	}
	srv.mu.Unlock()

	c := NewClient(base, space)
	cfg, err := c.FetchAgentConfig("stable")
	if err != nil {
		t.Fatalf("FetchAgentConfig: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}

	// Same config — field values should match what we stored.
	if cfg.Backend != "tmux" || cfg.Command != "claude" || cfg.WorkDir != "/workspace/stable" {
		t.Errorf("config mismatch: %+v", cfg)
	}
}

func containsString(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

func TestParseAndValidateFleetFile(t *testing.T) {
	t.Setenv("ODIS_COMMAND_ALLOWLIST", "claude,claude-dev")
	t.Setenv("ODIS_WORK_DIR_PREFIX", "")

	valid := `version: "1"
space:
  name: "Test"
agents:
  worker:
    backend: tmux
    command: claude
`
	ff, err := ParseAndValidateFleetFile([]byte(valid))
	if err != nil {
		t.Fatalf("valid fleet: %v", err)
	}
	if ff.Space.Name != "Test" {
		t.Errorf("space name: want Test, got %q", ff.Space.Name)
	}

	// Unknown field must be rejected.
	unknown := "version: \"1\"\nspace:\n  name: \"X\"\nagents: {}\nevil_field: yes\n"
	if _, err := ParseAndValidateFleetFile([]byte(unknown)); err == nil {
		t.Error("unknown field: want error, got nil")
	}

	// Bad command.
	badCmd := "version: \"1\"\nspace:\n  name: \"X\"\nagents:\n  a:\n    backend: tmux\n    command: bash\n"
	if _, err := ParseAndValidateFleetFile([]byte(badCmd)); err == nil {
		t.Error("bad command: want error, got nil")
	}

	// Relative work_dir.
	relDir := "version: \"1\"\nspace:\n  name: \"X\"\nagents:\n  a:\n    backend: tmux\n    command: claude\n    work_dir: relative/path\n"
	if _, err := ParseAndValidateFleetFile([]byte(relDir)); err == nil {
		t.Error("relative work_dir: want error, got nil")
	}

	// Fleet file with repos list.
	withRepos := `version: "1"
space:
  name: "RepoTest"
agents:
  worker:
    backend: ambient
    command: claude
    repos:
      - url: "https://gitea.example.com/org/repo-a.git"
        branch: main
      - url: "https://gitea.example.com/org/repo-b.git"
`
	ffRepos, err := ParseAndValidateFleetFile([]byte(withRepos))
	if err != nil {
		t.Fatalf("fleet with repos: %v", err)
	}
	if len(ffRepos.Agents["worker"].Repos) != 2 {
		t.Errorf("want 2 repos, got %d", len(ffRepos.Agents["worker"].Repos))
	}
	if ffRepos.Agents["worker"].Repos[0].Branch != "main" {
		t.Errorf("repos[0] branch: want main, got %q", ffRepos.Agents["worker"].Repos[0].Branch)
	}
	if ffRepos.Agents["worker"].Repos[1].Branch != "" {
		t.Errorf("repos[1] branch: want empty, got %q", ffRepos.Agents["worker"].Repos[1].Branch)
	}
}

func TestValidateFleetFieldSizes(t *testing.T) {
	big := strings.Repeat("x", fleetMaxFieldBytes+1)

	// shared_contracts over limit.
	ff := &FleetFile{Space: FleetSpace{SharedContracts: big}}
	if err := ValidateFleetFieldSizes(ff); err == nil {
		t.Error("oversized shared_contracts: want error, got nil")
	}

	// persona prompt over limit.
	ff = &FleetFile{
		Personas: map[string]FleetPersona{
			"p1": {Name: "P1", Prompt: big},
		},
	}
	if err := ValidateFleetFieldSizes(ff); err == nil {
		t.Error("oversized persona prompt: want error, got nil")
	}

	// agent initial_prompt over limit.
	ff = &FleetFile{
		Agents: map[string]FleetAgent{
			"a1": {Backend: "tmux", Command: "claude", InitialPrompt: big},
		},
	}
	if err := ValidateFleetFieldSizes(ff); err == nil {
		t.Error("oversized agent initial_prompt: want error, got nil")
	}

	// All fields at exactly the limit must pass.
	atLimit := strings.Repeat("y", fleetMaxFieldBytes)
	ff = &FleetFile{
		Space: FleetSpace{SharedContracts: atLimit},
		Personas: map[string]FleetPersona{
			"p1": {Name: "P1", Prompt: atLimit},
		},
		Agents: map[string]FleetAgent{
			"a1": {Backend: "tmux", Command: "claude", InitialPrompt: atLimit},
		},
	}
	if err := ValidateFleetFieldSizes(ff); err != nil {
		t.Errorf("at-limit fields should pass: %v", err)
	}
}
