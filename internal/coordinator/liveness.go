package coordinator

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

func (s *Server) livenessLoop() {
	ticker := time.NewTicker(1 * time.Second)
	staleTicker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	defer staleTicker.Stop()
	heartbeatTicker := time.NewTicker(10 * time.Second)
	defer heartbeatTicker.Stop()
	for {
		select {
		case <-s.stopLiveness:
			return
		case <-ticker.C:
			s.checkAllSessionLiveness()
		case <-staleTicker.C:
			s.checkStaleness()
		case <-heartbeatTicker.C:
			s.checkHeartbeatStaleness()
		}
	}
}

func (s *Server) checkAllSessionLiveness() {
	s.mu.RLock()
	type probe struct {
		space, agent, session, backendType string
	}
	var probes []probe
	for spaceName, ks := range s.spaces {
		for name, rec := range ks.Agents {
			if rec == nil || rec.Status == nil { continue }
			agent := rec.Status
			if agent.SessionID != "" {
				probes = append(probes, probe{spaceName, name, agent.SessionID, agent.BackendType})
			}
		}
	}
	s.mu.RUnlock()

	type statusEntry struct {
		agent, session   string
		exists, idle     bool
		needsApproval    bool
		toolName, prompt string
	}
	spaceResults := make(map[string][]statusEntry)
	for _, p := range probes {
		backend := s.backendByName(p.backendType)
		if !backend.Available() {
			continue
		}
		e := statusEntry{agent: p.agent, session: p.session}
		e.exists = backend.SessionExists(p.session)
		if e.exists {
			e.idle = backend.IsIdle(p.session)
			if !e.idle {
				approval := backend.CheckApproval(p.session)
				e.needsApproval = approval.NeedsApproval
				e.toolName = approval.ToolName
				e.prompt = approval.PromptText
			}
		}
		spaceResults[p.space] = append(spaceResults[p.space], e)
	}

	for space, entries := range spaceResults {
		payload := make([]map[string]interface{}, len(entries))
		for i, e := range entries {
			key := space + "/" + e.agent
			if e.needsApproval {
				if _, already := s.approvalTracked[key]; !already {
					ctx := map[string]string{}
					if e.toolName != "" {
						ctx["tool"] = e.toolName
					}
					s.interrupts.Record(space, e.agent, InterruptApproval, e.prompt, ctx)
					s.approvalTracked[key] = time.Now()
					s.logEvent(fmt.Sprintf("[%s/%s] approval interrupt detected (tool: %s)", space, e.agent, e.toolName))
				}
			} else {
				if started, was := s.approvalTracked[key]; was {
					delete(s.approvalTracked, key)
					waitSec := time.Since(started).Seconds()
					s.interrupts.RecordResolved(space, e.agent, InterruptApproval,
						"", "auto", "cleared",
						map[string]string{"wait_seconds": fmt.Sprintf("%.1f", waitSec)})
					s.logEvent(fmt.Sprintf("[%s/%s] approval interrupt cleared (waited %.1fs)", space, e.agent, waitSec))
				}
			}

			m := map[string]interface{}{
				"agent":          e.agent,
				"session":        e.session,
				"exists":         e.exists,
				"idle":           e.idle,
				"needs_approval": e.needsApproval,
			}
			if e.toolName != "" {
				m["tool_name"] = e.toolName
			}
			if e.prompt != "" {
				m["prompt_text"] = e.prompt
			}
			payload[i] = m

			// Update InferredStatus on the agent record
			inferred := inferAgentStatus(e.exists, e.idle, e.needsApproval)
			s.mu.Lock()
			if ks, ok := s.spaces[space]; ok {
				if agentRec, ok := ks.Agents[e.agent]; ok {
					if agentRec.Status != nil { agentRec.Status.InferredStatus = inferred }
				}
			}
			s.mu.Unlock()

			// Fire nudge when agent is idle, OR immediately if the session is gone
			// (agent may have restarted without re-registering a session).
			if (e.exists && e.idle) || !e.exists {
				s.nudgeMu.Lock()
				if _, pending := s.nudgePending[key]; pending {
					if !s.nudgeInFlight[key] {
						s.nudgeInFlight[key] = true
						delete(s.nudgePending, key)
						s.nudgeMu.Unlock()
						go s.executeNudge(space, e.agent)
					} else {
						s.nudgeMu.Unlock()
					}
				} else {
					s.nudgeMu.Unlock()
				}
			}
		}
		data, _ := json.Marshal(payload)
		s.broadcastSSE(space, "", "session_liveness", string(data))
	}

	// Sweep: fire nudges for agents with no registered session (not in probes).
	probedKeys := make(map[string]struct{}, len(probes))
	for _, p := range probes {
		probedKeys[p.space+"/"+p.agent] = struct{}{}
	}
	s.nudgeMu.Lock()
	var sessionlessNudges []struct{ space, agent string }
	for key := range s.nudgePending {
		if _, probed := probedKeys[key]; !probed {
			last := len(key) - 1
			for last >= 0 && key[last] != '/' {
				last--
			}
			if last > 0 {
				sessionlessNudges = append(sessionlessNudges, struct{ space, agent string }{key[:last], key[last+1:]})
			}
		}
	}
	for _, n := range sessionlessNudges {
		key := n.space + "/" + n.agent
		if !s.nudgeInFlight[key] {
			s.nudgeInFlight[key] = true
			delete(s.nudgePending, key)
			go s.executeNudge(n.space, n.agent)
		}
	}
	s.nudgeMu.Unlock()
}

// executeNudge triggers a single-agent check-in for an agent that has
// pending messages. Called from the liveness loop when the agent is idle.
func (s *Server) executeNudge(spaceName, agentName string) {
	key := spaceName + "/" + agentName
	defer func() {
		s.nudgeMu.Lock()
		delete(s.nudgeInFlight, key)
		s.nudgeMu.Unlock()
	}()

	s.logEvent(fmt.Sprintf("[%s/%s] auto-nudge: message pending, triggering check-in", spaceName, agentName))
	result := s.SingleAgentCheckIn(spaceName, agentName, "", "")

	if len(result.Errors) > 0 {
		s.logEvent(fmt.Sprintf("[%s/%s] auto-nudge failed: %s", spaceName, agentName, result.Errors[0]))
	} else if len(result.Sent) > 0 {
		s.logEvent(fmt.Sprintf("[%s/%s] auto-nudge complete", spaceName, agentName))
	}

	sseData, _ := json.Marshal(result)
	s.broadcastSSE(spaceName, "", "broadcast_complete", string(sseData))
}

func (s *Server) recordDecisionInterrupts(spaceName, agentName string, update *AgentUpdate) {
	for _, q := range update.Questions {
		ctx := map[string]string{}
		if update.Branch != "" {
			ctx["branch"] = update.Branch
		}
		if update.PR != "" {
			ctx["pr"] = update.PR
		}
		if update.Phase != "" {
			ctx["phase"] = update.Phase
		}
		s.interrupts.Record(spaceName, agentName, InterruptDecision, q, ctx)
	}
}

func (s *Server) handleInterrupts(w http.ResponseWriter, r *http.Request, spaceName string) {
	switch r.Method {
	case http.MethodGet:
		interrupts := s.interrupts.LoadAll(spaceName)
		if interrupts == nil {
			interrupts = []Interrupt{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(interrupts)
	case http.MethodPost:
		// Resolve a specific interrupt by ID.
		var payload struct {
			ID         string `json:"id"`
			Answer     string `json:"answer"`
			ResolvedBy string `json:"resolved_by"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.ID == "" {
			writeJSONError(w, "body must contain {id, answer}", http.StatusBadRequest)
			return
		}
		by := payload.ResolvedBy
		if by == "" {
			by = "human"
		}
		if err := s.interrupts.Resolve(spaceName, payload.ID, by, payload.Answer); err != nil {
			writeJSONError(w, err.Error(), http.StatusNotFound)
			return
		}
		s.logEvent(fmt.Sprintf("[%s] interrupt %s resolved by %s", spaceName, payload.ID, by))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "resolved", "id": payload.ID})
	default:
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleInterruptMetrics(w http.ResponseWriter, r *http.Request, spaceName string) {
	if r.Method != http.MethodGet {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	metrics := s.interrupts.Metrics(spaceName)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metrics)
}
