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

	// Task events
	EventTaskCreated   SpaceEventType = "task_created"
	EventTaskUpdated   SpaceEventType = "task_updated"
	EventTaskDeleted   SpaceEventType = "task_deleted"
	EventTaskCommented SpaceEventType = "task_commented"
	EventTaskMoved     SpaceEventType = "task_moved"
	EventTaskAssigned  SpaceEventType = "task_assigned"
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

// CompactionThreshold is the number of events per space that triggers automatic
// count-based compaction. The server checks this after each write.
const CompactionThreshold = 1000

// ringBufferCap is the default maximum number of events held per space in memory
// when the journal operates in ring buffer mode (SQLite is the primary store).
const ringBufferCap = 500

// EventJournal is an append-only JSONL event log for a data directory.
// One journal file per space: {space}.events.jsonl
//
// When UseRingBuffer is called (used when SQLite is the primary store),
// events are kept in a bounded in-memory ring instead of written to disk.
type EventJournal struct {
	dataDir   string
	mu        sync.RWMutex        // write lock for Append/Compact; read lock for LoadSince
	seq       atomic.Int64
	openFiles map[string]*os.File // persistent write handles, protected by mu (write lock)
	counts    sync.Map            // map[string]*atomic.Int64 — event count per space

	// Ring buffer mode (no file I/O). All fields protected by mu.
	ringMode bool
	ringBuf  map[string][]SpaceEvent
	ringCap  int
	// dbWrite is called in ring buffer mode to persist each event to SQLite.
	// It must be safe for concurrent use and must not block significantly.
	dbWrite func(ev *SpaceEvent)
}

func NewEventJournal(dataDir string) *EventJournal {
	j := &EventJournal{
		dataDir:   dataDir,
		openFiles: make(map[string]*os.File),
	}
	j.seq.Store(time.Now().UnixMilli())
	return j
}

// LoadIntoRing loads a pre-existing event into the ring buffer without calling
// dbWrite. Used to pre-warm the ring from SQLite on startup.
func (j *EventJournal) LoadIntoRing(ev *SpaceEvent) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.ringMode {
		return
	}
	buf := j.ringBuf[ev.Space]
	buf = append(buf, *ev)
	if len(buf) > j.ringCap {
		buf = buf[len(buf)-j.ringCap:]
	}
	j.ringBuf[ev.Space] = buf
}

// UseRingBuffer switches the journal to in-memory ring buffer mode.
// All subsequent Append calls store events in memory (capped at cap per space)
// and no files are written. Compact and MigrateFromJSON become no-ops.
// dbWrite, if non-nil, is called for each event to persist it to SQLite.
// Must be called before any Append calls (i.e. before serving requests).
func (j *EventJournal) UseRingBuffer(cap int, dbWrite func(ev *SpaceEvent)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.ringMode = true
	j.ringCap = cap
	j.ringBuf = make(map[string][]SpaceEvent)
	j.dbWrite = dbWrite
}

// EventCount returns the current event count for a space (best-effort, not exact after restart).
func (j *EventJournal) EventCount(space string) int64 {
	v, ok := j.counts.Load(space)
	if !ok {
		return 0
	}
	return v.(*atomic.Int64).Load()
}

func (j *EventJournal) incrementCount(space string) {
	v, _ := j.counts.LoadOrStore(space, new(atomic.Int64))
	v.(*atomic.Int64).Add(1)
}

func (j *EventJournal) resetCount(space string) {
	v, _ := j.counts.LoadOrStore(space, new(atomic.Int64))
	v.(*atomic.Int64).Store(1) // 1 for the snapshot event just written
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

	if j.ringMode {
		buf := j.ringBuf[ev.Space]
		buf = append(buf, *ev)
		if len(buf) > j.ringCap {
			buf = buf[len(buf)-j.ringCap:]
		}
		j.ringBuf[ev.Space] = buf
		j.incrementCount(ev.Space)
		dbw := j.dbWrite
		j.mu.Unlock()
		// Call dbWrite outside the lock to avoid holding mu during I/O.
		if dbw != nil {
			dbw(ev)
		}
		return
	}

	defer j.mu.Unlock()

	data, err := json.Marshal(ev)
	if err != nil {
		return
	}

	var f *os.File
	var ok bool
	f, ok = j.openFiles[ev.Space]
	if !ok {
		f, err = os.OpenFile(j.journalPath(ev.Space), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return
		}
		j.openFiles[ev.Space] = f
	}
	f.Write(data)
	f.Write([]byte("\n"))
	j.incrementCount(ev.Space)
}

// LoadSince returns all events for a space at or after the given time.
// If since is zero, all events are returned.
func (j *EventJournal) LoadSince(space string, since time.Time) ([]SpaceEvent, error) {
	j.mu.RLock()
	defer j.mu.RUnlock()

	if j.ringMode {
		buf := j.ringBuf[space]
		var events []SpaceEvent
		for _, ev := range buf {
			if since.IsZero() || !ev.Timestamp.Before(since) {
				events = append(events, ev)
			}
		}
		return events, nil
	}

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
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)
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
				ks.Agents = make(map[string]*AgentRecord)
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
			ks.setAgentStatus(ev.Agent, &update)
			ks.UpdatedAt = ev.Timestamp

		case EventAgentRemoved:
			delete(ks.Agents, ev.Agent)
			ks.UpdatedAt = ev.Timestamp

		case EventMessageSent:
			var msg AgentMessage
			if err := json.Unmarshal(ev.Payload, &msg); err != nil {
				continue
			}
			agent := ks.agentStatus(ev.Agent)
			if agent == nil {
				agent = &AgentUpdate{
					Status:    StatusIdle,
					Summary:   ev.Agent + ": pending message delivery",
					UpdatedAt: ev.Timestamp,
				}
				ks.setAgentStatus(ev.Agent, agent)
			}
			agent.Messages = append(agent.Messages, msg)
			// Retain all unread messages; cap read messages at 50.
			const maxReadMessages = 50
			readCount := 0
			for _, m := range agent.Messages {
				if m.Read {
					readCount++
				}
			}
			if readCount > maxReadMessages {
				toSkip := readCount - maxReadMessages
				skipped := 0
				filtered := make([]AgentMessage, 0, len(agent.Messages))
				for _, m := range agent.Messages {
					if m.Read && skipped < toSkip {
						skipped++
						continue
					}
					filtered = append(filtered, m)
				}
				agent.Messages = filtered
			}

		case EventMessageAcked:
			var ack struct {
				MessageID string    `json:"message_id"`
				AckedAt   time.Time `json:"acked_at"`
			}
			if err := json.Unmarshal(ev.Payload, &ack); err != nil {
				continue
			}
			agent, ok := ks.agentStatusOk(ev.Agent)
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

		case EventTaskCreated, EventTaskUpdated:
			var task Task
			if err := json.Unmarshal(ev.Payload, &task); err != nil {
				continue
			}
			if ks.Tasks == nil {
				ks.Tasks = make(map[string]*Task)
			}
			ks.Tasks[task.ID] = &task
			if task.ID > fmt.Sprintf("TASK-%03d", ks.NextTaskSeq) {
				// parse and update counter
				var seq int
				fmt.Sscanf(task.ID, "TASK-%d", &seq)
				if seq >= ks.NextTaskSeq {
					ks.NextTaskSeq = seq + 1
				}
			}

		case EventTaskDeleted:
			var payload struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(ev.Payload, &payload); err == nil && payload.ID != "" {
				delete(ks.Tasks, payload.ID)
			}

		case EventTaskCommented:
			var payload struct {
				TaskID  string      `json:"task_id"`
				Comment TaskComment `json:"comment"`
			}
			if err := json.Unmarshal(ev.Payload, &payload); err != nil {
				continue
			}
			if ks.Tasks == nil {
				continue
			}
			if task, ok := ks.Tasks[payload.TaskID]; ok {
				task.Comments = append(task.Comments, payload.Comment)
				task.UpdatedAt = ev.Timestamp
			}

		case EventTaskMoved:
			var payload struct {
				ID     string     `json:"id"`
				Status TaskStatus `json:"status"`
			}
			if err := json.Unmarshal(ev.Payload, &payload); err != nil {
				continue
			}
			if ks.Tasks == nil {
				continue
			}
			if task, ok := ks.Tasks[payload.ID]; ok {
				task.Status = payload.Status
				task.UpdatedAt = ev.Timestamp
			}

		case EventTaskAssigned:
			var payload struct {
				ID         string `json:"id"`
				AssignedTo string `json:"assigned_to"`
			}
			if err := json.Unmarshal(ev.Payload, &payload); err != nil {
				continue
			}
			if ks.Tasks == nil {
				continue
			}
			if task, ok := ks.Tasks[payload.ID]; ok {
				task.AssignedTo = payload.AssignedTo
				task.UpdatedAt = ev.Timestamp
			}
		}
	}

	return ks, nil
}

// Compact writes a snapshot event of the current state and then rewrites the
// journal to contain only the snapshot (dropping all prior events).
// In ring buffer mode this is a no-op — SQLite is the durable store.
func (j *EventJournal) Compact(space string, ks *KnowledgeSpace) error {
	j.mu.Lock()
	if j.ringMode {
		j.mu.Unlock()
		return nil
	}
	j.mu.Unlock()

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

	// Close and evict the pooled write handle before rewriting the file.
	if f, ok := j.openFiles[space]; ok {
		f.Close()
		delete(j.openFiles, space)
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

	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	j.resetCount(space)
	return nil
}

// MigrateFromJSON writes an initial snapshot event from an existing JSON space
// so that subsequent operations are journal-based.
// In ring buffer mode this is a no-op — SQLite handles persistence.
func (j *EventJournal) MigrateFromJSON(ks *KnowledgeSpace) error {
	if j.ringMode {
		return nil
	}
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
