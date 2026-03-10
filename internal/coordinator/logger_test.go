package coordinator

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestJSONLoggerOutput(t *testing.T) {
	var buf bytes.Buffer
	l := NewJSONLogger(&buf)

	e := DomainEvent{
		Level:     LevelInfo,
		EventType: EventAgentSpawned,
		Space:     "TestSpace",
		Agent:     "Bot1",
		Msg:       "agent spawned",
		Timestamp: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
	}
	l.Log(e)

	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("expected JSON output, got empty string")
	}

	var got map[string]interface{}
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("invalid JSON: %v — output: %s", err, line)
	}
	if got["event_type"] != string(EventAgentSpawned) {
		t.Errorf("event_type = %q, want %q", got["event_type"], EventAgentSpawned)
	}
	if got["msg"] != "agent spawned" {
		t.Errorf("msg = %q, want %q", got["msg"], "agent spawned")
	}
	if got["space"] != "TestSpace" {
		t.Errorf("space = %q, want TestSpace", got["space"])
	}
}

func TestJSONLoggerSetsTimestamp(t *testing.T) {
	var buf bytes.Buffer
	l := NewJSONLogger(&buf)
	l.Log(DomainEvent{Level: LevelInfo, EventType: EventGeneric, Msg: "hello"})

	var got map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	ts, ok := got["timestamp"].(string)
	if !ok || ts == "" {
		t.Error("expected timestamp to be set automatically")
	}
}

func TestPrettyLoggerOutput(t *testing.T) {
	var buf bytes.Buffer
	l := NewPrettyLogger(&buf)

	l.Log(DomainEvent{
		Level:     LevelWarn,
		EventType: EventAgentStale,
		Space:     "S",
		Agent:     "A",
		Msg:       "going stale",
		Timestamp: time.Date(2026, 1, 1, 9, 30, 0, 0, time.UTC),
	})

	out := buf.String()
	if !strings.Contains(out, "09:30:00") {
		t.Errorf("expected timestamp in output, got: %s", out)
	}
	if !strings.Contains(out, "going stale") {
		t.Errorf("expected message in output, got: %s", out)
	}
	if !strings.Contains(out, "[S/A]") {
		t.Errorf("expected space/agent context in output, got: %s", out)
	}
}

func TestNewLoggerEnvJSON(t *testing.T) {
	t.Setenv("LOG_FORMAT", "json")
	f, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	l := NewLogger(f)
	if _, ok := l.(*JSONLogger); !ok {
		t.Errorf("LOG_FORMAT=json should produce JSONLogger, got %T", l)
	}
}

func TestNewLoggerEnvPretty(t *testing.T) {
	t.Setenv("LOG_FORMAT", "pretty")
	f, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	l := NewLogger(f)
	if _, ok := l.(*PrettyLogger); !ok {
		t.Errorf("LOG_FORMAT=pretty should produce PrettyLogger, got %T", l)
	}
}

func TestNewLoggerDefaultFile(t *testing.T) {
	t.Setenv("LOG_FORMAT", "")
	f, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// A regular temp file is not a character device → should produce JSONLogger.
	l := NewLogger(f)
	if _, ok := l.(*JSONLogger); !ok {
		t.Errorf("non-TTY file should produce JSONLogger, got %T", l)
	}
}

func TestTestLoggerCollectsEvents(t *testing.T) {
	tl := &testLogger{}
	srv := newTestServer(t, tl)
	_ = srv

	tl.Log(DomainEvent{Level: LevelInfo, EventType: EventGeneric, Msg: "hello"})
	if len(tl.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(tl.events))
	}
	if tl.events[0].Msg != "hello" {
		t.Errorf("msg = %q, want hello", tl.events[0].Msg)
	}
}

func TestServerEmitPopulatesEventLog(t *testing.T) {
	tl := &testLogger{}
	srv := newTestServer(t, tl)

	srv.emit(DomainEvent{
		Level:     LevelInfo,
		EventType: EventAgentSpawned,
		Space:     "S",
		Agent:     "A",
		Msg:       "test event",
	})

	// EventLog ring buffer should contain the entry.
	events := srv.RecentEvents(10)
	found := false
	for _, e := range events {
		if strings.Contains(e, "test event") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'test event' in EventLog, got: %v", events)
	}

	// testLogger should also have received it.
	if len(tl.events) == 0 {
		t.Fatal("testLogger received no events")
	}
	last := tl.last()
	if last == nil || last.Msg != "test event" {
		t.Errorf("testLogger last event msg = %q, want 'test event'", last.Msg)
	}
}

func TestLogEventUsesEmit(t *testing.T) {
	tl := &testLogger{}
	srv := newTestServer(t, tl)

	srv.logEvent("some log message")

	if len(tl.events) == 0 {
		t.Fatal("testLogger received no events from logEvent")
	}
	if tl.last().Msg != "some log message" {
		t.Errorf("msg = %q, want 'some log message'", tl.last().Msg)
	}
	if tl.last().EventType != EventGeneric {
		t.Errorf("event_type = %q, want %q", tl.last().EventType, EventGeneric)
	}
}

// newTestServer creates a Server with an injected testLogger and no HTTP listener.
func newTestServer(t *testing.T, tl *testLogger) *Server {
	t.Helper()
	srv := NewServer(":0", t.TempDir())
	srv.logger = tl
	return srv
}
