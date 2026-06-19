// Package agent adds an opt-in, permission-gated tool loop to PRISMAG's CLI
// path. It is intentionally provider-agnostic: rather than relying on each
// vendor's function-calling protocol, the model emits a small JSON "action" in a
// fenced ```prismag block, PRISMAG executes it (after asking permission), and
// feeds the result back. This works identically across Anthropic, OpenAI,
// OpenRouter, and local OpenAI-compatible servers (Ollama/vLLM).
//
// It is CLI-only by design. Inside an IDE the agent already has its own tools, so
// PRISMAG just emits a delegation plan there instead.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Tool names the side-effecting capabilities a block may use.
const (
	ToolWriteFile = "write_file"
	ToolReadFile  = "read_file"
	ToolShell     = "run_shell"
	ToolFinish    = "finish"
)

// Action is a single tool invocation requested by the model.
type Action struct {
	Tool    string `json:"tool"`
	Path    string `json:"path,omitempty"`
	Content string `json:"content,omitempty"`
	Command string `json:"command,omitempty"`
}

// Record captures an executed (or denied) action for reporting.
type Record struct {
	Action Action
	Result string
	Denied bool
}

// CompleteFunc is the model call the loop drives — a thin adapter over a backend.
type CompleteFunc func(ctx context.Context, system, prompt string) (string, error)

// Policy gates and bounds what the loop may do.
type Policy struct {
	// AllowShell enables the run_shell tool. Off by default.
	AllowShell bool
	// Root, if set, confines file reads/writes to this directory tree.
	Root string
	// MaxSteps caps tool iterations to avoid runaway loops (default 12).
	MaxSteps int
	// Approve is consulted before every side-effecting action. It returns
	// whether to allow it, plus a human reason when denied. Required.
	Approve func(Action) (bool, string)
	// Emit, if set, receives short human-readable progress lines.
	Emit func(string)
}

const (
	defaultMaxSteps = 12
	maxObservation  = 4000
)

var actionRE = regexp.MustCompile("(?s)```prismag\\s*\\n(.*?)```")

// ParseAction extracts the first prismag action block from model output. It
// returns the parsed action and ok=true when a well-formed block is present.
func ParseAction(text string) (Action, bool) {
	m := actionRE.FindStringSubmatch(text)
	if m == nil {
		return Action{}, false
	}
	var a Action
	if err := json.Unmarshal([]byte(strings.TrimSpace(m[1])), &a); err != nil {
		return Action{}, false
	}
	if strings.TrimSpace(a.Tool) == "" {
		return Action{}, false
	}
	return a, true
}

// stripActions removes any action blocks, leaving the model's prose (final answer).
func stripActions(text string) string {
	return strings.TrimSpace(actionRE.ReplaceAllString(text, ""))
}

// SystemPrompt builds the instruction prompt for an exec-enabled block, wrapping
// the caller's base system (shared preamble / prior context).
func SystemPrompt(base string, pol Policy) string {
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()

	var b strings.Builder
	if strings.TrimSpace(base) != "" {
		b.WriteString(base)
		b.WriteString("\n\n")
	}
	b.WriteString("You are a PRISMAG worker that can take real actions on the user's machine to complete the task.\n\n")
	b.WriteString("To act, emit EXACTLY ONE action as a fenced block, then stop and wait for the result:\n\n")
	b.WriteString("```prismag\n{\"tool\":\"write_file\",\"path\":\"/abs/or/~path\",\"content\":\"file contents\"}\n```\n\n")
	b.WriteString("Available tools:\n")
	b.WriteString("- write_file {path, content}: create/overwrite a text file (parent dirs are created).\n")
	b.WriteString("- read_file {path}: read a text file.\n")
	if pol.AllowShell {
		b.WriteString("- run_shell {command}: run a shell command and see its output.\n")
	}
	b.WriteString("- finish {}: nothing left to do.\n\n")
	b.WriteString("Rules:\n")
	b.WriteString("- Emit only one action block per reply. After each action you receive its result, then continue.\n")
	b.WriteString("- Paths may use ~ for the home directory. Use absolute paths when the task names a location.\n")
	b.WriteString("- When the task is complete, reply with a short confirmation and NO action block.\n")
	b.WriteString("- Every action requires the user's permission; if denied, adapt or stop.\n\n")
	if cwd != "" {
		fmt.Fprintf(&b, "Working directory: %s\n", cwd)
	}
	if home != "" {
		fmt.Fprintf(&b, "Home directory: %s (Desktop is %s)\n", home, filepath.Join(home, "Desktop"))
	}
	return b.String()
}

// Run drives the tool loop until the model stops requesting actions, MaxSteps is
// hit, or the model context is cancelled. It returns the final prose answer and
// the list of actions taken.
func Run(ctx context.Context, complete CompleteFunc, base, task string, pol Policy) (string, []Record, error) {
	if pol.Approve == nil {
		return "", nil, fmt.Errorf("agent: Approve policy is required")
	}
	if pol.MaxSteps <= 0 {
		pol.MaxSteps = defaultMaxSteps
	}
	system := SystemPrompt(base, pol)
	convo := "TASK:\n" + strings.TrimSpace(task)
	var records []Record
	last := ""

	for step := 0; step < pol.MaxSteps; step++ {
		if err := ctx.Err(); err != nil {
			return last, records, err
		}
		out, err := complete(ctx, system, convo)
		if err != nil {
			return last, records, err
		}
		last = out

		act, ok := ParseAction(out)
		if !ok || act.Tool == ToolFinish {
			return stripActions(out), records, nil
		}

		allowed, reason := true, ""
		if act.Tool == ToolShell && !pol.AllowShell {
			allowed, reason = false, "shell is disabled (pass --exec-shell to enable)"
		} else {
			allowed, reason = pol.Approve(act)
		}

		var obs string
		if !allowed {
			obs = "DENIED by user: " + reason
			records = append(records, Record{Action: act, Result: obs, Denied: true})
			emit(pol, "  ✗ denied "+Describe(act))
		} else {
			res, execErr := execute(act, pol)
			if execErr != nil {
				obs = "ERROR: " + execErr.Error()
			} else {
				obs = res
			}
			records = append(records, Record{Action: act, Result: obs})
			emit(pol, "  ✓ "+Describe(act))
		}

		convo += "\n\n--- your previous reply ---\n" + out +
			"\n\n--- result ---\n" + truncate(obs, maxObservation) +
			"\n\nContinue with the next action, or give your final answer with no action block."
	}
	return stripActions(last), records, fmt.Errorf("agent: reached step limit (%d) without finishing", pol.MaxSteps)
}

func emit(pol Policy, s string) {
	if pol.Emit != nil {
		pol.Emit(s)
	}
}

// Describe renders a one-line summary of an action for prompts and logs.
func Describe(a Action) string {
	switch a.Tool {
	case ToolWriteFile:
		return fmt.Sprintf("write_file %s (%d bytes)", a.Path, len(a.Content))
	case ToolReadFile:
		return "read_file " + a.Path
	case ToolShell:
		return "run_shell: " + singleLine(a.Command)
	default:
		return a.Tool
	}
}

func execute(a Action, pol Policy) (string, error) {
	switch a.Tool {
	case ToolWriteFile:
		abs, err := resolvePath(a.Path, pol.Root)
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(abs, []byte(a.Content), 0o644); err != nil {
			return "", err
		}
		return fmt.Sprintf("wrote %d bytes to %s", len(a.Content), abs), nil
	case ToolReadFile:
		abs, err := resolvePath(a.Path, pol.Root)
		if err != nil {
			return "", err
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return "", err
		}
		return truncate(string(data), maxObservation), nil
	case ToolShell:
		if !pol.AllowShell {
			return "", fmt.Errorf("shell is disabled")
		}
		cmd := exec.Command("sh", "-c", a.Command)
		if pol.Root != "" {
			cmd.Dir = pol.Root
		}
		out, err := cmd.CombinedOutput()
		res := truncate(strings.TrimSpace(string(out)), maxObservation)
		if err != nil {
			return res, fmt.Errorf("command failed: %v", err)
		}
		if res == "" {
			res = "(no output)"
		}
		return res, nil
	default:
		return "", fmt.Errorf("unknown tool %q", a.Tool)
	}
}

// resolvePath expands ~, makes the path absolute, and (when Root is set) ensures
// it stays within the allowed tree.
func resolvePath(p, root string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", fmt.Errorf("empty path")
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
	}
	if !filepath.IsAbs(p) {
		base := root
		if base == "" {
			base, _ = os.Getwd()
		}
		p = filepath.Join(base, p)
	}
	abs := filepath.Clean(p)
	if root != "" {
		r := filepath.Clean(root)
		if abs != r && !strings.HasPrefix(abs, r+string(os.PathSeparator)) {
			return "", fmt.Errorf("path %s is outside the allowed root %s", abs, r)
		}
	}
	return abs, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n…(truncated)"
}

func singleLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
