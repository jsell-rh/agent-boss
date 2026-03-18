package coordinator

import (
	"testing"
)

func TestParseApprovalFromLines_ToolPermission(t *testing.T) {
	// Typical Claude Code tool-approval prompt (new-style, no box drawing).
	// The approval dialog shows the tool name as a plain line before the question.
	lines := []string{
		"  Bash(ls -la /tmp)",
		"  Do you want to run this command?",
		"❯ 1. Yes",
		"  2. No",
	}
	info := parseApprovalFromLines(lines)
	if !info.NeedsApproval {
		t.Fatal("expected NeedsApproval=true for tool permission prompt")
	}
	if info.ToolName != "Bash" {
		t.Errorf("expected ToolName=Bash, got %q", info.ToolName)
	}
}

func TestParseApprovalFromLines_TrustFolder(t *testing.T) {
	// Real "trust this folder?" prompt captured from Claude Code (TASK-120).
	// Appears when Claude is launched in a directory it hasn't seen before.
	lines := []string{
		" ─────────────────────────────────────────────────────────────────────────────────",
		" Accessing workspace:",
		"",
		" /tmp/new-project",
		"",
		" Quick safety check: Is this a project you created or one you trust? (Like your own",
		" code, a well-known open source project, or work from your team). If not, take a",
		" moment to review what's in this folder first.",
		"",
		" Claude Code'll be able to read, edit, and execute files here.",
		"",
		" Security guide",
		"",
		" ❯ 1. Yes, I trust this folder",
		"   2. No, exit",
		"",
		" Enter to confirm · Esc to cancel",
	}
	info := parseApprovalFromLines(lines)
	if !info.NeedsApproval {
		t.Fatal("expected NeedsApproval=true for trust-folder prompt")
	}
}

func TestParseApprovalFromLines_NoPrompt(t *testing.T) {
	lines := []string{
		"● Bash(ls -la /tmp)",
		"  ⎿  total 48",
		"     drwxrwxrwt  15 root root 4096 Mar 18 10:00 .",
	}
	info := parseApprovalFromLines(lines)
	if info.NeedsApproval {
		t.Fatal("expected NeedsApproval=false when no prompt present")
	}
}

func TestParseApprovalFromLines_PromptWithoutChoices(t *testing.T) {
	// Prompt text found but no numbered/cursor choice lines — not a real approval.
	lines := []string{
		"  Do you want to know the weather?",
	}
	info := parseApprovalFromLines(lines)
	if info.NeedsApproval {
		t.Fatal("expected NeedsApproval=false when no choice markers present")
	}
}

func TestParseApprovalFromLines_OldStyleBoxDrawing(t *testing.T) {
	// Old-style dialog with │ box-drawing characters around choice.
	lines := []string{
		"  │ Bash                                         │",
		"  │ rm -rf /tmp/test                            │",
		"  │ Do you want to run this command?            │",
		"  │ ❯ 1. Yes                                   │",
		"  │   2. No                                    │",
	}
	info := parseApprovalFromLines(lines)
	if !info.NeedsApproval {
		t.Fatal("expected NeedsApproval=true for old-style box-drawing prompt")
	}
}
