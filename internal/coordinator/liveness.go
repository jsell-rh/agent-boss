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
	if !tmuxAvailable() {
		return
	}
	s.mu.RLock()
	type probe struct {
		space, agent, session string
	}
	var probes []probe
	for spaceName, ks := range s.spaces {
		for name, agent := range ks.Agents {
			if agent.TmuxSession != "" {
				probes = append(probes, probe{spaceName, name, agent.TmuxSession})
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
		e := statusEntry{agent: p.agent, session: p.session}
		e.exists = tmuxSessionExists(p.session)
		if e.exists {
			e.idle = tmuxIsIdle(p.session)
			if !e.idle {
				approval := tmuxCheckApproval(p.session)
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
					agentRec.InferredStatus = inferred
				}
			}
			s.mu.Unlock()

			// Check if this idle agent has a pending nudge
			if e.exists && e.idle {
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
		s.broadcastSSE(space, "", "tmux_liveness", string(data))
	}
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
