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
	t.Setenv("BOSS_COMMAND_ALLOWLIST", "claude,claude-dev")

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
	t.Setenv("BOSS_COMMAND_ALLOWLIST", "")
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
	t.Setenv("BOSS_WORK_DIR_PREFIX", "")
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
	t.Setenv("BOSS_WORK_DIR_PREFIX", "/workspace")
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
	t.Setenv("BOSS_COMMAND_ALLOWLIST", "claude")

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
	t.Setenv("BOSS_COMMAND_ALLOWLIST", "claude,claude-dev")
	t.Setenv("BOSS_WORK_DIR_PREFIX", "")

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
}
