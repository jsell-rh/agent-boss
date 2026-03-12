// Package coordinator_test contains taste-invariant linters that enforce
// code quality conventions for the coordinator package. These tests run as
// part of `go test ./internal/coordinator/...` and fail loud with remediation
// instructions so that agents know exactly what to fix.
//
// Four invariants are checked:
//  1. No fmt.Print* in server code — use the structured logger instead.
//  2. No new .go files in coordinator exceeding 600 lines.
//  3. HTTP handler functions follow the handle{Noun}{Verb} naming convention.
//  4. TmuxCreateOpts literals that set MCPServerURL must also set AgentToken.
package coordinator_test

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// coordinatorDir returns the absolute path to internal/coordinator/.
func coordinatorDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(thisFile)
}

// goFiles returns all non-test .go files in dir (non-recursive).
func goFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			files = append(files, filepath.Join(dir, name))
		}
	}
	return files
}

// linesOf counts lines in a file.
func linesOf(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	count := 0
	for sc.Scan() {
		count++
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return count
}

// TestNoFmtPrintInServerCode verifies that no production coordinator code uses
// fmt.Print*, fmt.Println*, or fmt.Fprintf to stderr/stdout. All logging must go
// through the structured logger (log.Info, log.Error, log.Warn, etc.).
//
// TO FIX: Replace fmt.Printf("...") with log.Info("...") or log.Error("...").
// Import "log/slog" and use slog.Default() if the local logger is not available.
func TestNoFmtPrintInServerCode(t *testing.T) {
	dir := coordinatorDir(t)
	files := goFiles(t, dir)

	// Matches fmt.Print*, fmt.Println*, fmt.Printf* — functions that write to stdout.
	// Does NOT match fmt.Sprintf/Sprint (string formatting) or fmt.Fprintf/Fprint
	// (writer-based, used legitimately for HTTP responses).
	// Also catches fmt.Fprintf(os.Stderr/os.Stdout) — explicit stderr/stdout writes.
	fmtPrintRe := regexp.MustCompile(`\bfmt\.(Print|Println|Printf)\(|\bfmt\.Fprintf\(os\.(Stderr|Stdout)\b`)

	for _, file := range files {
		f, err := os.Open(file)
		if err != nil {
			t.Fatalf("open %s: %v", file, err)
		}
		sc := bufio.NewScanner(f)
		lineNum := 0
		for sc.Scan() {
			lineNum++
			line := sc.Text()
			trimmed := strings.TrimSpace(line)
			// Skip comment lines
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if fmtPrintRe.MatchString(line) {
				t.Errorf(
					"LINT FAIL [fmt.Print*]: %s:%d\n"+
						"  Found: %s\n"+
						"  Rule: coordinator code must use the structured logger, not fmt.Print*.\n"+
						"  Fix: Replace with log.Info(\"message\", \"key\", value) or log.Error(\"message\", \"err\", err).\n"+
						"  Import: \"log/slog\" and use slog.Default() if no local logger is available.",
					filepath.Base(file), lineNum, strings.TrimSpace(line),
				)
			}
		}
		f.Close()
		if err := sc.Err(); err != nil {
			t.Fatalf("scan %s: %v", file, err)
		}
	}
}

// TestFileSizeLimit verifies that no new .go file in internal/coordinator/
// exceeds 600 lines. Files grandfathered below are known tech debt.
//
// TO FIX: Split the file into focused sub-files. Suggested approach:
//   - Extract handler functions into handlers_<noun>.go files
//   - Extract pure helper/util logic into helpers_<topic>.go files
//   - Each file should own one coherent responsibility
//
// When adding a file to the grandfather list, include a comment with:
//   - The line count at the time of grandfathering
//   - A TASK reference for the cleanup work
const fileSizeLimit = 600

// grandfatheredLargeFiles lists files that exceeded 600 lines when this linter
// was introduced (2026-03). Each entry documents the count at grandfathering time
// and a reference to the cleanup task. New files must not be added here without
// a corresponding task in the backlog.
//
// To grandfather a file: add it to this map with its current line count and task reference.
// Do NOT add files here as a shortcut — create a cleanup task first.
var grandfatheredLargeFiles = map[string]string{
	// 1680 lines at grandfathering — TASK-013 tech debt, awaiting handler extraction
	"handlers_agent.go": "1680 lines — handler extraction planned (see TASK-013)",
	// 1104 lines at grandfathering — TASK-013 tech debt, MCP tools need own package
	"mcp_tools.go": "1104 lines — MCP tool extraction planned (see TASK-013)",
	// 887 lines at grandfathering — TASK-013 tech debt, task handler extraction
	"handlers_task.go": "887 lines — task handler extraction planned (see TASK-013)",
	// 879 lines at grandfathering — TASK-013 tech debt, lifecycle management
	"lifecycle.go": "879 lines — lifecycle extraction planned (see TASK-013)",
	// 802 lines at grandfathering — TASK-013 tech debt, types consolidation
	"types.go": "802 lines — types consolidation planned (see TASK-013)",
	// 723 lines at grandfathering — TASK-013 tech debt, tmux backend
	"tmux.go": "723 lines — tmux backend extraction planned (see TASK-013)",
}

func TestFileSizeLimit(t *testing.T) {
	dir := coordinatorDir(t)
	files := goFiles(t, dir)

	for _, file := range files {
		base := filepath.Base(file)
		lines := linesOf(t, file)

		if lines <= fileSizeLimit {
			continue
		}

		if note, ok := grandfatheredLargeFiles[base]; ok {
			t.Logf("LINT WARN [file-size]: %s has %d lines (grandfathered: %s)", base, lines, note)
			continue
		}

		t.Errorf(
			"LINT FAIL [file-size]: %s has %d lines (limit: %d)\n"+
				"  Rule: No new .go file in internal/coordinator/ may exceed %d lines.\n"+
				"  Fix: Split into focused files (e.g. handlers_<noun>.go, helpers_<topic>.go).\n"+
				"  If this is unavoidable tech debt, add it to grandfatheredLargeFiles in lint_test.go\n"+
				"  with a comment referencing the cleanup task.",
			base, lines, fileSizeLimit, fileSizeLimit,
		)
	}
}

// TestHandlerNaming verifies that HTTP handler methods on *Server follow the
// handle{Noun}{Verb} naming convention (e.g. handleAgentGet, handleTaskCreate).
//
// Rationale: Consistent naming makes handlers grep-able and self-documenting.
// The noun says what resource is being operated on; the verb says what operation.
//
// TO FIX: Rename the handler to follow handle{Noun}{Verb}:
//   - handleListSpaces   → handleSpaceList
//   - handleDeleteSpace  → handleSpaceDelete
//   - handleApproveAgent → handleAgentApprove
//   - handleCreateAgents → handleAgentCreate
//   - handleReplyAgent   → handleAgentReply
//   - handleDismissQuestion → handleQuestionDismiss
//
// Grandfathered handlers below are known naming debt. New handlers added after
// this linter was introduced (2026-03) MUST follow the convention.
var grandfatheredHandlers = map[string]string{
	// Route dispatchers and top-level handlers — no clear noun/verb split
	"handleRoot":       "top-level SPA fallback — no noun/verb applicable",
	"handleSSE":        "SSE is a protocol acronym, not a noun/verb pair",
	"handleSpaceRoute": "route dispatcher — dispatches to sub-handlers",
	"handleSettings":   "settings has no sub-resources yet — rename to handleSettingsGet when expanded",

	// Verb-first handlers (should be handle{Noun}{Verb}) — TASK-013 naming debt
	"handleListSpaces":   "should be handleSpaceList — verb-first naming debt (TASK-013)",
	"handleDeleteSpace":  "should be handleSpaceDelete — verb-first naming debt (TASK-013)",
	"handleApproveAgent": "should be handleAgentApprove — verb-first naming debt (TASK-013)",
	"handleReplyAgent":   "should be handleAgentReply — verb-first naming debt (TASK-013)",
	"handleDismissQuestion":  "should be handleQuestionDismiss — verb-first naming debt (TASK-013)",
	"handleCreateAgents": "should be handleAgentCreate — verb-first naming debt (TASK-013)",

	// Compound/special-purpose handlers — no simple noun/verb decomposition
	"handleSpaceAgent":          "route dispatcher for /spaces/:space/agents/:agent — compound route",
	"handleSpaceJSON":           "should be handleSpaceGet (JSON format) — naming debt (TASK-013)",
	"handleSpaceHierarchy":      "should be handleSpaceHierarchyGet — naming debt (TASK-013)",
	"handleSpaceTextField":      "internal sub-handler for text fields — not a top-level HTTP route",
	"handleSpaceRaw":            "should be handleSpaceRawGet — naming debt (TASK-013)",
	"handleSpaceContracts":      "should be handleSpaceContractList — naming debt (TASK-013)",
	"handleSpaceContractsDefault": "should be handleSpaceContractDefault — naming debt (TASK-013)",
	"handleSpaceAgentsJSON":     "should be handleAgentList — naming debt (TASK-013)",
	"handleSpaceEventsJSON":     "should be handleEventList — naming debt (TASK-013)",
	"handleSpaceSessionStatus":  "should be handleSessionStatusGet — naming debt (TASK-013)",
	"handleSpaceSSE":            "SSE is a protocol acronym — should be handleSpaceStream (TASK-013)",
	"handleSingleBroadcast":     "should be handleAgentBroadcast — naming debt (TASK-013)",
	"handleBroadcast":           "should be handleSpaceBroadcast — naming debt (TASK-013)",
	"handleIgnition":            "should be handleAgentIgnite — naming debt (TASK-013)",

	// Persona handlers — prefix is correct but suffix doesn't follow verb pattern
	"handlePersonaDetail":           "should be handlePersonaGet — naming debt (TASK-013)",
	"handlePersonaHistory":          "should be handlePersonaHistoryList — naming debt (TASK-013)",
	"handlePersonaAgents":           "should be handlePersonaAgentList — naming debt (TASK-013)",
	"handlePersonaRestartOutdated":  "compound — acceptable for specificity, consider handlePersonaOutdatedRestart",

	// Interrupt handlers — no noun/verb decomposition
	"handleInterrupts":       "should be handleInterruptList — naming debt (TASK-013)",
	"handleInterruptMetrics": "should be handleInterruptMetricGet — naming debt (TASK-013)",

	// Task sub-handlers and route dispatchers
	"handleTaskCreateSubtask": "compound verb — acceptable for specificity; consider handleSubtaskCreate",
	"handleSpaceTasks":        "should be handleTaskList — verb/noun naming debt (TASK-013)",

	// History handlers — History is a noun, not a verb
	"handleSpaceHistory": "should be handleSpaceHistoryList — noun suffix not a verb (TASK-013)",
	"handleAgentHistory": "should be handleAgentHistoryList — noun suffix not a verb (TASK-013)",

	// Lifecycle handlers with non-verb suffixes
	"handleRestartAll":   "should be handleAgentRestartAll or handleSpaceRestart — verb-first, missing noun (TASK-013)",
	"handleAgentHeartbeat": "Heartbeat is a noun; should be handleAgentPing or handleAgentHeartbeatPost (TASK-013)",
	"handleAgentMessages":  "Messages is a plural noun; should be handleAgentMessageList (TASK-013)",
	"handleAgentSSE":       "SSE is an acronym; should be handleAgentStream (TASK-013)",

	// handlers_agent.go — agent config (Config used as noun not verb)
	"handleAgentConfig": "Config is ambiguous noun/verb — acceptable; consider handleAgentConfigure (TASK-013)",
}

// handlerFuncRe matches handler method declarations on *Server.
var handlerFuncRe = regexp.MustCompile(`^func \(s \*Server\) (handle[A-Z][a-zA-Z]+)\(`)

// handlerNounVerbRe matches the approved handle{Noun}{Verb} pattern.
// Noun: one or more title-case words (e.g. Task, SpaceAgent)
// Verb: a known action verb at the end of the name
var handlerNounVerbRe = regexp.MustCompile(
	`^handle[A-Z][a-zA-Z]+(Create|List|Get|Update|Delete|Move|Assign|Comment|Ack|Approve|` +
		`Reply|Dismiss|Duplicate|Archive|View|Revert|Restart|Send|Post|Put|Patch|` +
		`Config|Stream|Ignite|Broadcast|Export|Import|Publish|Subscribe|` +
		`Lock|Unlock|Enable|Disable|Reset|Flush|Sync|Poll|Ping|` +
		`Spawn|Stop|Interrupt|Introspect|Register|Message|Document|` +
		`Activate|Deactivate|Start|Cancel|Retry|Attach|Detach|Check|Submit|Execute)$`,
)

func TestHandlerNaming(t *testing.T) {
	dir := coordinatorDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		filePath := filepath.Join(dir, e.Name())
		f, err := os.Open(filePath)
		if err != nil {
			t.Fatalf("open %s: %v", filePath, err)
		}

		sc := bufio.NewScanner(f)
		lineNum := 0
		for sc.Scan() {
			lineNum++
			line := sc.Text()
			m := handlerFuncRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			name := m[1]

			if note, ok := grandfatheredHandlers[name]; ok {
				t.Logf("LINT WARN [handler-naming]: %s:%d — %s (grandfathered: %s)",
					e.Name(), lineNum, name, note)
				continue
			}

			if !handlerNounVerbRe.MatchString(name) {
				t.Errorf(
					"LINT FAIL [handler-naming]: %s:%d — %s\n"+
						"  Rule: HTTP handlers must follow handle{Noun}{Verb} (e.g. handleAgentCreate, handleTaskGet).\n"+
						"  Fix: Rename to handle{Resource}{Action}, where Resource is the noun (Agent, Task, Space)\n"+
						"       and Action is a CRUD verb (Create, List, Get, Update, Delete, Move, Ack, etc.).\n"+
						"  Note: If this is unavoidable, add to grandfatheredHandlers in lint_test.go with a reason.",
					e.Name(), lineNum, name,
				)
			}
		}
		f.Close()
		if err := sc.Err(); err != nil {
			t.Fatalf("scan %s: %v", filePath, err)
		}
	}
}

// TestAgentExperienceSurfaceInvariants enforces the agent experience contract:
// any TmuxCreateOpts struct literal that sets MCPServerURL must also set AgentToken.
//
// Rationale: MCPServerURL and AgentToken are logically coupled. When BOSS_API_TOKEN
// is set on the server, all MCP tool calls require an Authorization: Bearer header.
// If MCPServerURL is registered without AgentToken, agents silently receive 401s
// on every MCP call — a connectivity failure with no obvious error at spawn time.
//
// This invariant was introduced after TASK-015 (PR #155) revealed exactly this
// failure mode: auth was added to the HTTP layer but spawn never got the --header flag.
//
// See: docs/design-docs/agent-experience-surface.md
//
// TO FIX: Add AgentToken: s.apiToken to any TmuxCreateOpts literal that sets MCPServerURL.
func TestAgentExperienceSurfaceInvariants(t *testing.T) {
	dir := coordinatorDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}

	// We scan source files for TmuxCreateOpts struct literals using a simple
	// state-machine approach: track when we're inside a TmuxCreateOpts{...} block
	// and check that MCPServerURL and AgentToken appear together.
	//
	// This grep-based approach is intentionally simple and robust — no AST parsing
	// needed because the struct literal fields are always on separate lines by
	// convention (enforced by gofmt). If the codebase style changes, adjust the
	// detection logic below.

	tmuxOptsStartRe := regexp.MustCompile(`\bTmuxCreateOpts\s*\{`)
	mcpURLFieldRe := regexp.MustCompile(`\bMCPServerURL\s*:`)
	agentTokenFieldRe := regexp.MustCompile(`\bAgentToken\s*:`)

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		filePath := filepath.Join(dir, e.Name())

		// Read all lines so we can do windowed scans around each TmuxCreateOpts literal.
		f, err := os.Open(filePath)
		if err != nil {
			t.Fatalf("open %s: %v", filePath, err)
		}
		var lines []string
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			lines = append(lines, sc.Text())
		}
		f.Close()
		if err := sc.Err(); err != nil {
			t.Fatalf("scan %s: %v", filePath, err)
		}

		for i, line := range lines {
			if !tmuxOptsStartRe.MatchString(line) {
				continue
			}
			// Found a TmuxCreateOpts{ literal at line i (1-indexed: i+1).
			// Scan forward to find the closing brace, collecting all field names.
			// Limit the scan window to 30 lines — struct literals should never be longer.
			startLine := i + 1 // 1-indexed for error messages
			hasMCPURL := mcpURLFieldRe.MatchString(line)
			hasAgentToken := agentTokenFieldRe.MatchString(line)

			depth := strings.Count(line, "{") - strings.Count(line, "}")
			for j := i + 1; j < len(lines) && j < i+30; j++ {
				l := lines[j]
				if mcpURLFieldRe.MatchString(l) {
					hasMCPURL = true
				}
				if agentTokenFieldRe.MatchString(l) {
					hasAgentToken = true
				}
				depth += strings.Count(l, "{") - strings.Count(l, "}")
				if depth <= 0 {
					break
				}
			}

			if hasMCPURL && !hasAgentToken {
				t.Errorf(
					"LINT FAIL [agent-experience]: %s:%d — TmuxCreateOpts sets MCPServerURL without AgentToken\n"+
						"  Invariant: MCPServerURL and AgentToken must always be set together.\n"+
						"  Reason: When BOSS_API_TOKEN is configured, every MCP tool call requires an\n"+
						"          Authorization: Bearer header. Omitting AgentToken silently breaks all\n"+
						"          agent MCP connectivity with no error at spawn time.\n"+
						"  Fix: Add AgentToken: s.apiToken to this TmuxCreateOpts literal.\n"+
						"  Spec: docs/design-docs/agent-experience-surface.md",
					e.Name(), startLine,
				)
			}
		}
	}
}
