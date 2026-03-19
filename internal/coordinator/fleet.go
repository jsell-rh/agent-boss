package coordinator

// fleet.go — agent-compose.yaml export/import support (TASK-103).
//
// The agent-compose format is a portable team blueprint: agents, personas,
// and space config. Tasks and runtime state are intentionally excluded.
// Design spec: docs/design-docs/agent-compose.md

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ─── Fleet YAML schema ────────────────────────────────────────────────────────

// FleetFile is the top-level structure for agent-compose.yaml.
type FleetFile struct {
	Version  string                    `yaml:"version"`
	Space    FleetSpace                `yaml:"space"`
	Personas map[string]FleetPersona   `yaml:"personas,omitempty"`
	Agents   map[string]FleetAgent     `yaml:"agents"`
}

// FleetSpace captures space-level metadata.
type FleetSpace struct {
	Name            string `yaml:"name"`
	Description     string `yaml:"description,omitempty"`
	SharedContracts string `yaml:"shared_contracts,omitempty"`
}

// FleetPersona is the inline persona definition in the fleet file.
type FleetPersona struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	Prompt      string `yaml:"prompt"`
}

// FleetRepo is a git repository reference in the fleet file.
type FleetRepo struct {
	URL    string `yaml:"url"`
	Branch string `yaml:"branch,omitempty"`
}

// FleetAgent is one agent's config entry in the fleet file.
type FleetAgent struct {
	Role          string      `yaml:"role,omitempty"`
	Description   string      `yaml:"description,omitempty"`
	Parent        string      `yaml:"parent,omitempty"`
	Personas      []string    `yaml:"personas,omitempty"`
	WorkDir       string      `yaml:"work_dir,omitempty"`
	Backend       string      `yaml:"backend"`    // always explicit for round-trip fidelity
	Command       string      `yaml:"command"`    // always explicit
	InitialPrompt string      `yaml:"initial_prompt,omitempty"`
	RepoURL       string      `yaml:"repo_url,omitempty"` // userinfo stripped on export
	Repos         []FleetRepo `yaml:"repos,omitempty"`    // git repos for ambient sessions
	Model         string      `yaml:"model,omitempty"`
}

// ─── Export handler ───────────────────────────────────────────────────────────

// handleSpaceExport serves GET /spaces/:space/export — returns agent-compose YAML.
// Pure read: holds only RLock; never calls persist. Excludes tasks, tokens,
// session IDs, messages. Strips userinfo from repo_url. Always includes backend
// and command explicitly for round-trip fidelity.
func (s *Server) handleSpaceExport(w http.ResponseWriter, r *http.Request, spaceName string) {
	if r.Method != http.MethodGet {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.RLock()
	ks, ok := s.spaces[spaceName]
	if !ok {
		s.mu.RUnlock()
		writeJSONError(w, "space not found", http.StatusNotFound)
		return
	}

	// Collect personas referenced by agents in this space.
	referencedPersonaIDs := map[string]struct{}{}
	for _, rec := range ks.Agents {
		if rec != nil && rec.Config != nil {
			for _, pRef := range rec.Config.Personas {
				referencedPersonaIDs[pRef.ID] = struct{}{}
			}
		}
	}

	// Build agent map.
	fleetAgents := make(map[string]FleetAgent, len(ks.Agents))
	for name, rec := range ks.Agents {
		if rec == nil {
			continue
		}
		cfg := rec.Config
		if cfg == nil {
			cfg = &AgentConfig{}
		}
		var st *AgentUpdate
		if rec.Status != nil {
			st = rec.Status
		}

		// Persona ID list.
		personaIDs := make([]string, 0, len(cfg.Personas))
		for _, pRef := range cfg.Personas {
			personaIDs = append(personaIDs, pRef.ID)
		}

		// Strip userinfo from repo_url.
		repoURL := cfg.RepoURL
		if repoURL != "" {
			if u, err := url.Parse(repoURL); err == nil {
				u.User = nil
				repoURL = u.String()
			}
		}

		// Convert SessionRepo → FleetRepo (strip userinfo from each URL).
		var fleetRepos []FleetRepo
		for _, sr := range cfg.Repos {
			repoU := sr.URL
			if repoU != "" {
				if u, err := url.Parse(repoU); err == nil {
					u.User = nil
					repoU = u.String()
				}
			}
			fleetRepos = append(fleetRepos, FleetRepo{URL: repoU, Branch: sr.Branch})
		}

		// Determine role/parent from runtime status if not in config.
		role := ""
		parent := ""
		if st != nil {
			role = st.Role
			parent = st.Parent
		}

		// Backend and Command always explicit (for round-trip fidelity).
		backend := cfg.Backend
		if backend == "" {
			backend = "tmux"
		}
		command := cfg.Command
		if command == "" {
			command = "claude"
		}

		fleetAgents[name] = FleetAgent{
			Role:          role,
			Parent:        parent,
			Personas:      personaIDs,
			WorkDir:       cfg.WorkDir,
			Backend:       backend,
			Command:       command,
			InitialPrompt: cfg.InitialPrompt,
			RepoURL:       repoURL,
			Repos:         fleetRepos,
			Model:         cfg.Model,
		}
	}

	// Build shared_contracts from space.
	ff := FleetFile{
		Version: "1",
		Space: FleetSpace{
			Name:            ks.Name,
			SharedContracts: ks.SharedContracts,
		},
		Agents: fleetAgents,
	}

	s.mu.RUnlock()

	// Collect persona content outside the space lock.
	if s.personas != nil && len(referencedPersonaIDs) > 0 {
		fleetPersonas := make(map[string]FleetPersona, len(referencedPersonaIDs))
		for id := range referencedPersonaIDs {
			p := s.personas.get(id)
			if p == nil {
				continue
			}
			fleetPersonas[id] = FleetPersona{
				Name:        p.Name,
				Description: p.Description,
				Prompt:      p.Prompt,
			}
		}
		if len(fleetPersonas) > 0 {
			ff.Personas = fleetPersonas
		}
	}

	out, err := yaml.Marshal(ff)
	if err != nil {
		writeJSONError(w, "failed to marshal YAML", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="fleet.yaml"`)
	w.Write(out)
}

// ─── Security validators ──────────────────────────────────────────────────────

// fleetCommandAllowlist returns the set of allowed launch commands.
// Configured via BOSS_COMMAND_ALLOWLIST (comma-separated). Defaults to
// "claude,claude-dev".
func fleetCommandAllowlist() map[string]struct{} {
	raw := os.Getenv("BOSS_COMMAND_ALLOWLIST")
	if raw == "" {
		raw = "claude,claude-dev"
	}
	set := map[string]struct{}{}
	for _, v := range strings.Split(raw, ",") {
		v = strings.TrimSpace(v)
		if v != "" {
			set[v] = struct{}{}
		}
	}
	return set
}

// ValidateFleetCommand checks that cmd is in the server allowlist.
func ValidateFleetCommand(cmd string) error {
	if cmd == "" {
		return nil // empty means default; accepted
	}
	allowed := fleetCommandAllowlist()
	if _, ok := allowed[cmd]; !ok {
		return fmt.Errorf("command %q is not in the allowlist (BOSS_COMMAND_ALLOWLIST)", cmd)
	}
	return nil
}

const (
	fleetMaxBytes  = 1 << 20  // 1 MB
	fleetMaxAgents = 100
)

// ValidateFleetSize checks file size and agent count against server limits.
// Both the CLI and server call this independently.
func ValidateFleetSize(data []byte, agentCount int) error {
	if len(data) > fleetMaxBytes {
		return fmt.Errorf("fleet file exceeds 1 MB limit (%d bytes)", len(data))
	}
	if agentCount > fleetMaxAgents {
		return fmt.Errorf("fleet file exceeds 100-agent limit (%d agents)", agentCount)
	}
	return nil
}

// ValidateRepoURL checks that a git repo URL is safe for the ambient runner.
// Rules: HTTPS scheme only; no RFC 1918 or link-local targets; no file:// .
func ValidateRepoURL(rawURL string) error {
	if rawURL == "" {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid repo URL: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("repo URL must use https scheme (got %q)", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("repo URL missing host")
	}
	// DNS resolution check: reject RFC 1918 and link-local targets.
	addrs, err := net.LookupHost(host)
	if err != nil {
		// If we can't resolve, reject conservatively.
		return fmt.Errorf("cannot resolve repo URL host %q: %w", host, err)
	}
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil {
			continue
		}
		if isPrivateIP(ip) {
			return fmt.Errorf("repo URL host %q resolves to private/link-local address %s", host, addr)
		}
	}
	return nil
}

// isPrivateIP returns true for RFC 1918, loopback, and link-local addresses.
func isPrivateIP(ip net.IP) bool {
	private := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"::1/128",
		"fc00::/7",
		"fe80::/10",
	}
	for _, cidr := range private {
		_, block, _ := net.ParseCIDR(cidr)
		if block != nil && block.Contains(ip) {
			return true
		}
	}
	return false
}

// ValidateWorkDir checks that a work_dir value is safe.
// Rules: must be absolute; must not contain ".." after cleaning;
// must begin with BOSS_WORK_DIR_PREFIX if that env var is set.
func ValidateWorkDir(workDir string) error {
	if workDir == "" {
		return nil
	}
	if !filepath.IsAbs(workDir) {
		return fmt.Errorf("work_dir must be an absolute path (got %q)", workDir)
	}
	// Reject any path containing ".." components (before or after cleaning).
	// filepath.Clean resolves ".." so we check the raw path first.
	if strings.Contains(workDir, "..") {
		return fmt.Errorf("work_dir contains path traversal (got %q)", workDir)
	}
	cleaned := filepath.Clean(workDir)
	if prefix := os.Getenv("BOSS_WORK_DIR_PREFIX"); prefix != "" {
		// Resolve symlinks before checking the prefix so that a symlink pointing
		// outside the allowed tree doesn't bypass the guard.
		resolved := cleaned
		if r, err := filepath.EvalSymlinks(cleaned); err == nil {
			resolved = r
		}
		if !strings.HasPrefix(resolved, prefix) {
			return fmt.Errorf("work_dir %q is outside the allowed prefix %q", resolved, prefix)
		}
	}
	return nil
}

// ─── Topology ─────────────────────────────────────────────────────────────────

// TopoSortAgents returns agent names in topological order (parents before
// children). Returns an error if a cycle is detected. Agent names are sorted
// alphabetically within each level for deterministic output.
func TopoSortAgents(agents map[string]FleetAgent) ([]string, error) {
	const (
		unvisited = 0
		inStack   = 1
		done      = 2
	)
	state := make(map[string]int, len(agents))
	var order []string

	var visit func(name string) error
	visit = func(name string) error {
		switch state[name] {
		case done:
			return nil
		case inStack:
			return fmt.Errorf("cycle detected involving agent %q", name)
		}
		state[name] = inStack
		// Visit parent first if it is also in the fleet file.
		if a, ok := agents[name]; ok && a.Parent != "" {
			if _, parentInFile := agents[a.Parent]; parentInFile {
				if err := visit(a.Parent); err != nil {
					return err
				}
			}
		}
		state[name] = done
		order = append(order, name)
		return nil
	}

	names := make([]string, 0, len(agents))
	for name := range agents {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if err := visit(name); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// ParseAndValidateFleetFile parses YAML bytes into a FleetFile and runs all
// server-side validations. Returns the parsed file and any error.
func ParseAndValidateFleetFile(data []byte) (*FleetFile, error) {
	// Size check first (before YAML parsing to prevent YAML bomb).
	if len(data) > fleetMaxBytes {
		return nil, fmt.Errorf("fleet file exceeds 1 MB limit")
	}

	var ff FleetFile
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true) // reject unknown fields
	if err := dec.Decode(&ff); err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}

	if err := ValidateFleetSize(data, len(ff.Agents)); err != nil {
		return nil, err
	}

	// Validate each agent's fields.
	for name, agent := range ff.Agents {
		if err := ValidateFleetCommand(agent.Command); err != nil {
			return nil, fmt.Errorf("agent %q: %w", name, err)
		}
		if err := ValidateWorkDir(agent.WorkDir); err != nil {
			return nil, fmt.Errorf("agent %q: %w", name, err)
		}
		// Note: repo URL DNS validation is expensive and skipped at parse time;
		// it runs at spawn time in the ambient backend.
	}

	return &ff, nil
}
