package coordinator

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// taskRefRe matches TASK-NNN references in agent text (e.g. TASK-001, TASK-42).
var taskRefRe = regexp.MustCompile(`\bTASK-\d+\b`)

// linkifyTaskRefs replaces TASK-NNN tokens in text with markdown links to the
// task detail endpoint. Only known task IDs are linked; unrecognized IDs are
// left unchanged.
func linkifyTaskRefs(text, spaceName string, tasks map[string]*Task) string {
	if len(tasks) == 0 || !taskRefRe.MatchString(text) {
		return text
	}
	return taskRefRe.ReplaceAllStringFunc(text, func(ref string) string {
		task, ok := tasks[ref]
		if !ok {
			return ref
		}
		return fmt.Sprintf("[%s: %s](/spaces/%s/tasks/%s)", ref, task.Title, spaceName, ref)
	})
}

// HierarchyTree is the full agent hierarchy for a space, computed on demand.
type HierarchyTree struct {
	Space string                     `json:"space"`
	Roots []string                   `json:"roots"` // agents with no parent
	Nodes map[string]*HierarchyNode  `json:"nodes"`
}

// HierarchyNode is one agent's position in the hierarchy tree.
type HierarchyNode struct {
	Agent    string   `json:"agent"`
	Parent   string   `json:"parent,omitempty"`
	Children []string `json:"children"`
	Depth    int      `json:"depth"` // 0 = root
	Role     string   `json:"role,omitempty"`
}

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
	SessionID      string          `json:"session_id,omitempty"`
	BackendType    string          `json:"backend_type,omitempty"`
	// ## TODO - REMOVE ME — backward compat for agents still posting "tmux_session" ## TODO
	DeprecatedTmuxSession string `json:"tmux_session,omitempty"`
	RepoURL        string          `json:"repo_url,omitempty"`
	Messages       []AgentMessage      `json:"messages,omitempty"`
	Notifications  []AgentNotification `json:"notifications,omitempty"`
	UpdatedAt      time.Time           `json:"updated_at"`

	// Hierarchy fields — optional. If Parent is empty, agent is a root node.
	// Parent is sticky (mutable): set via status POST or /register; omitting does not clear it.
	// Children is server-managed: computed by rebuildChildren(), never set by agents.
	Parent   string   `json:"parent,omitempty"`
	Children []string `json:"children,omitempty"`
	Role     string   `json:"role,omitempty"` // display label: "manager", "worker", "sme", etc.

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

// NotificationType identifies why an agent is being notified.
type NotificationType string

const (
	NotifTypeMessage     NotificationType = "message"         // new message from another agent
	NotifTypeTaskAssign  NotificationType = "task_assigned"   // task assigned to this agent
	NotifTypeTaskComment NotificationType = "task_commented"  // someone commented on agent's task
)

// AgentNotification is a typed notification surfaced to an agent explaining
// why it was woken up. Notifications render at the top of the agent's /raw
// section so agents see them immediately on check-in.
type AgentNotification struct {
	ID        string           `json:"id"`
	Type      NotificationType `json:"type"`
	Title     string           `json:"title"`        // e.g. "New message from Cto"
	Body      string           `json:"body"`         // truncated preview or task title
	From      string           `json:"from,omitempty"`
	TaskID    string           `json:"task_id,omitempty"`
	Timestamp time.Time        `json:"timestamp"`
	Read      bool             `json:"read,omitempty"`
}

// MessagePriority indicates the urgency of a message to an agent.
type MessagePriority string

const (
	PriorityInfo      MessagePriority = "info"
	PriorityDirective MessagePriority = "directive"
	PriorityUrgent    MessagePriority = "urgent"
)

type AgentMessage struct {
	ID        string          `json:"id"`
	Message   string          `json:"message"`
	Sender    string          `json:"sender"`
	Priority  MessagePriority `json:"priority,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
	Read      bool            `json:"read,omitempty"`
	ReadAt    *time.Time      `json:"read_at,omitempty"`
}

type Table struct {
	Headers []string   `json:"headers"`
	Rows    [][]string `json:"rows"`
}

// TaskStatus is the Kanban column a task occupies.
type TaskStatus string

const (
	TaskStatusBacklog    TaskStatus = "backlog"
	TaskStatusInProgress TaskStatus = "in_progress"
	TaskStatusReview     TaskStatus = "review"
	TaskStatusDone       TaskStatus = "done"
	TaskStatusBlocked    TaskStatus = "blocked"
)

func (ts TaskStatus) Valid() bool {
	switch ts {
	case TaskStatusBacklog, TaskStatusInProgress, TaskStatusReview, TaskStatusDone, TaskStatusBlocked:
		return true
	}
	return false
}

// TaskPriority controls visual ordering and filtering on the board.
type TaskPriority string

const (
	TaskPriorityLow    TaskPriority = "low"
	TaskPriorityMedium TaskPriority = "medium"
	TaskPriorityHigh   TaskPriority = "high"
	TaskPriorityUrgent TaskPriority = "urgent"
)

// Task is the canonical unit of tracked work within a KnowledgeSpace.
type Task struct {
	ID          string       `json:"id"`
	Space       string       `json:"space"`
	Title       string       `json:"title"`
	Description string       `json:"description,omitempty"`
	Status      TaskStatus   `json:"status"`
	Priority    TaskPriority `json:"priority,omitempty"`

	// Assignment
	AssignedTo string `json:"assigned_to,omitempty"`
	CreatedBy  string `json:"created_by"`

	// Relationships
	Labels     []string `json:"labels,omitempty"`
	ParentTask string   `json:"parent_task,omitempty"`
	Subtasks   []string `json:"subtasks,omitempty"`

	// Cross-system links
	LinkedBranch string `json:"linked_branch,omitempty"`
	LinkedPR     string `json:"linked_pr,omitempty"`

	// Timestamps
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DueAt     *time.Time `json:"due_at,omitempty"`

	// Activity
	Comments []TaskComment `json:"comments,omitempty"`
	Events   []TaskEvent   `json:"events,omitempty"`

	// Server-computed fields (not stored, computed at read time)
	IsStale bool `json:"is_stale,omitempty"` // true when in_progress and not updated for >1h
}

// TaskComment is a human or agent note on a task.
type TaskComment struct {
	ID        string    `json:"id"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// TaskEvent records a point-in-time change to a task for display in the event history.
type TaskEvent struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`   // "created", "moved", "assigned", "commented", "updated"
	By        string    `json:"by"`     // agent name who caused the event
	Detail    string    `json:"detail"` // human-readable description
	CreatedAt time.Time `json:"created_at"`
}

type KnowledgeSpace struct {
	Name            string                  `json:"name"`
	Agents          map[string]*AgentUpdate `json:"agents"`
	Tasks           map[string]*Task        `json:"tasks,omitempty"`
	NextTaskSeq     int                     `json:"next_task_seq,omitempty"`
	SharedContracts string                  `json:"shared_contracts,omitempty"`
	Archive         string                  `json:"archive,omitempty"`
	CreatedAt       time.Time               `json:"created_at"`
	UpdatedAt       time.Time               `json:"updated_at"`
}

// snapshot returns a deep copy of ks via JSON round-trip.
// Use to safely pass ks data to saveSpace outside of s.mu.
func (ks *KnowledgeSpace) snapshot() *KnowledgeSpace {
	b, _ := json.Marshal(ks)
	var snap KnowledgeSpace
	_ = json.Unmarshal(b, &snap)
	return &snap
}

func NewKnowledgeSpace(name string) *KnowledgeSpace {
	now := time.Now().UTC()
	return &KnowledgeSpace{
		Name:      name,
		Agents:    make(map[string]*AgentUpdate),
		Tasks:     make(map[string]*Task),
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
		b.WriteString(renderAgentSection(name, agent, ks.Name, ks.Tasks))
		b.WriteString("\n")
	}

	if ks.Archive != "" {
		b.WriteString("---\n\n## Archive\n\n")
		b.WriteString(ks.Archive)
		b.WriteString("\n")
	}

	return b.String()
}

func renderAgentSection(name string, agent *AgentUpdate, spaceName string, tasks map[string]*Task) string {
	var b strings.Builder

	summary := linkifyTaskRefs(agent.Summary, spaceName, tasks)
	b.WriteString(fmt.Sprintf("[%s] %s — **%s**",
		name, agent.UpdatedAt.Format("2006-01-02 15:04"), summary))
	if agent.TestCount != nil {
		b.WriteString(fmt.Sprintf(" %d tests.", *agent.TestCount))
	}
	b.WriteString("\n\n")

	for _, item := range agent.Items {
		b.WriteString("- ")
		b.WriteString(linkifyTaskRefs(item, spaceName, tasks))
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
		b.WriteString(linkifyTaskRefs(agent.NextSteps, spaceName, tasks))
		b.WriteString("\n\n")
	}

	if agent.FreeText != "" {
		b.WriteString(agent.FreeText)
		b.WriteString("\n\n")
	}

	// Render unread notifications before Messages so agents immediately see why they were woken up.
	unreadNotifs := make([]AgentNotification, 0)
	for _, n := range agent.Notifications {
		if !n.Read {
			unreadNotifs = append(unreadNotifs, n)
		}
	}
	if len(unreadNotifs) > 0 {
		b.WriteString("#### Notifications\n\n")
		for _, n := range unreadNotifs {
			b.WriteString(fmt.Sprintf("- [!] [%s] %s (%s): %s\n",
				string(n.Type), n.Title, n.Timestamp.Format("15:04"), n.Body))
		}
		b.WriteString("\n")
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

// BuildHierarchyTree computes the hierarchy tree for a KnowledgeSpace on demand.
// Must be called with ks read-accessible (caller holds s.mu.RLock or s.mu.Lock).
func BuildHierarchyTree(ks *KnowledgeSpace) *HierarchyTree {
	tree := &HierarchyTree{
		Space: ks.Name,
		Roots: []string{},
		Nodes: make(map[string]*HierarchyNode),
	}

	// Build all nodes
	for name, ag := range ks.Agents {
		node := &HierarchyNode{
			Agent:    name,
			Parent:   ag.Parent,
			Children: make([]string, len(ag.Children)),
			Role:     ag.Role,
		}
		copy(node.Children, ag.Children)
		tree.Nodes[name] = node
	}

	// Compute depths via BFS from roots
	for name, node := range tree.Nodes {
		if node.Parent == "" {
			tree.Roots = append(tree.Roots, name)
			node.Depth = 0
		}
	}
	sort.Strings(tree.Roots)

	// BFS to set depth for non-root nodes
	visited := make(map[string]bool)
	queue := make([]string, len(tree.Roots))
	copy(queue, tree.Roots)
	for _, r := range tree.Roots {
		visited[r] = true
	}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		node := tree.Nodes[current]
		for _, child := range node.Children {
			if !visited[child] {
				visited[child] = true
				if cn, ok := tree.Nodes[child]; ok {
					cn.Depth = node.Depth + 1
				}
				queue = append(queue, child)
			}
		}
	}

	return tree
}

// rebuildChildren recomputes all Children slices by inverting the Parent fields.
// Must be called inside s.mu.Lock().
func rebuildChildren(ks *KnowledgeSpace) {
	// Reset all children slices
	for _, ag := range ks.Agents {
		ag.Children = nil
	}
	// Populate from Parent fields
	for name, ag := range ks.Agents {
		if ag.Parent == "" {
			continue
		}
		canonicalParent := resolveAgentName(ks, ag.Parent)
		if parent, ok := ks.Agents[canonicalParent]; ok {
			parent.Children = append(parent.Children, name)
		}
	}
	// Sort children for stable output
	for _, ag := range ks.Agents {
		sort.Strings(ag.Children)
	}
}

// hasCycle returns true if assigning proposedParent as the parent of agentName
// would create a cycle. Must be called inside s.mu.Lock().
func hasCycle(ks *KnowledgeSpace, agentName, proposedParent string) bool {
	if proposedParent == "" {
		return false
	}
	target := strings.ToLower(agentName)
	visited := make(map[string]bool)
	current := strings.ToLower(proposedParent)
	for current != "" {
		if current == target {
			return true
		}
		if visited[current] {
			break
		}
		visited[current] = true
		canonical := resolveAgentName(ks, current)
		ag, ok := ks.Agents[canonical]
		if !ok {
			break // dangling reference — no cycle through here
		}
		current = strings.ToLower(ag.Parent)
	}
	return false
}

// collectSubtree returns agentName plus all its descendants (BFS order).
// Must be called inside s.mu.Lock() or s.mu.RLock().
func collectSubtree(ks *KnowledgeSpace, agentName string) []string {
	var result []string
	visited := make(map[string]bool)
	queue := []string{agentName}
	visited[agentName] = true
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		result = append(result, current)
		ag, ok := ks.Agents[current]
		if !ok {
			continue
		}
		for _, child := range ag.Children {
			if !visited[child] {
				visited[child] = true
				queue = append(queue, child)
			}
		}
	}
	return result
}

// StatusSnapshot is a point-in-time record of an agent's status.
// Snapshots are appended to data/{space}-history.json on every agent
// status change and on periodic liveness loop ticks.
type StatusSnapshot struct {
	AgentName      string      `json:"agent_name"`
	Space          string      `json:"space"`
	Status         AgentStatus `json:"status"`
	InferredStatus string      `json:"inferred_status,omitempty"`
	Stale          bool        `json:"stale,omitempty"`
	Timestamp      time.Time   `json:"timestamp"`
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

// TaskStalenessThreshold is how long an in_progress task must be un-updated
// before it is flagged as stale.
const TaskStalenessThreshold = 1 * time.Hour

// computeTaskStaleness sets t.IsStale based on status and last update time.
// Call this on a copy before returning a task in an API response.
func computeTaskStaleness(t *Task) {
	t.IsStale = t.Status == TaskStatusInProgress && time.Since(t.UpdatedAt) > TaskStalenessThreshold
}
