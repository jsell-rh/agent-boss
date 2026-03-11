package coordinator

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// handleRoot serves the Vue SPA index.html for all non-API routes.
// Since this handler is registered as "/" (catch-all in Go's ServeMux),
// it receives every request that doesn't match a more specific pattern
// (i.e., every path that isn't /spaces/, /events, /assets/, etc.).
// Serving index.html for all such paths lets Vue Router handle client-side
// navigation, enabling deep-linking to URLs like /SpaceName or /SpaceName/kanban.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	// Serve Vue SPA index.html: runtime FRONTEND_DIR takes priority, then embedded.
	if s.frontendDir != "" {
		indexPath := filepath.Join(s.frontendDir, "index.html")
		if _, err := os.Stat(indexPath); err == nil {
			content, err := os.ReadFile(indexPath)
			if err != nil {
				writeJSONError(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(content)
			return
		}
	}
	// Try embedded frontend (compiled Vue dist).
	if content, err := embeddedFrontend.ReadFile("frontend/index.html"); err == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(content)
		return
	}
	writeJSONError(w, "frontend not available", http.StatusNotFound)
}

func (s *Server) handleListSpaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type spaceSummary struct {
		Name           string    `json:"name"`
		AgentCount     int       `json:"agent_count"`
		AttentionCount int       `json:"attention_count"`
		Archive        string    `json:"archive,omitempty"`
		CreatedAt      time.Time `json:"created_at"`
		UpdatedAt      time.Time `json:"updated_at"`
	}

	s.mu.RLock()
	summaries := make([]spaceSummary, 0, len(s.spaces))
	for _, ks := range s.spaces {
		attention := 0
		for _, rec := range ks.Agents {
			if rec != nil && rec.Status != nil {
				attention += len(rec.Status.Questions) + len(rec.Status.Blockers)
			}
		}
		summaries = append(summaries, spaceSummary{
			Name:           ks.Name,
			AgentCount:     len(ks.Agents),
			AttentionCount: attention,
			Archive:        ks.Archive,
			CreatedAt:      ks.CreatedAt,
			UpdatedAt:      ks.UpdatedAt,
		})
	}
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(summaries)
}

func (s *Server) handleSpaceRoute(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/spaces/")
	parts := strings.Split(path, "/")

	spaceName := parts[0]
	if spaceName == "" {
		s.handleListSpaces(w, r)
		return
	}

	if len(parts) == 1 || (len(parts) == 2 && parts[1] == "") {
		if r.Method == http.MethodDelete {
			s.handleDeleteSpace(w, r, spaceName)
			return
		}
		s.handleSpaceView(w, r, spaceName)
		return
	}

	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}

	subRoute := parts[1]

	switch subRoute {
	case "events":
		s.handleSpaceSSE(w, r, spaceName)
	case "raw":
		s.handleSpaceRaw(w, r, spaceName)
	case "contracts":
		if len(parts) >= 3 && strings.TrimRight(parts[2], "/") == "default" {
			s.handleSpaceContractsDefault(w, r, spaceName)
			return
		}
		s.handleSpaceContracts(w, r, spaceName)
	case "archive":
		s.handleSpaceArchive(w, r, spaceName)
	case "agent":
		if len(parts) < 3 {
			writeJSONError(w, "missing agent name", http.StatusBadRequest)
			return
		}
		agentName := parts[2]
		if len(parts) >= 4 {
			// Handle sub-routes: /spaces/{space}/agent/{agent}/{action}
			action := strings.TrimRight(parts[3], "/")
			switch action {
			case "message":
				// /spaces/{space}/agent/{agent}/message — send message
				// /spaces/{space}/agent/{agent}/message/{id}/ack — ack message
				if len(parts) >= 6 && strings.TrimRight(parts[5], "/") == "ack" {
					msgID := strings.TrimRight(parts[4], "/")
					s.handleMessageAck(w, r, spaceName, agentName, msgID)
				} else {
					s.handleAgentMessage(w, r, spaceName, agentName)
				}
			case "register":
				s.handleAgentRegister(w, r, spaceName, agentName)
			case "heartbeat":
				s.handleAgentHeartbeat(w, r, spaceName, agentName)
			case "messages":
				s.handleAgentMessages(w, r, spaceName, agentName)
			case "events":
				s.handleAgentSSE(w, r, spaceName, agentName)
			case "spawn":
				s.handleAgentSpawn(w, r, spaceName, agentName)
			case "stop":
				s.handleAgentStop(w, r, spaceName, agentName)
			case "interrupt":
				s.handleAgentInterrupt(w, r, spaceName, agentName)
			case "restart":
				s.handleAgentRestart(w, r, spaceName, agentName)
			case "introspect":
				s.handleAgentIntrospect(w, r, spaceName, agentName)
			case "history":
				s.handleAgentHistory(w, r, spaceName, agentName)
			case "config":
				s.handleAgentConfig(w, r, spaceName, agentName)
			case "duplicate":
				s.handleAgentDuplicate(w, r, spaceName, agentName)
			default:
				// Handle document path: /spaces/{space}/agent/{agent}/{slug}
				s.handleAgentDocument(w, r, spaceName, agentName, action)
			}
		} else {
			// Handle agent updates: /spaces/{space}/agent/{agent}
			agentName = strings.TrimRight(agentName, "/")
			s.handleSpaceAgent(w, r, spaceName, agentName)
		}
	case "api":
		if len(parts) == 3 {
			switch strings.TrimRight(parts[2], "/") {
			case "agents":
				s.handleSpaceAgentsJSON(w, r, spaceName)
			case "events":
				s.handleSpaceEventsJSON(w, r)
			case "session-status":
				s.handleSpaceSessionStatus(w, r, spaceName)
			default:
				http.NotFound(w, r)
			}
		} else {
			http.NotFound(w, r)
		}
	case "tasks":
		rest := ""
		if len(parts) >= 3 {
			rest = strings.TrimRight(strings.Join(parts[2:], "/"), "/")
		}
		s.handleSpaceTasks(w, r, spaceName, rest)
	case "agents":
		s.handleCreateAgents(w, r, spaceName)
	case "hierarchy":
		s.handleSpaceHierarchy(w, r, spaceName)
	case "history":
		s.handleSpaceHistory(w, r, spaceName)
	case "ignition":
		agentName := ""
		if len(parts) == 3 {
			agentName = strings.TrimRight(parts[2], "/")
		}
		s.handleIgnition(w, r, spaceName, agentName)
	case "approve":
		if len(parts) == 3 {
			agentName := strings.TrimRight(parts[2], "/")
			s.handleApproveAgent(w, r, spaceName, agentName)
		} else {
			writeJSONError(w, "agent name required", http.StatusBadRequest)
		}
	case "broadcast":
		if len(parts) == 3 {
			agentName := strings.TrimRight(parts[2], "/")
			s.handleSingleBroadcast(w, r, spaceName, agentName)
		} else {
			s.handleBroadcast(w, r, spaceName)
		}
	case "reply":
		if len(parts) == 3 {
			agentName := strings.TrimRight(parts[2], "/")
			s.handleReplyAgent(w, r, spaceName, agentName)
		} else {
			writeJSONError(w, "agent name required", http.StatusBadRequest)
		}
	case "dismiss":
		if len(parts) == 3 {
			agentName := strings.TrimRight(parts[2], "/")
			s.handleDismissQuestion(w, r, spaceName, agentName)
		} else {
			writeJSONError(w, "agent name required", http.StatusBadRequest)
		}
	case "restart-all":
		s.handleRestartAll(w, r, spaceName)
	case "factory":
		factorySub := ""
		if len(parts) == 3 {
			factorySub = strings.TrimRight(parts[2], "/")
		}
		switch factorySub {
		case "", "interrupts":
			s.handleInterrupts(w, r, spaceName)
		case "metrics":
			s.handleInterruptMetrics(w, r, spaceName)
		default:
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleSpaceView(w http.ResponseWriter, r *http.Request, spaceName string) {
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		s.handleSpaceJSON(w, r, spaceName)
		return
	}
	// Serve Vue SPA — the frontend router handles /spaces/{space} client-side.
	s.handleRoot(w, r)
}

func (s *Server) handleSpaceJSON(w http.ResponseWriter, r *http.Request, spaceName string) {
	if r.Method == http.MethodDelete {
		s.handleDeleteSpace(w, r, spaceName)
		return
	}
	if r.Method != http.MethodGet {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ks, ok := s.getSpace(spaceName)
	if !ok {
		writeJSONError(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ks)
}

func (s *Server) handleDeleteSpace(w http.ResponseWriter, _ *http.Request, spaceName string) {
	s.mu.Lock()
	_, ok := s.spaces[spaceName]
	if !ok {
		s.mu.Unlock()
		writeJSONError(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}
	delete(s.spaces, spaceName)
	s.mu.Unlock()

	os.Remove(s.spacePath(spaceName))
	os.Remove(s.spaceMarkdownPath(spaceName))
	os.Remove(filepath.Join(s.dataDir, spaceName+".events.jsonl"))
	s.deleteSpaceFromDB(spaceName)

	s.logEvent(fmt.Sprintf("space %q deleted", spaceName))
	s.broadcastSSE(spaceName, "", "space_deleted", spaceName)
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "deleted space %q", spaceName)
}

func (s *Server) handleSpaceHierarchy(w http.ResponseWriter, r *http.Request, spaceName string) {
	if r.Method != http.MethodGet {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ks, ok := s.getSpace(spaceName)
	if !ok {
		writeJSONError(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}
	s.mu.RLock()
	tree := BuildHierarchyTree(ks)
	s.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tree)
}

// textFieldConfig describes a single text-field endpoint on a KnowledgeSpace.
// Used by handleSpaceTextField to avoid duplicating the raw/contracts/archive handlers.
type textFieldConfig struct {
	getField    func(*KnowledgeSpace) string
	setField    func(*KnowledgeSpace, string)
	logLabel    string
	journalType SpaceEventType
}

// handleSpaceTextField is a generic GET/POST handler for KnowledgeSpace text fields
// (SharedContracts, Archive). GET returns the field as text/plain; POST replaces it.
func (s *Server) handleSpaceTextField(w http.ResponseWriter, r *http.Request, spaceName string, cfg textFieldConfig) {
	switch r.Method {
	case http.MethodGet:
		ks, ok := s.getSpace(spaceName)
		if !ok {
			writeJSONError(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, cfg.getField(ks))

	case http.MethodPost:
		defer r.Body.Close()
		body, err := io.ReadAll(io.LimitReader(r.Body, MaxBodySize))
		if err != nil {
			writeJSONError(w, fmt.Sprintf("read body: %v", err), http.StatusBadRequest)
			return
		}

		s.mu.Lock()
		ks := s.getOrCreateSpaceLocked(spaceName)
		cfg.setField(ks, sanitizeInput(string(body)))
		ks.UpdatedAt = time.Now().UTC()
		snap := ks.snapshot()
		s.mu.Unlock()

		if err := s.saveSpace(snap); err != nil {
			writeJSONError(w, fmt.Sprintf("save: %v", err), http.StatusInternalServerError)
			return
		}
		s.logEvent(fmt.Sprintf("[%s] %s updated (%d bytes)", spaceName, cfg.logLabel, len(body)))
		s.journal.Append(spaceName, cfg.journalType, "", map[string]string{"content": sanitizeInput(string(body))})
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")

	default:
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleSpaceRaw serves GET /spaces/{space}/raw (rendered markdown of full space state).
// POST /spaces/{space}/raw is preserved as a backward-compatible alias for
// POST /spaces/{space}/contracts — both write to SharedContracts.
// /contracts is the canonical write endpoint; /raw POST exists for legacy callers.
func (s *Server) handleSpaceRaw(w http.ResponseWriter, r *http.Request, spaceName string) {
	if r.Method == http.MethodGet {
		ks, ok := s.getSpace(spaceName)
		if !ok {
			writeJSONError(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
			return
		}
		s.mu.RLock()
		md := ks.RenderMarkdown()
		s.mu.RUnlock()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, md)
		return
	}
	// POST: alias for /contracts (writes SharedContracts).
	s.handleSpaceTextField(w, r, spaceName, textFieldConfig{
		getField:    func(ks *KnowledgeSpace) string { return ks.SharedContracts },
		setField:    func(ks *KnowledgeSpace, v string) { ks.SharedContracts = v },
		logLabel:    "shared contracts",
		journalType: EventContractsUpdated,
	})
}

func (s *Server) handleSpaceContracts(w http.ResponseWriter, r *http.Request, spaceName string) {
	s.handleSpaceTextField(w, r, spaceName, textFieldConfig{
		getField:    func(ks *KnowledgeSpace) string { return ks.SharedContracts },
		setField:    func(ks *KnowledgeSpace, v string) { ks.SharedContracts = v },
		logLabel:    "contracts",
		journalType: EventContractsUpdated,
	})
}

// handleSpaceContractsDefault serves GET /spaces/{space}/contracts/default —
// returns the embedded protocol.md template with {SPACE} substituted.
// This lets the frontend compare current contracts to the default and offer a reset.
func (s *Server) handleSpaceContractsDefault(w http.ResponseWriter, r *http.Request, spaceName string) {
	if r.Method != http.MethodGet {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if protocolTemplate == "" {
		writeJSONError(w, "protocol template not available", http.StatusInternalServerError)
		return
	}
	defaultContracts := strings.ReplaceAll(protocolTemplate, "{SPACE}", spaceName)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, defaultContracts)
}

func (s *Server) handleSpaceArchive(w http.ResponseWriter, r *http.Request, spaceName string) {
	s.handleSpaceTextField(w, r, spaceName, textFieldConfig{
		getField:    func(ks *KnowledgeSpace) string { return ks.Archive },
		setField:    func(ks *KnowledgeSpace, v string) { ks.Archive = v },
		logLabel:    "archive",
		journalType: EventArchiveUpdated,
	})
}
