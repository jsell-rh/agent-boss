package coordinator

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// spaceHistoryPath returns the path to the NDJSON history file for a space.
func (s *Server) spaceHistoryPath(spaceName string) string {
	return filepath.Join(s.dataDir, spaceName+"-history.json")
}

// appendSnapshot appends a StatusSnapshot to the space history.
// When a repository is available (SQLite active) it is written to SQLite only.
// Fallback: append to NDJSON file when no repository is configured.
func (s *Server) appendSnapshot(snapshot StatusSnapshot) error {
	// Persist to SQLite (primary store).
	if s.repo != nil {
		s.saveSnapshotToDB(&snapshot)
		return nil
	}

	// Fallback: no SQLite — write to NDJSON file.
	if s.dataDir == "" {
		return nil
	}
	path := s.spaceHistoryPath(snapshot.Space)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open history file: %w", err)
	}
	defer f.Close()

	line, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	_, err = f.Write(append(line, '\n'))
	return err
}

// loadHistory reads snapshots for a space, preferring SQLite when available.
// If agent is non-empty, only snapshots for that agent are returned.
// If since is non-zero, only snapshots after that time are returned.
func (s *Server) loadHistory(spaceName, agent string, since time.Time) ([]StatusSnapshot, error) {
	// Prefer SQLite.
	if s.repo != nil {
		var sincePtr *time.Time
		if !since.IsZero() {
			sincePtr = &since
		}
		return s.loadSnapshotsFromRepo(spaceName, agent, sincePtr)
	}

	// Fallback: read NDJSON file.
	path := s.spaceHistoryPath(spaceName)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []StatusSnapshot{}, nil
		}
		return nil, fmt.Errorf("open history file: %w", err)
	}
	defer f.Close()

	var snapshots []StatusSnapshot
	scanner := bufio.NewScanner(f)
	// Default scanner buffer is 64KiB; increase for safety.
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var snap StatusSnapshot
		if err := json.Unmarshal(line, &snap); err != nil {
			continue // skip malformed lines
		}
		if agent != "" && !strings.EqualFold(snap.AgentName, agent) {
			continue
		}
		if !since.IsZero() && !snap.Timestamp.After(since) {
			continue
		}
		snapshots = append(snapshots, snap)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read history: %w", err)
	}
	return snapshots, nil
}

// handleSpaceHistory handles GET /spaces/{space}/history
// Optional query params: ?agent=name and ?since=RFC3339
func (s *Server) handleSpaceHistory(w http.ResponseWriter, r *http.Request, spaceName string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	agent := r.URL.Query().Get("agent")
	var since time.Time
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		var err error
		since, err = time.Parse(time.RFC3339, sinceStr)
		if err != nil {
			http.Error(w, fmt.Sprintf("invalid since param (use RFC3339): %v", err), http.StatusBadRequest)
			return
		}
	}

	snapshots, err := s.loadHistory(spaceName, agent, since)
	if err != nil {
		http.Error(w, fmt.Sprintf("load history: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(snapshots)
}

// handleAgentHistory handles GET /spaces/{space}/agent/{name}/history
// Optional query param: ?since=RFC3339
func (s *Server) handleAgentHistory(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var since time.Time
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		var err error
		since, err = time.Parse(time.RFC3339, sinceStr)
		if err != nil {
			http.Error(w, fmt.Sprintf("invalid since param (use RFC3339): %v", err), http.StatusBadRequest)
			return
		}
	}

	snapshots, err := s.loadHistory(spaceName, agentName, since)
	if err != nil {
		http.Error(w, fmt.Sprintf("load history: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(snapshots)
}

// snapshotFromAgent creates a StatusSnapshot from an agent update.
func snapshotFromAgent(spaceName, agentName string, agent *AgentUpdate) StatusSnapshot {
	return StatusSnapshot{
		AgentName:      agentName,
		Space:          spaceName,
		Status:         agent.Status,
		InferredStatus: agent.InferredStatus,
		Stale:          agent.Stale,
		Timestamp:      time.Now().UTC(),
	}
}
