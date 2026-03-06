package coordinator

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// SpaceEventType identifies what kind of state change occurred.
type SpaceEventType string

const (
	EventAgentUpdated      SpaceEventType = "agent_updated"
	EventMessageSent       SpaceEventType = "message_sent"
	EventMessageAcked      SpaceEventType = "message_acked"
	EventAgentRemoved      SpaceEventType = "agent_removed"
	EventSpaceCreated      SpaceEventType = "space_created"
	EventContractsUpdated  SpaceEventType = "contracts_updated"
	EventArchiveUpdated    SpaceEventType = "archive_updated"
	EventSnapshot          SpaceEventType = "snapshot"
)

// SpaceEvent is a single append-only entry in the event journal.
type SpaceEvent struct {
	ID        string          `json:"id"`
	Space     string          `json:"space"`
	Type      SpaceEventType  `json:"type"`
	Agent     string          `json:"agent,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// EventJournal is an append-only JSONL event log for a data directory.
// One journal file per space: {space}.events.jsonl
type EventJournal struct {
	dataDir string
	mu      sync.Mutex
	seq     atomic.Int64
}

func NewEventJournal(dataDir string) *EventJournal {
	j := &EventJournal{dataDir: dataDir}
	j.seq.Store(time.Now().UnixMilli())
	return j
}

func (j *EventJournal) journalPath(space string) string {
	return filepath.Join(j.dataDir, space+".events.jsonl")
}

func (j *EventJournal) nextID() string {
	n := j.seq.Add(1)
	return fmt.Sprintf("ev_%d", n)
}

// Append writes an event to the journal. Errors are silently dropped (best-effort).
func (j *EventJournal) Append(space string, evType SpaceEventType, agent string, payload any) *SpaceEvent {
	var raw json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err == nil {
			raw = b
		}
	}
	ev := &SpaceEvent{
		ID:        j.nextID(),
		Space:     space,
		Type:      evType,
		Agent:     agent,
		Timestamp: time.Now().UTC(),
		Payload:   raw,
	}
	j.write(ev)
	return ev
}

func (j *EventJournal) write(ev *SpaceEvent) {
	j.mu.Lock()
	defer j.mu.Unlock()

	data, err := json.Marshal(ev)
	if err != nil {
		return
	}

	f, err := os.OpenFile(j.journalPath(ev.Space), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(data)
	f.Write([]byte("\n"))
}

// LoadSince returns all events for a space at or after the given time.
// If since is zero, all events are returned.
func (j *EventJournal) LoadSince(space string, since time.Time) ([]SpaceEvent, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	f, err := os.Open(j.journalPath(space))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var events []SpaceEvent
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 512*1024), 512*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev SpaceEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if since.IsZero() || !ev.Timestamp.Before(since) {
			events = append(events, ev)
		}
	}
	return events, scanner.Err()
}

// ReplayInto reconstructs a KnowledgeSpace from the event journal.
// It returns nil if the journal does not exist (caller should fall back to JSON).
func (j *EventJournal) ReplayInto(space string) (*KnowledgeSpace, error) {
	events, err := j.LoadSince(space, time.Time{})
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, nil
	}

	ks := NewKnowledgeSpace(space)

	for _, ev := range events {
		switch ev.Type {
		case EventSnapshot:
			var snap KnowledgeSpace
			if err := json.Unmarshal(ev.Payload, &snap); err != nil {
				continue
			}
			// Snapshot replaces current state entirely.
			ks = &snap
			if ks.Agents == nil {
				ks.Agents = make(map[string]*AgentUpdate)
			}

		case EventSpaceCreated:
			var meta struct {
				Name      string    `json:"name"`
				CreatedAt time.Time `json:"created_at"`
			}
			if err := json.Unmarshal(ev.Payload, &meta); err == nil && meta.Name != "" {
				ks.Name = meta.Name
				ks.CreatedAt = meta.CreatedAt
			}

		case EventAgentUpdated:
			var update AgentUpdate
			if err := json.Unmarshal(ev.Payload, &update); err != nil {
				continue
			}
			ks.Agents[ev.Agent] = &update
			ks.UpdatedAt = ev.Timestamp

		case EventAgentRemoved:
			delete(ks.Agents, ev.Agent)
			ks.UpdatedAt = ev.Timestamp

		case EventMessageSent:
			var msg AgentMessage
			if err := json.Unmarshal(ev.Payload, &msg); err != nil {
				continue
			}
			agent, ok := ks.Agents[ev.Agent]
			if !ok {
				agent = &AgentUpdate{
					Status:    StatusIdle,
					Summary:   ev.Agent + ": pending message delivery",
					UpdatedAt: ev.Timestamp,
				}
				ks.Agents[ev.Agent] = agent
			}
			agent.Messages = append(agent.Messages, msg)
			if len(agent.Messages) > 50 {
				agent.Messages = agent.Messages[len(agent.Messages)-50:]
			}

		case EventMessageAcked:
			var ack struct {
				MessageID string    `json:"message_id"`
				AckedAt   time.Time `json:"acked_at"`
			}
			if err := json.Unmarshal(ev.Payload, &ack); err != nil {
				continue
			}
			agent, ok := ks.Agents[ev.Agent]
			if !ok {
				continue
			}
			for i := range agent.Messages {
				if agent.Messages[i].ID == ack.MessageID {
					agent.Messages[i].Read = true
					t := ack.AckedAt
					agent.Messages[i].ReadAt = &t
					break
				}
			}

		case EventContractsUpdated:
			var payload struct {
				Content string `json:"content"`
			}
			if err := json.Unmarshal(ev.Payload, &payload); err == nil {
				ks.SharedContracts = payload.Content
			}

		case EventArchiveUpdated:
			var payload struct {
				Content string `json:"content"`
			}
			if err := json.Unmarshal(ev.Payload, &payload); err == nil {
				ks.Archive = payload.Content
			}
		}
	}

	return ks, nil
}

// Compact writes a snapshot event of the current state and then rewrites the
// journal to contain only the snapshot (dropping all prior events).
func (j *EventJournal) Compact(space string, ks *KnowledgeSpace) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	snapPayload, err := json.Marshal(ks)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}

	ev := &SpaceEvent{
		ID:        j.nextID(),
		Space:     space,
		Type:      EventSnapshot,
		Timestamp: time.Now().UTC(),
		Payload:   snapPayload,
	}

	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	// Write new journal with only the snapshot.
	path := j.journalPath(space)
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	f.Write(data)
	f.Write([]byte("\n"))
	f.Close()

	return os.Rename(tmp, path)
}

// MigrateFromJSON writes an initial snapshot event from an existing JSON space
// so that subsequent operations are journal-based.
func (j *EventJournal) MigrateFromJSON(ks *KnowledgeSpace) error {
	snapPayload, err := json.Marshal(ks)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	ev := &SpaceEvent{
		ID:        j.nextID(),
		Space:     ks.Name,
		Type:      EventSnapshot,
		Timestamp: time.Now().UTC(),
		Payload:   snapPayload,
	}
	j.write(ev)
	return nil
}
