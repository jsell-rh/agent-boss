package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	bossdb "github.com/ambient/platform/components/boss/internal/coordinator/db"
)

// slugRe matches any character that is not a letter, digit, or hyphen.
var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// slugify converts a name to a URL-safe identifier (lowercase, hyphens).
func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = slugRe.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// PersonaStore manages global personas persisted to SQLite.
type PersonaStore struct {
	repo *bossdb.Repository
}

func newPersonaStore(repo *bossdb.Repository) *PersonaStore {
	return &PersonaStore{repo: repo}
}

func (ps *PersonaStore) list() []*Persona {
	rows, err := ps.repo.ListPersonas()
	if err != nil {
		return nil
	}
	out := make([]*Persona, len(rows))
	for i, r := range rows {
		out[i] = personaFromRow(r)
	}
	return out
}

func (ps *PersonaStore) get(id string) *Persona {
	row, err := ps.repo.GetPersona(id)
	if err != nil || row == nil {
		return nil
	}
	return personaFromRow(row)
}

func (ps *PersonaStore) create(p *Persona) error {
	exists, err := ps.repo.PersonaExists(p.ID)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("persona %q already exists", p.ID)
	}
	return ps.repo.CreatePersona(personaToRow(p))
}

func (ps *PersonaStore) update(id string, fn func(*Persona)) (*Persona, error) {
	row, err := ps.repo.GetPersona(id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, fmt.Errorf("persona %q not found", id)
	}
	// Save current state as a version snapshot before applying changes.
	if err := ps.repo.SavePersonaVersion(&bossdb.PersonaVersionRow{
		PersonaID: id,
		Version:   row.Version,
		Prompt:    row.Prompt,
		UpdatedAt: row.UpdatedAt,
	}); err != nil {
		return nil, err
	}
	p := personaFromRow(row)
	fn(p)
	p.Version = row.Version + 1
	p.UpdatedAt = time.Now().UTC()
	if err := ps.repo.SavePersona(personaToRow(p)); err != nil {
		return nil, err
	}
	return p, nil
}

// history returns the version history for a persona (past versions + current).
func (ps *PersonaStore) history(id string) ([]PersonaVersion, error) {
	row, err := ps.repo.GetPersona(id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, fmt.Errorf("persona %q not found", id)
	}
	vrows, err := ps.repo.GetPersonaVersions(id)
	if err != nil {
		return nil, err
	}
	all := make([]PersonaVersion, 0, len(vrows)+1)
	for _, v := range vrows {
		all = append(all, PersonaVersion{Version: v.Version, Prompt: v.Prompt, UpdatedAt: v.UpdatedAt})
	}
	// Append current version.
	all = append(all, PersonaVersion{Version: row.Version, Prompt: row.Prompt, UpdatedAt: row.UpdatedAt})
	return all, nil
}

// revert restores a persona to a previous version's prompt, creating a new version.
func (ps *PersonaStore) revert(id string, targetVersion int) (*Persona, error) {
	row, err := ps.repo.GetPersona(id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, fmt.Errorf("persona %q not found", id)
	}
	vrows, err := ps.repo.GetPersonaVersions(id)
	if err != nil {
		return nil, err
	}
	var targetPrompt string
	found := false
	for _, v := range vrows {
		if v.Version == targetVersion {
			targetPrompt = v.Prompt
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("version %d not found in history", targetVersion)
	}
	// Snapshot current before reverting.
	if err := ps.repo.SavePersonaVersion(&bossdb.PersonaVersionRow{
		PersonaID: id,
		Version:   row.Version,
		Prompt:    row.Prompt,
		UpdatedAt: row.UpdatedAt,
	}); err != nil {
		return nil, err
	}
	row.Prompt = targetPrompt
	row.Version++
	row.UpdatedAt = time.Now().UTC()
	if err := ps.repo.SavePersona(row); err != nil {
		return nil, err
	}
	return personaFromRow(row), nil
}

func (ps *PersonaStore) delete(id string) error {
	exists, err := ps.repo.PersonaExists(id)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("persona %q not found", id)
	}
	return ps.repo.DeletePersona(id)
}

// currentVersion returns the current version of persona id, or 0 if not found.
func (ps *PersonaStore) currentVersion(id string) int {
	row, err := ps.repo.GetPersona(id)
	if err != nil || row == nil {
		return 0
	}
	return row.Version
}

// personaFromRow converts a DB row to a coordinator Persona.
func personaFromRow(r *bossdb.PersonaRow) *Persona {
	return &Persona{
		ID:          r.ID,
		Name:        r.Name,
		Description: r.Description,
		Prompt:      r.Prompt,
		Version:     r.Version,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
}

// personaToRow converts a coordinator Persona to a DB row.
func personaToRow(p *Persona) *bossdb.PersonaRow {
	return &bossdb.PersonaRow{
		ID:          p.ID,
		Name:        p.Name,
		Description: p.Description,
		Prompt:      p.Prompt,
		Version:     p.Version,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
	}
}

// assemblePersonaPrompt builds the combined persona prompt text for a set of PersonaRefs.
// Returns empty string if no personas or none are found.
func (s *Server) assemblePersonaPrompt(refs []PersonaRef) string {
	if s.personas == nil || len(refs) == 0 {
		return ""
	}
	var parts []string
	for _, ref := range refs {
		if p := s.personas.get(ref.ID); p != nil && p.Prompt != "" {
			parts = append(parts, p.Prompt)
		}
	}
	return strings.Join(parts, "\n\n")
}

// resolvePersonaRefs resolves persona IDs to PersonaRefs with current pinned versions.
func (s *Server) resolvePersonaRefs(refs []PersonaRef) []PersonaRef {
	if s.personas == nil {
		return refs
	}
	out := make([]PersonaRef, len(refs))
	for i, ref := range refs {
		out[i] = PersonaRef{
			ID:            ref.ID,
			PinnedVersion: s.personas.currentVersion(ref.ID),
		}
	}
	return out
}

// --- HTTP handlers ---

// handlePersonaList handles GET/POST /personas.
func (s *Server) handlePersonaList(w http.ResponseWriter, r *http.Request) {
	if s.personas == nil {
		writeJSONError(w, "persona store not initialized", http.StatusInternalServerError)
		return
	}
	switch r.Method {
	case http.MethodGet:
		// Browser navigation sends Accept: text/html; API calls send Accept: application/json or */*
		if strings.Contains(r.Header.Get("Accept"), "text/html") {
			s.handleRoot(w, r)
			return
		}
		personas := s.personas.list()
		// Annotate with spaces_used info
		type personaWithUsage struct {
			*Persona
			SpacesUsed []string `json:"spaces_used,omitempty"`
		}
		results := make([]personaWithUsage, len(personas))
		s.mu.RLock()
		for i, p := range personas {
			var spacesUsed []string
			for spaceName, ks := range s.spaces {
				for _, rec := range ks.Agents {
					if rec == nil || rec.Config == nil {
						continue
					}
					for _, ref := range rec.Config.Personas {
						if ref.ID == p.ID {
							spacesUsed = append(spacesUsed, spaceName)
							break
						}
					}
				}
			}
			results[i] = personaWithUsage{Persona: p, SpacesUsed: dedup(spacesUsed)}
		}
		s.mu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(results)

	case http.MethodPost:
		var p Persona
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			writeJSONError(w, "invalid json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(p.ID) == "" {
			p.ID = slugify(p.Name)
		}
		if strings.TrimSpace(p.ID) == "" {
			writeJSONError(w, "name is required", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(p.Name) == "" {
			writeJSONError(w, "name is required", http.StatusBadRequest)
			return
		}
		now := time.Now().UTC()
		p.Version = 1
		p.CreatedAt = now
		p.UpdatedAt = now
		if err := s.personas.create(&p); err != nil {
			writeJSONError(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(p)

	default:
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handlePersonaDetail handles GET/PUT/PATCH/DELETE /personas/{id}.
func (s *Server) handlePersonaDetail(w http.ResponseWriter, r *http.Request, personaID string) {
	if s.personas == nil {
		writeJSONError(w, "persona store not initialized", http.StatusInternalServerError)
		return
	}
	switch r.Method {
	case http.MethodGet:
		p := s.personas.get(personaID)
		if p == nil {
			writeJSONError(w, fmt.Sprintf("persona %q not found", personaID), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(p)

	case http.MethodPut, http.MethodPatch:
		var patch struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Prompt      string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			writeJSONError(w, "invalid json: "+err.Error(), http.StatusBadRequest)
			return
		}
		updated, err := s.personas.update(personaID, func(p *Persona) {
			if patch.Name != "" {
				p.Name = patch.Name
			}
			if patch.Description != "" {
				p.Description = patch.Description
			}
			if patch.Prompt != "" {
				p.Prompt = patch.Prompt
			}
		})
		if err != nil {
			writeJSONError(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(updated)

	case http.MethodDelete:
		// Check if persona is assigned to any agent
		s.mu.RLock()
		var assignedAgents []string
		for spaceName, ks := range s.spaces {
			for agentName, rec := range ks.Agents {
				if rec == nil || rec.Config == nil {
					continue
				}
				for _, ref := range rec.Config.Personas {
					if ref.ID == personaID {
						assignedAgents = append(assignedAgents, spaceName+"/"+agentName)
						break
					}
				}
			}
		}
		s.mu.RUnlock()
		if len(assignedAgents) > 0 {
			writeJSONError(w, fmt.Sprintf("persona assigned to %d agent(s): %s — remove assignments first",
				len(assignedAgents), strings.Join(assignedAgents, ", ")), http.StatusConflict)
			return
		}
		if err := s.personas.delete(personaID); err != nil {
			writeJSONError(w, err.Error(), http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handlePersonaHistory handles GET /personas/{id}/history.
func (s *Server) handlePersonaHistory(w http.ResponseWriter, r *http.Request, personaID string) {
	if r.Method != http.MethodGet {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	versions, err := s.personas.history(personaID)
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(versions)
}

// handlePersonaRevert handles POST /personas/{id}/revert.
func (s *Server) handlePersonaRevert(w http.ResponseWriter, r *http.Request, personaID string) {
	if r.Method != http.MethodPost {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Version int `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	updated, err := s.personas.revert(personaID, req.Version)
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(updated)
}

// personaAgentInfo describes an agent using a persona, with outdated status.
type personaAgentInfo struct {
	Space          string `json:"space"`
	Agent          string `json:"agent"`
	PinnedVersion  int    `json:"pinned_version"`
	CurrentVersion int    `json:"current_version"`
	Outdated       bool   `json:"outdated"`
	SessionID      string `json:"session_id,omitempty"`
}

// handlePersonaAgents handles GET /personas/{id}/agents.
func (s *Server) handlePersonaAgents(w http.ResponseWriter, r *http.Request, personaID string) {
	if r.Method != http.MethodGet {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p := s.personas.get(personaID)
	if p == nil {
		writeJSONError(w, fmt.Sprintf("persona %q not found", personaID), http.StatusNotFound)
		return
	}

	var agents []personaAgentInfo
	s.mu.RLock()
	for spaceName, ks := range s.spaces {
		for agentName, rec := range ks.Agents {
			if rec == nil || rec.Config == nil {
				continue
			}
			for _, ref := range rec.Config.Personas {
				if ref.ID == personaID {
					agents = append(agents, personaAgentInfo{
						Space:          spaceName,
						Agent:          agentName,
						PinnedVersion:  ref.PinnedVersion,
						CurrentVersion: p.Version,
						Outdated:       ref.PinnedVersion < p.Version,
						SessionID:      rec.Status.SessionID,
					})
					break
				}
			}
		}
	}
	s.mu.RUnlock()

	if agents == nil {
		agents = []personaAgentInfo{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(agents)
}

// handlePersonaRestartOutdated handles POST /personas/{id}/restart-outdated.
func (s *Server) handlePersonaRestartOutdated(w http.ResponseWriter, r *http.Request, personaID string) {
	if r.Method != http.MethodPost {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p := s.personas.get(personaID)
	if p == nil {
		writeJSONError(w, fmt.Sprintf("persona %q not found", personaID), http.StatusNotFound)
		return
	}

	type restartTarget struct {
		Space     string
		Agent     string
		SessionID string
		Backend   string
	}
	var targets []restartTarget

	s.mu.RLock()
	for spaceName, ks := range s.spaces {
		for agentName, rec := range ks.Agents {
			if rec == nil || rec.Config == nil || rec.Status == nil {
				continue
			}
			for _, ref := range rec.Config.Personas {
				if ref.ID == personaID && ref.PinnedVersion < p.Version {
					targets = append(targets, restartTarget{
						Space:     spaceName,
						Agent:     agentName,
						SessionID: rec.Status.SessionID,
						Backend:   rec.Status.BackendType,
					})
					break
				}
			}
		}
	}
	s.mu.RUnlock()

	var restarted []string
	var errors []string
	for _, t := range targets {
		if t.SessionID == "" {
			errors = append(errors, fmt.Sprintf("%s/%s: no session", t.Space, t.Agent))
			continue
		}
		backend, err := s.backendByName(t.Backend)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s/%s: %v", t.Space, t.Agent, err))
			continue
		}
		// Re-pin persona versions before restart.
		s.mu.Lock()
		if ks, ok := s.spaces[t.Space]; ok {
			if rec, ok := ks.Agents[t.Agent]; ok && rec.Config != nil {
				rec.Config.Personas = s.resolvePersonaRefs(rec.Config.Personas)
				s.saveSpace(ks)
			}
		}
		s.mu.Unlock()

		// Trigger restart via the lifecycle handler logic.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := backend.KillSession(ctx, t.SessionID); err != nil {
			errors = append(errors, fmt.Sprintf("%s/%s: kill failed: %v", t.Space, t.Agent, err))
			cancel()
			continue
		}
		cancel()
		restarted = append(restarted, t.Space+"/"+t.Agent)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"restarted": restarted,
		"errors":    errors,
		"total":     len(targets),
	})
}

// dedup removes duplicate strings from a slice.
func dedup(s []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
