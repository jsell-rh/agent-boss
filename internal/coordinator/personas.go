package coordinator

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// personasFile is the filename for global persona storage in DATA_DIR.
const personasFile = "personas.json"

// PersonaStore manages global personas persisted to DATA_DIR/personas.json.
type PersonaStore struct {
	mu      sync.RWMutex
	dataDir string
	data    map[string]*Persona // keyed by persona ID
}

func newPersonaStore(dataDir string) *PersonaStore {
	ps := &PersonaStore{
		dataDir: dataDir,
		data:    make(map[string]*Persona),
	}
	_ = ps.load()
	return ps
}

func (ps *PersonaStore) path() string {
	return filepath.Join(ps.dataDir, personasFile)
}

func (ps *PersonaStore) load() error {
	data, err := os.ReadFile(ps.path())
	if err != nil {
		if os.IsNotExist(err) {
			return nil // first run, no file yet
		}
		return err
	}
	var personas []*Persona
	if err := json.Unmarshal(data, &personas); err != nil {
		return err
	}
	ps.data = make(map[string]*Persona, len(personas))
	for _, p := range personas {
		ps.data[p.ID] = p
	}
	return nil
}

func (ps *PersonaStore) save() error {
	personas := make([]*Persona, 0, len(ps.data))
	for _, p := range ps.data {
		personas = append(personas, p)
	}
	data, err := json.MarshalIndent(personas, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(ps.path(), data, 0644)
}

func (ps *PersonaStore) list() []*Persona {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	out := make([]*Persona, 0, len(ps.data))
	for _, p := range ps.data {
		copy := *p
		out = append(out, &copy)
	}
	return out
}

func (ps *PersonaStore) get(id string) *Persona {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	p := ps.data[id]
	if p == nil {
		return nil
	}
	copy := *p
	return &copy
}

func (ps *PersonaStore) create(p *Persona) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if _, exists := ps.data[p.ID]; exists {
		return fmt.Errorf("persona %q already exists", p.ID)
	}
	ps.data[p.ID] = p
	return ps.save()
}

func (ps *PersonaStore) update(id string, fn func(*Persona)) (*Persona, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	p := ps.data[id]
	if p == nil {
		return nil, fmt.Errorf("persona %q not found", id)
	}
	fn(p)
	p.Version++
	p.UpdatedAt = time.Now().UTC()
	if err := ps.save(); err != nil {
		return nil, err
	}
	copy := *p
	return &copy, nil
}

func (ps *PersonaStore) delete(id string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if _, exists := ps.data[id]; !exists {
		return fmt.Errorf("persona %q not found", id)
	}
	delete(ps.data, id)
	return ps.save()
}

// currentVersion returns the current version of persona id, or 0 if not found.
func (ps *PersonaStore) currentVersion(id string) int {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	if p := ps.data[id]; p != nil {
		return p.Version
	}
	return 0
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
			writeJSONError(w, "id is required", http.StatusBadRequest)
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
