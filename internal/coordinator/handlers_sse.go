package coordinator

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

func (s *Server) broadcastSSE(space, targetAgent, event, data string) {
	id := fmt.Sprintf("%d", time.Now().UnixNano())
	msg := fmt.Sprintf("id: %s\nevent: %s\ndata: %s\n\n", id, event, data)
	payload := []byte(msg)
	s.sseMu.Lock()
	defer s.sseMu.Unlock()
	// Buffer targeted events for Last-Event-ID replay (cap SSEBufCap per agent).
	if targetAgent != "" {
		key := space + "/" + strings.ToLower(targetAgent)
		s.agentSSEBuf[key] = append(s.agentSSEBuf[key], sseEvent{ID: id, EventType: event, Data: data})
		if len(s.agentSSEBuf[key]) > SSEBufCap {
			s.agentSSEBuf[key] = s.agentSSEBuf[key][len(s.agentSSEBuf[key])-SSEBufCap:]
		}
	}
	for c := range s.sseClients {
		if c.space != "" && c.space != space {
			continue
		}
		if c.agent != "" {
			// Per-agent client: only receive events targeted at exactly this agent.
			if !strings.EqualFold(c.agent, targetAgent) {
				continue
			}
		}
		select {
		case c.ch <- payload:
		default:
		}
	}
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	s.serveSSE(w, r, "")
}

func (s *Server) handleSpaceSSE(w http.ResponseWriter, r *http.Request, spaceName string) {
	s.serveSSE(w, r, spaceName)
}

func (s *Server) serveSSE(w http.ResponseWriter, r *http.Request, space string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	setCORSOriginHeader(w, r)
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	client := &sseClient{ch: make(chan []byte, 64), space: space}
	s.sseMu.Lock()
	s.sseClients[client] = struct{}{}
	s.sseMu.Unlock()

	defer func() {
		s.sseMu.Lock()
		delete(s.sseClients, client)
		s.sseMu.Unlock()
	}()

	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-client.ch:
			if _, err := w.Write(msg); err != nil {
				slog.Warn("sse: write error, client likely disconnected", "space", space, "err", err)
				return
			}
			flusher.Flush()
		case t := <-keepalive.C:
			if _, err := fmt.Fprintf(w, ": keepalive %s\n\n", t.UTC().Format(time.RFC3339)); err != nil {
				slog.Warn("sse: keepalive write error, client likely disconnected", "space", space, "err", err)
				return
			}
			flusher.Flush()
		}
	}
}
