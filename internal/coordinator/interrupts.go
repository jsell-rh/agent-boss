package coordinator

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	bossdb "github.com/ambient/platform/components/boss/internal/coordinator/db"
)

type InterruptType string

const (
	InterruptDecision  InterruptType = "decision"
	InterruptApproval  InterruptType = "approval"
	InterruptStaleness InterruptType = "staleness"
	InterruptReview    InterruptType = "review"
	InterruptSequence  InterruptType = "sequencing"
)

type InterruptResolution struct {
	ResolvedBy   string    `json:"resolved_by,omitempty"`
	Answer       string    `json:"answer,omitempty"`
	ResolvedAt   time.Time `json:"resolved_at,omitempty"`
	WaitDuration float64   `json:"wait_seconds,omitempty"`
}

type Interrupt struct {
	ID         string              `json:"id"`
	Space      string              `json:"space"`
	Agent      string              `json:"agent"`
	Type       InterruptType       `json:"type"`
	Question   string              `json:"question"`
	Context    map[string]string   `json:"context,omitempty"`
	Resolution *InterruptResolution `json:"resolution,omitempty"`
	CreatedAt  time.Time           `json:"created_at"`
}

type InterruptMetrics struct {
	TotalInterrupts   int            `json:"total_interrupts"`
	HumanInterrupts   int            `json:"human_interrupts"`
	AutoResolved      int            `json:"auto_resolved"`
	PendingInterrupts int            `json:"pending_interrupts"`
	ByType            map[string]int `json:"by_type"`
	ByAgent           map[string]int `json:"by_agent"`
	AvgWaitSeconds    float64        `json:"avg_wait_seconds"`
}

type InterruptLedger struct {
	dataDir string
	mu      sync.Mutex
	seq     atomic.Int64
	repo    *bossdb.Repository // nil until SetRepo is called; enables SQLite persistence
}

func NewInterruptLedger(dataDir string) *InterruptLedger {
	l := &InterruptLedger{dataDir: dataDir}
	l.seq.Store(time.Now().UnixMilli())
	return l
}

// SetRepo injects the SQLite repository. Once set, all operations use SQLite
// instead of the JSONL file. Must be called before any concurrent use.
func (l *InterruptLedger) SetRepo(repo *bossdb.Repository) {
	l.repo = repo
}

func (l *InterruptLedger) nextID() string {
	n := l.seq.Add(1)
	return fmt.Sprintf("int_%d", n)
}

func (l *InterruptLedger) ledgerPath(space string) string {
	return filepath.Join(l.dataDir, space+".interrupts.jsonl")
}

func (l *InterruptLedger) Record(space, agent string, itype InterruptType, question string, ctx map[string]string) *Interrupt {
	intr := &Interrupt{
		ID:        l.nextID(),
		Space:     space,
		Agent:     agent,
		Type:      itype,
		Question:  question,
		Context:   ctx,
		CreatedAt: time.Now().UTC(),
	}
	l.save(intr)
	return intr
}

func (l *InterruptLedger) RecordResolved(space, agent string, itype InterruptType, question, resolvedBy, answer string, ctx map[string]string) *Interrupt {
	now := time.Now().UTC()
	intr := &Interrupt{
		ID:       l.nextID(),
		Space:    space,
		Agent:    agent,
		Type:     itype,
		Question: question,
		Context:  ctx,
		Resolution: &InterruptResolution{
			ResolvedBy: resolvedBy,
			Answer:     answer,
			ResolvedAt: now,
		},
		CreatedAt: now,
	}
	l.save(intr)
	return intr
}

// save persists an interrupt to SQLite (when repo is set) or appends to JSONL file.
func (l *InterruptLedger) save(intr *Interrupt) {
	if l.repo != nil {
		ctxJSON := ""
		if intr.Context != nil {
			if b, err := json.Marshal(intr.Context); err == nil {
				ctxJSON = string(b)
			}
		}
		rec := &bossdb.InterruptRecord{
			ID:        intr.ID,
			SpaceName: intr.Space,
			Agent:     intr.Agent,
			Type:      string(intr.Type),
			Question:  intr.Question,
			Context:   ctxJSON,
			CreatedAt: intr.CreatedAt,
		}
		if intr.Resolution != nil {
			rec.ResolvedBy = intr.Resolution.ResolvedBy
			rec.Answer = intr.Resolution.Answer
			rec.ResolvedAt = sql.NullTime{Time: intr.Resolution.ResolvedAt, Valid: true}
			rec.WaitSeconds = intr.Resolution.WaitDuration
		}
		l.repo.SaveInterrupt(rec)
		return
	}
	l.appendFile(intr)
}

func (l *InterruptLedger) appendFile(intr *Interrupt) {
	l.mu.Lock()
	defer l.mu.Unlock()

	data, err := json.Marshal(intr)
	if err != nil {
		return
	}

	f, err := os.OpenFile(l.ledgerPath(intr.Space), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(data)
	f.Write([]byte("\n"))
}

func (l *InterruptLedger) LoadAll(space string) []Interrupt {
	if l.repo != nil {
		recs, err := l.repo.LoadInterrupts(space)
		if err != nil {
			return nil
		}
		result := make([]Interrupt, 0, len(recs))
		for _, rec := range recs {
			result = append(result, dbRecordToInterrupt(rec))
		}
		return result
	}
	return l.loadAllFromFile(space)
}

func (l *InterruptLedger) loadAllFromFile(space string) []Interrupt {
	l.mu.Lock()
	defer l.mu.Unlock()

	f, err := os.Open(l.ledgerPath(space))
	if err != nil {
		return nil
	}
	defer f.Close()

	var interrupts []Interrupt
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 256*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var intr Interrupt
		if err := json.Unmarshal([]byte(line), &intr); err != nil {
			continue
		}
		interrupts = append(interrupts, intr)
	}
	return interrupts
}

// Resolve marks a pending interrupt as resolved.
func (l *InterruptLedger) Resolve(space, id, resolvedBy, answer string) error {
	if l.repo != nil {
		return l.repo.ResolveInterrupt(space, id, resolvedBy, answer)
	}
	return l.resolveInFile(space, id, resolvedBy, answer)
}

func (l *InterruptLedger) resolveInFile(space, id, resolvedBy, answer string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	path := l.ledgerPath(space)
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open ledger: %w", err)
	}

	var interrupts []Interrupt
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 256*1024)
	found := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var intr Interrupt
		if err := json.Unmarshal([]byte(line), &intr); err != nil {
			continue
		}
		if intr.ID == id && intr.Resolution == nil {
			now := time.Now().UTC()
			intr.Resolution = &InterruptResolution{
				ResolvedBy:   resolvedBy,
				Answer:       answer,
				ResolvedAt:   now,
				WaitDuration: now.Sub(intr.CreatedAt).Seconds(),
			}
			found = true
		}
		interrupts = append(interrupts, intr)
	}
	f.Close()

	if !found {
		return fmt.Errorf("interrupt %q not found or already resolved", id)
	}

	tmp := path + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	for _, intr := range interrupts {
		data, err := json.Marshal(intr)
		if err != nil {
			continue
		}
		out.Write(data)
		out.Write([]byte("\n"))
	}
	out.Close()
	return os.Rename(tmp, path)
}

// dbRecordToInterrupt converts a db.InterruptRecord to the coordinator Interrupt type.
func dbRecordToInterrupt(rec *bossdb.InterruptRecord) Interrupt {
	intr := Interrupt{
		ID:        rec.ID,
		Space:     rec.SpaceName,
		Agent:     rec.Agent,
		Type:      InterruptType(rec.Type),
		Question:  rec.Question,
		CreatedAt: rec.CreatedAt,
	}
	if rec.Context != "" {
		var ctx map[string]string
		if json.Unmarshal([]byte(rec.Context), &ctx) == nil {
			intr.Context = ctx
		}
	}
	if rec.ResolvedAt.Valid {
		intr.Resolution = &InterruptResolution{
			ResolvedBy:   rec.ResolvedBy,
			Answer:       rec.Answer,
			ResolvedAt:   rec.ResolvedAt.Time,
			WaitDuration: rec.WaitSeconds,
		}
	}
	return intr
}

func (l *InterruptLedger) Metrics(space string) InterruptMetrics {
	all := l.LoadAll(space)
	m := InterruptMetrics{
		ByType:  make(map[string]int),
		ByAgent: make(map[string]int),
	}
	m.TotalInterrupts = len(all)

	var totalWait float64
	var resolvedCount int

	for _, intr := range all {
		m.ByType[string(intr.Type)]++
		m.ByAgent[intr.Agent]++
		if intr.Resolution != nil {
			resolvedCount++
			if intr.Resolution.ResolvedBy == "human" {
				m.HumanInterrupts++
			} else {
				m.AutoResolved++
			}
			if intr.Resolution.WaitDuration > 0 {
				totalWait += intr.Resolution.WaitDuration
			}
		} else {
			m.PendingInterrupts++
		}
	}

	if resolvedCount > 0 {
		m.AvgWaitSeconds = totalWait / float64(resolvedCount)
	}
	return m
}
