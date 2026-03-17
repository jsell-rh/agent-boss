package coordinator

import (
	"encoding/json"
	"net/http"
	"sort"
)

// fleetExport is the JSON payload returned by GET /spaces/:space/export.
// It is YAML-serializable (no runtime fields like tokens or session IDs).
type fleetExport struct {
	Version  string                  `json:"version"`
	Space    fleetSpaceExport        `json:"space"`
	Personas map[string]fleetPersona `json:"personas,omitempty"`
	Agents   map[string]fleetAgent   `json:"agents,omitempty"`
}

type fleetSpaceExport struct {
	Name            string `json:"name"`
	SharedContracts string `json:"shared_contracts,omitempty"`
}

type fleetPersona struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Prompt      string `json:"prompt"`
}

type fleetAgent struct {
	Role          string   `json:"role,omitempty"`
	Parent        string   `json:"parent,omitempty"`
	Personas      []string `json:"personas,omitempty"`
	WorkDir       string   `json:"work_dir,omitempty"`
	Backend       string   `json:"backend,omitempty"`
	Command       string   `json:"command,omitempty"`
	InitialPrompt string   `json:"initial_prompt,omitempty"`
	RepoURL       string   `json:"repo_url,omitempty"`
	Model         string   `json:"model,omitempty"`
}

// handleSpaceExport serves GET /spaces/:space/export.
// Returns a fleet blueprint JSON payload (no tokens, no session IDs).
// The frontend converts this to YAML for download via js-yaml.
func (s *Server) handleSpaceExport(w http.ResponseWriter, r *http.Request, spaceName string) {
	if r.Method != http.MethodGet {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.RLock()
	ks, ok := s.spaces[spaceName]
	var snap *KnowledgeSpace
	if ok {
		snap = ks.snapshot()
	}
	s.mu.RUnlock()
	if !ok {
		writeJSONError(w, "space not found", http.StatusNotFound)
		return
	}

	// Collect persona IDs referenced by agents in this space.
	referencedPersonaIDs := map[string]struct{}{}
	for _, rec := range snap.Agents {
		if rec.Config != nil {
			for _, ref := range rec.Config.Personas {
				referencedPersonaIDs[ref.ID] = struct{}{}
			}
		}
	}

	personas := map[string]fleetPersona{}
	for id := range referencedPersonaIDs {
		p := s.personas.get(id)
		if p == nil {
			continue
		}
		fp := fleetPersona{Name: p.Name, Prompt: p.Prompt}
		if p.Description != "" {
			fp.Description = p.Description
		}
		personas[id] = fp
	}

	// Build agents map in deterministic order.
	agentNames := make([]string, 0, len(snap.Agents))
	for name := range snap.Agents {
		agentNames = append(agentNames, name)
	}
	sort.Strings(agentNames)

	agents := map[string]fleetAgent{}
	for _, name := range agentNames {
		rec := snap.Agents[name]
		fa := fleetAgent{}
		if rec.Status != nil {
			fa.Role = rec.Status.Role
			fa.Parent = rec.Status.Parent
		}
		if rec.Config != nil {
			fa.WorkDir = rec.Config.WorkDir
			fa.InitialPrompt = rec.Config.InitialPrompt
			fa.Backend = rec.Config.Backend
			fa.Command = rec.Config.Command
			fa.RepoURL = rec.Config.RepoURL
			fa.Model = rec.Config.Model
			if len(rec.Config.Personas) > 0 {
				ids := make([]string, len(rec.Config.Personas))
				for i, ref := range rec.Config.Personas {
					ids[i] = ref.ID
				}
				fa.Personas = ids
			}
		}
		agents[name] = fa
	}

	payload := fleetExport{
		Version: "1",
		Space: fleetSpaceExport{
			Name:            snap.Name,
			SharedContracts: snap.SharedContracts,
		},
	}
	if len(personas) > 0 {
		payload.Personas = personas
	}
	if len(agents) > 0 {
		payload.Agents = agents
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}
