package coordinator

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// EventType identifies the category of a domain event.
type EventType string

const (
	// AgentLifecycle events
	EventAgentSpawned    EventType = "agent.spawned"
	EventAgentStopped    EventType = "agent.stopped"
	EventAgentRestarted  EventType = "agent.restarted"

	// AgentStatus events
	EventAgentStatusUpdated EventType = "agent.status_updated"
	EventAgentDropped       EventType = "agent.removed"
	EventAgentCreated       EventType = "agent.created"

	// MessageDelivery events
	EventMsgDelivered     EventType = "message.delivered"
	EventWebhookDelivered EventType = "webhook.delivered"
	EventMsgAcked         EventType = "message.acked"

	// Liveness events
	EventAgentStale        EventType = "liveness.agent_stale"
	EventAgentStaleCleared EventType = "liveness.agent_stale_cleared"
	EventHeartbeatReceived EventType = "liveness.heartbeat_received"
	EventNudgeTriggered    EventType = "liveness.nudge_triggered"

	// Registration events
	EventAgentRegistered EventType = "registration.agent_registered"

	// Persistence events
	EventSpaceLoaded    EventType = "persistence.space_loaded"
	EventSpacePersisted EventType = "persistence.space_created"
	EventSpaceDeleted   EventType = "persistence.space_deleted"
	EventSpaceCompacted EventType = "persistence.space_compacted"

	// HTTP events
	EventHTTPRequest EventType = "http.request"

	// Server events
	EventServerStarted EventType = "server.started"
	EventServerStopped EventType = "server.stopped"
	EventServerError   EventType = "server.error"

	// Tmux events
	EventBroadcastComplete EventType = "tmux.broadcast_complete"

	// Generic event for unclassified logEvent calls
	EventGeneric EventType = "generic"
)

// Level represents the severity of a domain event.
type Level string

const (
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// DomainEvent carries structured information about a coordinator event.
type DomainEvent struct {
	Level     Level             `json:"level"`
	EventType EventType         `json:"event_type"`
	Space     string            `json:"space,omitempty"`
	Agent     string            `json:"agent,omitempty"`
	Msg       string            `json:"msg"`
	Fields    map[string]string `json:"fields,omitempty"`
	Timestamp time.Time         `json:"timestamp"`
}

// Logger is the port for domain event emission.
type Logger interface {
	Log(e DomainEvent)
}

// JSONLogger emits newline-delimited JSON to an io.Writer (production default).
type JSONLogger struct {
	w io.Writer
}

// NewJSONLogger creates a JSONLogger writing to w.
func NewJSONLogger(w io.Writer) *JSONLogger {
	return &JSONLogger{w: w}
}

func (l *JSONLogger) Log(e DomainEvent) {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	fmt.Fprintf(l.w, "%s\n", b)
}

// ANSI color codes used by PrettyLogger.
const (
	ansiReset  = "\033[0m"
	ansiGray   = "\033[90m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiRed    = "\033[31m"
	ansiCyan   = "\033[36m"
)

// PrettyLogger emits human-readable, ANSI-colored lines to an io.Writer (TTY).
type PrettyLogger struct {
	w io.Writer
}

// NewPrettyLogger creates a PrettyLogger writing to w.
func NewPrettyLogger(w io.Writer) *PrettyLogger {
	return &PrettyLogger{w: w}
}

func (l *PrettyLogger) Log(e DomainEvent) {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	color := ansiGreen
	switch e.Level {
	case LevelWarn:
		color = ansiYellow
	case LevelError:
		color = ansiRed
	}
	ts := e.Timestamp.Format("15:04:05")
	ctx := ""
	if e.Space != "" && e.Agent != "" {
		ctx = fmt.Sprintf(" %s[%s/%s]%s", ansiCyan, e.Space, e.Agent, ansiReset)
	} else if e.Space != "" {
		ctx = fmt.Sprintf(" %s[%s]%s", ansiCyan, e.Space, ansiReset)
	}
	fmt.Fprintf(l.w, "%s%s%s%s %s%s%s %s\n",
		ansiGray, ts, ansiReset,
		ctx,
		color, string(e.EventType), ansiReset,
		e.Msg,
	)
}

// isCharDevice returns true when f refers to a character device (i.e. a TTY).
func isCharDevice(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// NewLogger creates a Logger appropriate for the given output file.
// Selection order:
//  1. LOG_FORMAT env var: "json" → JSONLogger, "pretty" → PrettyLogger
//  2. If out is a character device (TTY) → PrettyLogger
//  3. Otherwise → JSONLogger
func NewLogger(out *os.File) Logger {
	switch os.Getenv("LOG_FORMAT") {
	case "json":
		return NewJSONLogger(out)
	case "pretty":
		return NewPrettyLogger(out)
	}
	if isCharDevice(out) {
		return NewPrettyLogger(out)
	}
	return NewJSONLogger(out)
}

// testLogger collects emitted DomainEvents in memory for use in tests.
type testLogger struct {
	mu     sync.Mutex
	events []DomainEvent
}

func (l *testLogger) Log(e DomainEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
}

func (l *testLogger) last() *DomainEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.events) == 0 {
		return nil
	}
	e := l.events[len(l.events)-1]
	return &e
}
