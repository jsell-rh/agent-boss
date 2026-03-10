package coordinator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s *Server) spacePath(name string) string {
	return filepath.Join(s.dataDir, name+".json")
}

func (s *Server) spaceMarkdownPath(name string) string {
	return filepath.Join(s.dataDir, name+".md")
}

func (s *Server) loadAllSpaces() error {
	// --- Primary path: load from SQLite ---
	if s.repo != nil {
		empty, err := s.repo.IsEmpty()
		if err != nil {
			return fmt.Errorf("check db empty: %w", err)
		}
		if !empty {
			return s.loadAllSpacesFromRepo()
		}
		// DB is empty: import from legacy JSON/journal files, then persist to DB.
		s.logEvent("DB empty — importing legacy JSON/journal data")
		if err := s.loadAllSpacesFromFiles(); err != nil {
			return err
		}
		for _, ks := range s.spaces {
			s.persistSpaceToDB(ks)
		}
		return nil
	}

	// --- Fallback: no DB, use file storage ---
	return s.loadAllSpacesFromFiles()
}

// loadAllSpacesFromFiles loads spaces from legacy JSON/JSONL files (fallback path).
func (s *Server) loadAllSpacesFromFiles() error {
	entries, err := os.ReadDir(s.dataDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	// Collect space names from both .json and .events.jsonl files.
	seen := make(map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".json") && !strings.HasSuffix(name, ".events.jsonl") {
			seen[strings.TrimSuffix(name, ".json")] = true
		} else if strings.HasSuffix(name, ".events.jsonl") {
			seen[strings.TrimSuffix(name, ".events.jsonl")] = true
		}
	}

	for name := range seen {
		ks, err := s.loadSpaceWithJournal(name)
		if err != nil {
			s.logEvent(fmt.Sprintf("failed to load space %q: %v", name, err))
			continue
		}
		s.spaces[name] = ks
		s.logEvent(fmt.Sprintf("loaded space %q (%d agents)", name, len(ks.Agents)))
	}
	return nil
}

// loadSpaceWithJournal loads a space preferring event replay over raw JSON.
// If a journal exists, it replays events (migrating from JSON if needed).
// If no journal exists, it loads from JSON and seeds a snapshot event.
func (s *Server) loadSpaceWithJournal(name string) (*KnowledgeSpace, error) {
	journalPath := filepath.Join(s.dataDir, name+".events.jsonl")
	jsonPath := s.spacePath(name)

	journalExists := false
	if _, err := os.Stat(journalPath); err == nil {
		journalExists = true
	}

	if journalExists {
		ks, err := s.journal.ReplayInto(name)
		if err != nil {
			return nil, fmt.Errorf("replay journal for %q: %w", name, err)
		}
		if ks != nil {
			return ks, nil
		}
		// Empty journal — fall through to JSON.
	}

	// No journal or empty journal — try loading from JSON.
	if _, err := os.Stat(jsonPath); err != nil {
		// No JSON either — create a fresh space.
		ks := NewKnowledgeSpace(name)
		s.journal.Append(name, EventSpaceCreated, "", map[string]any{
			"name":       name,
			"created_at": ks.CreatedAt,
		})
		return ks, nil
	}

	ks, err := s.loadSpace(name)
	if err != nil {
		return nil, err
	}

	// Migrate: seed the journal with a snapshot of the existing JSON state.
	if err := s.journal.MigrateFromJSON(ks); err != nil {
		s.logEvent(fmt.Sprintf("warning: migrate %q to journal: %v", name, err))
	} else {
		s.logEvent(fmt.Sprintf("migrated space %q from JSON to event journal", name))
	}

	return ks, nil
}

func (s *Server) loadSpace(name string) (*KnowledgeSpace, error) {
	data, err := os.ReadFile(s.spacePath(name))
	if err != nil {
		return nil, err
	}
	var ks KnowledgeSpace
	if err := json.Unmarshal(data, &ks); err != nil {
		return nil, fmt.Errorf("unmarshal %s: %w", name, err)
	}
	if ks.Agents == nil {
		ks.Agents = make(map[string]*AgentRecord)
	}
	return &ks, nil
}


func (s *Server) saveSpace(ks *KnowledgeSpace) error {
	// Refresh protocol before persisting so SQLite receives the updated SharedContracts.
	s.refreshProtocol(ks)
	// Persist to SQLite (primary store). JSON/.md files are migration-only artifacts.
	s.persistSpaceToDB(ks)
	return nil
}

func (s *Server) refreshProtocol(ks *KnowledgeSpace) {
	if protocolTemplate == "" {
		return
	}
	// Only set protocol if SharedContracts is empty (don't overwrite manual edits)
	if ks.SharedContracts == "" {
		ks.SharedContracts = strings.ReplaceAll(protocolTemplate, "{SPACE}", ks.Name)
	}
}

// getOrCreateSpaceLocked returns or creates the named space.
// Caller MUST hold s.mu (write lock).
func (s *Server) getOrCreateSpaceLocked(name string) *KnowledgeSpace {
	if ks, ok := s.spaces[name]; ok {
		return ks
	}
	ks := NewKnowledgeSpace(name)
	s.spaces[name] = ks
	s.logEvent(fmt.Sprintf("created space %q", name))
	return ks
}

// getOrCreateSpace is a convenience wrapper that acquires s.mu internally.
// Use this only when the returned space pointer does NOT need to be used
// while holding s.mu (e.g. for read-only access or when re-acquiring the
// lock immediately after is not needed). For write operations, prefer
// acquiring s.mu first then calling getOrCreateSpaceLocked.
func (s *Server) getOrCreateSpace(name string) *KnowledgeSpace {
	s.mu.Lock()
	ks := s.getOrCreateSpaceLocked(name)
	s.mu.Unlock()
	return ks
}

func (s *Server) getSpace(name string) (*KnowledgeSpace, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ks, ok := s.spaces[name]
	return ks, ok
}

func (s *Server) listSpaceNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.spaces))
	for name := range s.spaces {
		names = append(names, name)
	}
	return names
}

func (s *Server) startCompactionLoop(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-s.stopLiveness:
				return
			case <-ticker.C:
				s.compactAllSpaces()
			}
		}
	}()
}

func (s *Server) compactAllSpaces() {
	s.mu.RLock()
	names := make([]string, 0, len(s.spaces))
	for name := range s.spaces {
		names = append(names, name)
	}
	s.mu.RUnlock()

	for _, name := range names {
		s.compactSpace(name)
	}
}

// compactSpace snapshots and compacts the journal for a single space.
func (s *Server) compactSpace(name string) {
	// Snapshot the space under RLock so Compact does not race with writers.
	s.mu.RLock()
	ks, ok := s.spaces[name]
	var ksCopy *KnowledgeSpace
	if ok {
		b, err := json.Marshal(ks)
		if err == nil {
			var snap KnowledgeSpace
			if json.Unmarshal(b, &snap) == nil {
				ksCopy = &snap
			}
		}
	}
	s.mu.RUnlock()
	if ksCopy == nil {
		return
	}
	if err := s.journal.Compact(name, ksCopy); err != nil {
		s.logEvent(fmt.Sprintf("compaction failed for %q: %v", name, err))
	} else {
		s.logEvent(fmt.Sprintf("compacted journal for %q", name))
	}
}

// maybeCompact triggers a background compaction for a space if its event count
// exceeds CompactionThreshold. It returns immediately; compaction runs async.
func (s *Server) maybeCompact(spaceName string) {
	if s.journal.EventCount(spaceName) > CompactionThreshold {
		go s.compactSpace(spaceName)
	}
}

