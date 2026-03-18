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
	// "Do you trust the files in this folder?" prompt that appears when Claude
	// is launched in a directory it has not seen before. This is TASK-120.
	lines := []string{
		"  ╭──────────────────────────────────────────────╮",
		"  │ Do you trust the files in this folder?       │",
		"  │                                              │",
		"  │ claude has been asked to operate in          │",
		"  │ /tmp/new-project. This code may attempt to  │",
		"  │ read and modify files on your computer.     │",
		"  │ Trust this folder to allow claude to run    │",
		"  │ as normal.                                  │",
		"  ╰──────────────────────────────────────────────╯",
		"  ❯ 1. Yes, proceed",
		"    2. No, exit",
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
