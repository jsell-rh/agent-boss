package coordinator

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type AgentStatus string

const (
	StatusActive  AgentStatus = "active"
	StatusBlocked AgentStatus = "blocked"
	StatusDone    AgentStatus = "done"
	StatusIdle    AgentStatus = "idle"
	StatusError   AgentStatus = "error"
)

func (s AgentStatus) Valid() bool {
	switch s {
	case StatusActive, StatusBlocked, StatusDone, StatusIdle, StatusError:
		return true
	}
	return false
}

func (s AgentStatus) Emoji() string {
	switch s {
	case StatusActive:
		return "🟢"
	case StatusBlocked:
		return "🔴"
	case StatusDone:
		return "✅"
	case StatusIdle:
		return "⏸️"
	case StatusError:
		return "❌"
	}
	return "❓"
}

type AgentUpdate struct {
	Status         AgentStatus     `json:"status"`
	Summary        string          `json:"summary"`
	Branch         string          `json:"branch,omitempty"`
	Worktree       string          `json:"worktree,omitempty"`
	PR             string          `json:"pr,omitempty"`
	Phase          string          `json:"phase,omitempty"`
	TestCount      *int            `json:"test_count,omitempty"`
	Items          []string        `json:"items,omitempty"`
	Sections       []Section       `json:"sections,omitempty"`
	Questions      []string        `json:"questions,omitempty"`
	Blockers       []string        `json:"blockers,omitempty"`
	NextSteps      string          `json:"next_steps,omitempty"`
	FreeText       string          `json:"free_text,omitempty"`
	Documents      []AgentDocument `json:"documents,omitempty"`
	TmuxSession    string          `json:"tmux_session,omitempty"`
	RepoURL        string          `json:"repo_url,omitempty"`
	Messages       []AgentMessage  `json:"messages,omitempty"`
	UpdatedAt      time.Time       `json:"updated_at"`

	// Server-inferred fields (not set by agents themselves)
	InferredStatus string          `json:"inferred_status,omitempty"`
	Stale          bool            `json:"stale,omitempty"`

	// Protocol registration fields — preserved across status updates (sticky).
	Registration   *AgentRegistration `json:"registration,omitempty"`
	LastHeartbeat  time.Time          `json:"last_heartbeat,omitempty"`
	HeartbeatStale bool               `json:"heartbeat_stale,omitempty"`
}

type Section struct {
	Title string   `json:"title"`
	Items []string `json:"items,omitempty"`
	Table *Table   `json:"table,omitempty"`
}

type AgentDocument struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

// MessagePriority indicates the urgency of a message to an agent.
type MessagePriority string

const (
	PriorityInfo      MessagePriority = "info"
	PriorityDirective MessagePriority = "directive"
	PriorityUrgent    MessagePriority = "urgent"
)

// StalenessThreshold is the duration after which an agent that has not
// self-reported is considered stale.
const StalenessThreshold = 15 * time.Minute

type AgentMessage struct {
	ID        string          `json:"id"`
	Message   string          `json:"message"`
	Sender    string          `json:"sender"`
	Priority  MessagePriority `json:"priority,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
}

type Table struct {
	Headers []string   `json:"headers"`
	Rows    [][]string `json:"rows"`
}

type KnowledgeSpace struct {
	Name            string                  `json:"name"`
	Agents          map[string]*AgentUpdate `json:"agents"`
	SharedContracts string                  `json:"shared_contracts,omitempty"`
	Archive         string                  `json:"archive,omitempty"`
	CreatedAt       time.Time               `json:"created_at"`
	UpdatedAt       time.Time               `json:"updated_at"`
}

func NewKnowledgeSpace(name string) *KnowledgeSpace {
	now := time.Now().UTC()
	return &KnowledgeSpace{
		Name:      name,
		Agents:    make(map[string]*AgentUpdate),
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func (ks *KnowledgeSpace) RenderMarkdown() string {
	var b strings.Builder

	b.WriteString("# ")
	b.WriteString(ks.Name)
	b.WriteString("\n\n")

	b.WriteString("## Session Dashboard\n\n")
	b.WriteString("| **Agent** | **Status** | **Branch** | **PR** |\n")
	b.WriteString("| --------- | ---------- | ---------- | ------ |\n")

	sortedNames := make([]string, 0, len(ks.Agents))
	for name := range ks.Agents {
		sortedNames = append(sortedNames, name)
	}
	sort.Strings(sortedNames)

	for _, name := range sortedNames {
		agent := ks.Agents[name]
		branch := agent.Branch
		if branch == "" {
			branch = "—"
		}
		pr := agent.PR
		if pr == "" {
			pr = "—"
		}
		b.WriteString(fmt.Sprintf("| %s | %s %s | %s | %s |\n",
			name, agent.Status.Emoji(), agent.Status, branch, pr))
	}
	b.WriteString("\n---\n\n")

	if ks.SharedContracts != "" {
		b.WriteString("## Shared Contracts\n\n")
		b.WriteString(ks.SharedContracts)
		b.WriteString("\n\n---\n\n")
	}

	b.WriteString("## Agent Sections\n\n")
	for _, name := range sortedNames {
		agent := ks.Agents[name]
		b.WriteString("### ")
		b.WriteString(name)
		b.WriteString("\n\n")
		b.WriteString(renderAgentSection(name, agent))
		b.WriteString("\n")
	}

	if ks.Archive != "" {
		b.WriteString("---\n\n## Archive\n\n")
		b.WriteString(ks.Archive)
		b.WriteString("\n")
	}

	return b.String()
}

func renderAgentSection(name string, agent *AgentUpdate) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("[%s] %s — **%s**",
		name, agent.UpdatedAt.Format("2006-01-02 15:04"), agent.Summary))
	if agent.TestCount != nil {
		b.WriteString(fmt.Sprintf(" %d tests.", *agent.TestCount))
	}
	b.WriteString("\n\n")

	for _, item := range agent.Items {
		b.WriteString("- ")
		b.WriteString(item)
		b.WriteString("\n")
	}
	if len(agent.Items) > 0 {
		b.WriteString("\n")
	}

	for _, sec := range agent.Sections {
		b.WriteString("#### ")
		b.WriteString(sec.Title)
		b.WriteString("\n\n")
		for _, item := range sec.Items {
			b.WriteString("- ")
			b.WriteString(item)
			b.WriteString("\n")
		}
		if sec.Table != nil {
			b.WriteString(renderTable(sec.Table))
		}
		b.WriteString("\n")
	}

	if len(agent.Questions) > 0 {
		b.WriteString("#### Questions\n\n")
		for _, q := range agent.Questions {
			b.WriteString("- [?BOSS] ")
			b.WriteString(q)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if len(agent.Blockers) > 0 {
		b.WriteString("#### Blockers\n\n")
		for _, bl := range agent.Blockers {
			b.WriteString("- 🔴 ")
			b.WriteString(bl)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if agent.NextSteps != "" {
		b.WriteString(agent.NextSteps)
		b.WriteString("\n\n")
	}

	if agent.FreeText != "" {
		b.WriteString(agent.FreeText)
		b.WriteString("\n\n")
	}

	if len(agent.Messages) > 0 {
		b.WriteString("#### Messages\n\n")
		for _, msg := range agent.Messages {
			b.WriteString(fmt.Sprintf("- **%s** (%s): %s\n",
				msg.Sender, msg.Timestamp.Format("15:04"), msg.Message))
		}
		b.WriteString("\n")
	}

	if len(agent.Documents) > 0 {
		b.WriteString("#### Documents\n\n")
		for _, doc := range agent.Documents {
			b.WriteString(fmt.Sprintf("- [%s](./%s/%s)\n", doc.Title, name, doc.Slug))
		}
		b.WriteString("\n")
	}

	return b.String()
}

func renderTable(t *Table) string {
	if len(t.Headers) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("| ")
	b.WriteString(strings.Join(t.Headers, " | "))
	b.WriteString(" |\n| ")
	for i := range t.Headers {
		if i > 0 {
			b.WriteString(" | ")
		}
		b.WriteString("---")
	}
	b.WriteString(" |\n")
	for _, row := range t.Rows {
		b.WriteString("| ")
		padded := make([]string, len(t.Headers))
		copy(padded, row)
		b.WriteString(strings.Join(padded, " | "))
		b.WriteString(" |\n")
	}
	return b.String()
}

func (u *AgentUpdate) Validate() error {
	if !u.Status.Valid() {
		return fmt.Errorf("invalid status %q: must be one of active, blocked, done, idle, error", u.Status)
	}
	if strings.TrimSpace(u.Summary) == "" {
		return fmt.Errorf("summary is required")
	}
	return nil
}
