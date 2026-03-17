package db

import (
	"fmt"
	"testing"
	"time"

	glebarez "github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// openTestDB opens an in-memory SQLite DB with the full schema applied.
func openTestDB(t *testing.T) *Repository {
	t.Helper()
	db, err := gorm.Open(glebarez.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return New(db)
}

// mustSaveSnapshot is a test helper that creates a snapshot and fails the test on error.
func mustSaveSnapshot(t *testing.T, repo *Repository, spaceName, agentName string, ts time.Time) {
	t.Helper()
	if err := repo.SaveSnapshot(&StatusSnapshot{
		SpaceName: spaceName,
		AgentName: agentName,
		Status:    "active",
		Timestamp: ts,
	}); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
}

// ─── PruneOldSnapshots — age-based cutoff ────────────────────────────────────

func TestPruneOldSnapshotsAgeCutoff(t *testing.T) {
	repo := openTestDB(t)

	old := time.Now().UTC().Add(-48 * time.Hour)
	recent := time.Now().UTC()

	mustSaveSnapshot(t, repo, "s", "a", old)
	mustSaveSnapshot(t, repo, "s", "a", recent)

	cutoff := time.Now().UTC().Add(-24 * time.Hour)
	if err := repo.PruneOldSnapshots(cutoff); err != nil {
		t.Fatalf("PruneOldSnapshots: %v", err)
	}

	snaps, err := repo.GetSnapshots("s", "a", nil)
	if err != nil {
		t.Fatalf("GetSnapshots: %v", err)
	}
	if len(snaps) != 1 {
		t.Errorf("want 1 snapshot after age prune, got %d", len(snaps))
	}
}

// ─── PruneOldSnapshots — per-agent row cap ────────────────────────────────────

func TestPruneOldSnapshotsPerAgentCap(t *testing.T) {
	repo := openTestDB(t)

	// Insert snapshotPerAgentCap + 10 rows for a single agent, all within retention window.
	base := time.Now().UTC().Add(-1 * time.Hour)
	for i := 0; i < snapshotPerAgentCap+10; i++ {
		mustSaveSnapshot(t, repo, "s", "agent", base.Add(time.Duration(i)*time.Minute))
	}

	// Prune with a cutoff far in the past (age prune removes nothing; cap applies).
	cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour)
	if err := repo.PruneOldSnapshots(cutoff); err != nil {
		t.Fatalf("PruneOldSnapshots: %v", err)
	}

	snaps, err := repo.GetSnapshots("s", "agent", nil)
	if err != nil {
		t.Fatalf("GetSnapshots: %v", err)
	}
	if len(snaps) > snapshotPerAgentCap {
		t.Errorf("want at most %d snapshots per agent after cap, got %d", snapshotPerAgentCap, len(snaps))
	}
}

func TestPruneOldSnapshotsPerAgentCapIsolated(t *testing.T) {
	repo := openTestDB(t)

	// Two agents each with cap+5 rows. Cap should be enforced independently.
	base := time.Now().UTC().Add(-1 * time.Hour)
	for _, agent := range []string{"alice", "bob"} {
		for i := 0; i < snapshotPerAgentCap+5; i++ {
			mustSaveSnapshot(t, repo, "s", agent, base.Add(time.Duration(i)*time.Minute))
		}
	}

	cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour)
	if err := repo.PruneOldSnapshots(cutoff); err != nil {
		t.Fatalf("PruneOldSnapshots: %v", err)
	}

	for _, agent := range []string{"alice", "bob"} {
		snaps, err := repo.GetSnapshots("s", agent, nil)
		if err != nil {
			t.Fatalf("GetSnapshots(%s): %v", agent, err)
		}
		if len(snaps) > snapshotPerAgentCap {
			t.Errorf("agent %s: want at most %d snapshots, got %d", agent, snapshotPerAgentCap, len(snaps))
		}
	}
}

// ─── GetMessages LIMIT ────────────────────────────────────────────────────────

func TestGetMessagesLimit(t *testing.T) {
	repo := openTestDB(t)

	// Insert messageQueryLimit + 50 messages for the same agent.
	base := time.Now().UTC().Add(-1 * time.Hour)
	for i := 0; i < messageQueryLimit+50; i++ {
		repo.db.Create(&AgentMessage{
			ID:        fmt.Sprintf("msg-%05d", i),
			SpaceName: "s",
			AgentName: "agent",
			Sender:    "boss",
			Message:   fmt.Sprintf("message %d", i),
			Priority:  "info",
			Timestamp: base.Add(time.Duration(i) * time.Second),
		})
	}

	msgs, err := repo.GetMessages("s", "agent", nil)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) > messageQueryLimit {
		t.Errorf("GetMessages should return at most %d messages, got %d", messageQueryLimit, len(msgs))
	}
}

func TestGetMessagesSpaceFilter(t *testing.T) {
	repo := openTestDB(t)

	// Insert messages for two different spaces with the same agent name.
	repo.db.Create(&AgentMessage{
		ID: "msg-s1", SpaceName: "space1", AgentName: "agent",
		Sender: "boss", Message: "s1", Priority: "info", Timestamp: time.Now().UTC(),
	})
	repo.db.Create(&AgentMessage{
		ID: "msg-s2", SpaceName: "space2", AgentName: "agent",
		Sender: "boss", Message: "s2", Priority: "info", Timestamp: time.Now().UTC(),
	})

	msgs, err := repo.GetMessages("space1", "agent", nil)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Errorf("want 1 message for space1, got %d", len(msgs))
	}
	if len(msgs) == 1 && msgs[0].ID != "msg-s1" {
		t.Errorf("expected msg-s1, got %q", msgs[0].ID)
	}
}
