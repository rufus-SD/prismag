package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rufus-SD/prismag/internal/agent"
	"github.com/rufus-SD/prismag/internal/availability"
	"github.com/rufus-SD/prismag/internal/contextstore"
	"github.com/rufus-SD/prismag/internal/discovery"
	"github.com/rufus-SD/prismag/internal/orchestrator"
	"github.com/rufus-SD/prismag/internal/registry"
	"github.com/rufus-SD/prismag/internal/secrets"
	"github.com/rufus-SD/prismag/internal/workspace"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var replCmd = &cobra.Command{
	Use:     "repl",
	Aliases: []string{"chat"},
	Short:   "Start an interactive prismag> session (chained, transcript-logged)",
	Long: `Open a live session. Each line is a @@tagged prompt; blocks chain across
turns, so block 2 sees block 1 — a real conversation, not one-shot calls.

The whole session is saved as a markdown transcript. Use :remember to push a
note into maind, or :save to store the whole session for later recall. Resume a
past session with 'prismag resume'. Meta-commands start with ':' — type :help.

This is a CLI feature: inside an AI IDE the agent handles recall itself (via
maind), so the REPL is for working straight from a terminal.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error { return startREPL("") },
}

var flagNoTranscript bool

func init() {
	replCmd.Flags().BoolVar(&flagNoTranscript, "no-transcript", false, "don't save a session transcript to disk")
	rootCmd.AddCommand(replCmd)
}

// replState bundles everything one interactive session needs.
type replState struct {
	reg       *registry.Registry
	creds     availability.Credentials
	store     *offsetStore
	storeName string
	session   string
	tr        *transcript
	prompts   []string  // first lines of each prompt, for :save summaries
	out       io.Writer // where session output is written
	tty       bool      // true when attached to a terminal (enables spinner)
	last      string    // last prompt, for :retry
	ed        *editor   // line editor, also used to prompt for exec approvals

	exec      bool // tool loop enabled for this session
	execShell bool // run_shell allowed
	execYes   bool // auto-approve actions (no per-action prompt)

	mu     sync.Mutex
	cancel context.CancelFunc // cancels the in-flight turn (set during runTurn)
}

// setCancel stores the cancel func for the active turn (nil when idle).
func (s *replState) setCancel(c context.CancelFunc) {
	s.mu.Lock()
	s.cancel = c
	s.mu.Unlock()
}

// interrupt cancels the active turn, if any. Returns true if something was cancelled.
func (s *replState) interrupt() bool {
	s.mu.Lock()
	c := s.cancel
	s.mu.Unlock()
	if c != nil {
		c()
		return true
	}
	return false
}

// offsetStore wraps a context store and shifts every Write by a growing base so
// blocks across REPL turns (and resumed history) keep a chronological order —
// the underlying store keys by turn index, which each Run otherwise resets to 0.
type offsetStore struct {
	inner contextstore.Store
	base  int
}

func (o *offsetStore) Write(sessionID, alias string, turn int, output string) error {
	return o.inner.Write(sessionID, alias, o.base+turn, output)
}

func (o *offsetStore) Recall(sessionID, query string, budget int) (string, error) {
	return o.inner.Recall(sessionID, query, budget)
}

// isInteractive reports whether both stdin and stdout are terminals.
func isInteractive() bool {
	in, err1 := os.Stdin.Stat()
	out, err2 := os.Stdout.Stat()
	if err1 != nil || err2 != nil {
		return false
	}
	return (in.Mode()&os.ModeCharDevice) != 0 && (out.Mode()&os.ModeCharDevice) != 0
}

// editor reads prompt lines. In a real terminal it uses golang.org/x/term for
// full line editing (history via ↑/↓, cursor movement, clean paste); otherwise
// it falls back to a plain line scanner (pipes, tests).
//
// Raw mode is entered only for the duration of a ReadLine, so that between
// prompts (while a model call runs) the terminal stays in cooked mode and
// Ctrl-C still raises SIGINT — letting us cancel an in-flight turn.
type editor struct {
	t      *term.Terminal
	sc     *bufio.Scanner
	out    io.Writer
	prompt string
	fd     int
	tty    bool
}

func newEditor() *editor {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		rw := struct {
			io.Reader
			io.Writer
		}{os.Stdin, os.Stdout}
		t := term.NewTerminal(rw, "")
		t.SetBracketedPasteMode(true) // multi-line paste arrives as one prompt
		return &editor{t: t, out: os.Stdout, fd: fd, tty: true}
	}
	return newScannerEditor(os.Stdin, os.Stdout)
}

func newScannerEditor(r io.Reader, w io.Writer) *editor {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	return &editor{sc: sc, out: w}
}

func (e *editor) setPrompt(p string) {
	e.prompt = p
	if e.t != nil {
		e.t.SetPrompt(p)
	}
}

// addHistory records a line so ↑/↓ can recall it (term keeps its own history,
// but this lets us pre-seed from past transcripts).
func (e *editor) addHistory(line string) {
	if e.t != nil && strings.TrimSpace(line) != "" {
		e.t.History.Add(line)
	}
}

func (e *editor) readLine() (string, error) {
	if e.t != nil {
		old, err := term.MakeRaw(e.fd)
		if err == nil {
			if w, h, gerr := term.GetSize(e.fd); gerr == nil {
				e.t.SetSize(w, h)
			}
			defer term.Restore(e.fd, old)
		}
		return e.t.ReadLine()
	}
	fmt.Fprint(e.out, e.prompt)
	if !e.sc.Scan() {
		return "", io.EOF
	}
	return e.sc.Text(), nil
}

// next reads one logical prompt. A bracketed paste of several lines is returned
// as a single prompt; otherwise trailing-backslash lines are joined.
func (e *editor) next() (string, bool) {
	e.setPrompt(replPrompt())
	line, err := e.readLine()
	if err == term.ErrPasteIndicator {
		return e.collectPaste(line), true
	}
	if err != nil {
		return "", false
	}
	if !strings.HasSuffix(line, "\\") {
		return line, true
	}
	var b strings.Builder
	for {
		b.WriteString(strings.TrimSuffix(line, "\\"))
		b.WriteByte('\n')
		e.setPrompt(replContinuation())
		line, err = e.readLine()
		if err != nil {
			return strings.TrimRight(b.String(), "\n"), true
		}
		if !strings.HasSuffix(line, "\\") {
			b.WriteString(line)
			return b.String(), true
		}
	}
}

// collectPaste accumulates the lines of a bracketed paste into one prompt.
func (e *editor) collectPaste(first string) string {
	var b strings.Builder
	b.WriteString(first)
	for {
		line, err := e.readLine()
		b.WriteByte('\n')
		b.WriteString(line)
		if err != term.ErrPasteIndicator {
			return strings.TrimRight(b.String(), "\n")
		}
	}
}

// startREPL runs the interactive session loop. It always executes via provider
// APIs (the human-at-a-terminal path); when prismag is driven by an IDE agent,
// the agent handles routing itself and never enters this loop. A non-empty
// resumeRef reopens a past session and re-seeds its context.
func startREPL(resumeRef string) error {
	reg, err := loadRegistry()
	if err != nil {
		return err
	}
	inner, storeName := selectStore(flagStore)
	st := &replState{
		reg:       reg,
		creds:     availability.FromEnv(),
		store:     &offsetStore{inner: inner},
		storeName: storeName,
	}

	var seeded int
	if resumeRef != "" {
		meta, turns, perr := findSession(resumeRef)
		if perr != nil {
			return perr
		}
		st.session = meta.ID
		if flagNoTranscript {
			st.tr = &transcript{}
		} else {
			tr, terr := reopenTranscript(meta.Path)
			if terr != nil {
				fmt.Fprintln(os.Stderr, "  (transcript disabled:", terr, ")")
			}
			st.tr = tr
		}
		seeded = st.seed(turns)
	} else {
		st.session = newSessionID()
		if flagNoTranscript {
			st.tr = &transcript{}
		} else {
			tr, terr := newTranscript(st.session, storeName)
			if terr != nil {
				fmt.Fprintln(os.Stderr, "  (transcript disabled:", terr, ")")
			}
			st.tr = tr
		}
	}
	defer st.tr.close()

	ed := newEditor()
	st.out = ed.out
	st.tty = ed.tty
	st.ed = ed
	seedHistory(ed)

	// Ctrl-C cancels the in-flight turn (terminal is cooked between prompts,
	// so SIGINT is delivered). When idle it's a no-op — use :quit / Ctrl-D.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)
	go func() {
		for range sigCh {
			if st.interrupt() {
				fmt.Fprintln(st.out, "\n  ^C — cancelled")
			}
		}
	}()

	fmt.Fprint(st.out, Banner())
	st.printHeader(seeded)

loop:
	for {
		line, ok := ed.next()
		if !ok {
			fmt.Fprintln(st.out)
			break
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Bare exit/quit (no colon) is a common reflex — honor it.
		switch strings.ToLower(trimmed) {
		case "exit", "quit":
			break loop
		}
		if strings.HasPrefix(trimmed, ":") {
			if quit := st.handleMeta(trimmed); quit {
				break loop
			}
			continue
		}
		st.runTurn(line)
	}

	if st.tr.ok() {
		fmt.Fprintf(st.out, "\n  transcript saved → %s\n", st.tr.path)
	}
	return nil
}

// seedHistory pre-loads the editor's ↑/↓ history with prompts from recent
// sessions so history survives across launches.
func seedHistory(ed *editor) {
	if ed.t == nil {
		return
	}
	prompts := recentPrompts(60)
	for _, p := range prompts {
		ed.addHistory(p)
	}
}

func newSessionID() string {
	base := workspace.SessionID()
	short := base
	if strings.HasPrefix(short, "ws-") && len(short) >= 11 {
		short = short[3:11]
	}
	return fmt.Sprintf("%s-%s", short, time.Now().Format("150405"))
}

// seed loads prior turns into the store as history and advances the offset so
// new turns continue after them. Returns the number of turns seeded.
func (s *replState) seed(turns []turnRec) int {
	n := 0
	for _, t := range turns {
		if t.output == "" || strings.HasPrefix(t.output, "_(error") {
			continue
		}
		var body strings.Builder
		if t.input != "" {
			body.WriteString("user: " + t.input + "\n\n")
			s.prompts = append(s.prompts, firstLine(t.input))
		}
		body.WriteString("assistant: " + t.output)
		_ = s.store.Write(s.session, "session", n, body.String())
		n++
	}
	s.store.base = n
	return n
}

func (s *replState) runTurn(input string) {
	s.last = input
	s.prompts = append(s.prompts, firstLine(input))

	ctx, cancel := context.WithCancel(context.Background())
	s.setCancel(cancel)

	opts := orchestrator.Options{
		Parallel:      flagParallel,
		SessionID:     s.session,
		ContextBudget: orchestrator.DefaultContextBudget,
		Registry:      s.reg,
		Store:         s.store,
		Creds:         s.creds,
		Context:       availability.ContextCLI,
		Models:        discovery.Cached(availability.ContextCLI, s.creds),
		Exec:          s.execPolicy(),
	}

	// Stream live when interactive and serial; otherwise show a spinner and
	// print the formatted report at the end. Exec mode never streams (it drives
	// a tool loop with approval prompts).
	streaming := s.tty && !flagParallel && !s.exec
	stop := func() {}
	var curAlias string
	if streaming {
		opts.Stream = func(alias, delta string) {
			if alias != curAlias {
				fmt.Fprintf(s.out, "\n## @@%s\n\n", alias)
				curAlias = alias
			}
			fmt.Fprint(s.out, delta)
		}
	} else {
		stop = s.startSpinner()
	}

	result, err := orchestrator.Run(ctx, input, opts)
	stop()
	s.setCancel(nil)
	wasCanceled := ctx.Err() != nil // set only if the SIGINT handler cancelled
	cancel()
	if streaming && curAlias != "" {
		fmt.Fprintln(s.out)
	}

	if err != nil {
		msg := err.Error()
		switch {
		case wasCanceled || strings.Contains(msg, "context canceled"):
			fmt.Fprintln(s.out, "  (turn cancelled)")
			s.tr.turn(input, "_(cancelled)_")
			s.store.base++
			return
		case strings.Contains(msg, "no @@alias: tags"):
			fmt.Fprintln(s.out, "  no @@tag found — prompts need at least one '@@alias: task' (try :list)")
		case strings.Contains(msg, "no @@alias is ready"):
			fmt.Fprintln(s.out, "  "+msg)
		default:
			fmt.Fprintln(s.out, "  error:", msg)
		}
		if len(result.Tasks) == 0 {
			s.tr.turn(input, "_(error: "+msg+")_")
			s.store.base++ // keep turn ids monotonic even on a no-op
			return
		}
	}

	if streaming {
		// Blocks that didn't stream (skipped/errored) still need to be shown.
		for _, t := range result.Tasks {
			switch {
			case t.Skipped:
				fmt.Fprintf(s.out, "\n## @@%s → skipped — %s\n", t.RawAlias, t.SkipNote)
			case t.Err != nil:
				fmt.Fprintf(s.out, "\n## @@%s → error: %v\n", t.RawAlias, t.Err)
			}
		}
		fmt.Fprintln(s.out)
	} else {
		fmt.Fprintln(s.out, "\n"+orchestrator.FormatMarkdown(result)+"\n")
	}
	if usage := turnUsage(result); usage != "" {
		fmt.Fprintln(s.out, usage)
	}
	s.tr.turn(input, orchestrator.FormatMarkdown(result))
	// Advance past the blocks this turn wrote so the next turn sorts after them.
	if n := len(result.Tasks); n > 0 {
		s.store.base += n
	} else {
		s.store.base++
	}
}

// turnUsage renders a dim one-line token summary across a turn's blocks, or ""
// when no usage was reported by the providers.
func turnUsage(r orchestrator.Result) string {
	var in, out int
	for _, t := range r.Tasks {
		in += t.InTokens
		out += t.OutTokens
	}
	if in == 0 && out == 0 {
		return ""
	}
	line := fmt.Sprintf("  ⊙ %d in · %d out tokens", in, out)
	if !colorEnabled() {
		return line
	}
	return "\x1b[2m" + line + "\x1b[0m"
}

// startSpinner shows a lightweight working indicator until the returned stop is
// called. It is a no-op when not attached to a terminal.
func (s *replState) startSpinner() func() {
	if !s.tty {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		i := 0
		t := time.NewTicker(90 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-done:
				fmt.Fprint(s.out, "\r\033[K") // clear the spinner line
				return
			case <-t.C:
				fmt.Fprintf(s.out, "\r  %s routing… (Ctrl-C to cancel)", frames[i%len(frames)])
				i++
			}
		}
	}()
	return func() { close(done) }
}

func (s *replState) handleMeta(cmd string) (quit bool) {
	name, rest, _ := strings.Cut(strings.TrimPrefix(cmd, ":"), " ")
	rest = strings.TrimSpace(rest)
	switch strings.ToLower(name) {
	case "quit", "exit", "q":
		return true
	case "help", "h", "?":
		fmt.Fprintln(s.out, replHelp())
	case "list", "aliases":
		fmt.Fprintln(s.out, replList(s.reg, s.creds))
	case "context", "ctx":
		ctx, err := s.store.Recall(s.session, "", orchestrator.DefaultContextBudget)
		if err != nil || strings.TrimSpace(ctx) == "" {
			fmt.Fprintln(s.out, "  (no chained context yet)")
		} else {
			fmt.Fprintln(s.out, "\n"+strings.TrimSpace(ctx)+"\n")
		}
	case "clear", "reset":
		s.session = fmt.Sprintf("%s-c%s", s.session, time.Now().Format("150405"))
		s.store.base = 0
		fmt.Fprintln(s.out, "  context cleared — new prompts start a fresh thread (transcript continues)")
		s.tr.note("--- context cleared ---")
	case "remember", "rem":
		s.remember(rest)
	case "save":
		s.save()
	case "sessions", "ls":
		printSessions(s.out)
	case "resume", "r":
		s.resume(rest)
	case "retry":
		if strings.TrimSpace(s.last) == "" {
			fmt.Fprintln(s.out, "  nothing to retry yet")
		} else {
			fmt.Fprintln(s.out, "  ↻ "+firstLine(s.last))
			s.runTurn(s.last)
		}
	case "transcript":
		if s.tr.ok() {
			fmt.Fprintln(s.out, "  transcript → "+s.tr.path)
		} else {
			fmt.Fprintln(s.out, "  transcript disabled for this session")
		}
	case "models":
		fmt.Fprintln(s.out, "  run `prismag models` in another shell for live discovery; :list shows configured aliases")
	case "exec":
		s.setExec(rest)
	default:
		fmt.Fprintf(s.out, "  unknown command :%s — type :help\n", name)
	}
	return false
}

// setExec toggles the permission-gated tool loop for the session.
//
//	:exec            enable, ask before each action
//	:exec shell      enable + allow run_shell
//	:exec yes        enable + auto-approve every action (careful)
//	:exec off        disable
func (s *replState) setExec(arg string) {
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "off", "no", "0", "false":
		s.exec, s.execShell, s.execYes = false, false, false
		fmt.Fprintln(s.out, "  exec OFF — blocks return text only")
		return
	case "shell":
		s.exec, s.execShell = true, true
	case "yes", "auto":
		s.exec, s.execYes = true, true
	default:
		s.exec = true
	}
	mode := "asks before each action"
	if s.execYes {
		mode = "auto-approves actions"
	}
	shell := ""
	if s.execShell {
		shell = " + shell"
	}
	fmt.Fprintf(s.out, "  exec ON%s — %s. Blocks can write files and act on this machine.\n", shell, mode)
}

// execPolicy builds the tool-loop policy for a turn, or nil when exec is off.
func (s *replState) execPolicy() *agent.Policy {
	if !s.exec {
		return nil
	}
	return &agent.Policy{
		AllowShell: s.execShell,
		Emit:       func(line string) { fmt.Fprintln(s.out, line) },
		Approve: func(a agent.Action) (bool, string) {
			if s.execYes {
				return true, ""
			}
			if s.confirm(fmt.Sprintf("  ⚠ allow %s ? [y/N] ", agent.Describe(a))) {
				return true, ""
			}
			return false, "user declined"
		},
	}
}

// confirm asks a yes/no question using the line editor and returns true on "y".
func (s *replState) confirm(prompt string) bool {
	if s.ed == nil {
		return false
	}
	s.ed.setPrompt(prompt)
	line, err := s.ed.readLine()
	if err != nil {
		return false
	}
	ans := strings.ToLower(strings.TrimSpace(line))
	return ans == "y" || ans == "yes"
}

// resume switches the live loop to a past session: it recalls that session's
// earlier turns into context and continues logging into its transcript.
func (s *replState) resume(ref string) {
	if ref == "" {
		printSessions(s.out)
		fmt.Fprintln(s.out, "  usage: :resume <number|id>")
		return
	}
	meta, turns, err := findSession(ref)
	if err != nil {
		fmt.Fprintln(s.out, "  "+err.Error())
		return
	}
	if meta.ID == s.session {
		fmt.Fprintln(s.out, "  already in that session")
		return
	}
	s.tr.note("--- switching to session " + meta.ID + " ---")
	s.tr.close()
	tr, terr := reopenTranscript(meta.Path)
	if terr != nil {
		fmt.Fprintln(s.out, "  (transcript disabled:", terr, ")")
	}
	s.tr = tr
	s.session = meta.ID
	s.store.base = 0
	s.prompts = nil
	n := s.seed(turns)
	fmt.Fprintf(s.out, "  resumed %s — recalled %d earlier turn(s)\n", meta.ID, n)
	if s.tr.ok() {
		fmt.Fprintln(s.out, "  transcript → "+s.tr.path)
	}
}

// save stores a compact session summary in maind so it can be recalled later —
// including from an AI IDE, where the agent recalls via maind.
func (s *replState) save() {
	mp, ok := secrets.MaindPath()
	if !ok {
		fmt.Fprintln(s.out, "  maind not found on PATH — install maind to save sessions")
		return
	}
	if !secrets.MaindReady() {
		fmt.Fprintln(s.out, "  maind is locked — run `maind` to unlock, then :save again")
		return
	}
	if len(s.prompts) == 0 {
		fmt.Fprintln(s.out, "  nothing to save yet — ask something first")
		return
	}
	var body strings.Builder
	fmt.Fprintf(&body, "PRISMAG session %s — %d prompt(s):\n", s.session, len(s.prompts))
	for _, p := range s.prompts {
		body.WriteString("- " + p + "\n")
	}
	if s.tr.ok() {
		body.WriteString("Transcript: " + s.tr.path)
	}
	c := exec.Command(mp, "remember", body.String(),
		"--title", "PRISMAG session "+s.session,
		"--kind", "context",
		"--tags", "prismag,session",
		"--importance", "6",
		"--source", "ide",
	)
	if out, err := c.CombinedOutput(); err != nil {
		fmt.Fprintln(s.out, "  maind error:", strings.TrimSpace(string(out)))
		return
	}
	fmt.Fprintln(s.out, "  session saved to maind ✓ (recall it later, even from your IDE)")
	s.tr.note("saved session summary → maind")
}

func (s *replState) remember(note string) {
	if note == "" {
		fmt.Fprintln(s.out, "  usage: :remember <note to store in maind>")
		return
	}
	mp, ok := secrets.MaindPath()
	if !ok {
		fmt.Fprintln(s.out, "  maind not found on PATH — install maind to persist notes")
		return
	}
	if !secrets.MaindReady() {
		fmt.Fprintln(s.out, "  maind is locked — run `maind` to unlock, then :remember again")
		return
	}
	c := exec.Command(mp, "remember", note,
		"--kind", "note",
		"--tags", "prismag,session",
		"--importance", "6",
		"--source", "ide",
	)
	if out, err := c.CombinedOutput(); err != nil {
		fmt.Fprintln(s.out, "  maind error:", strings.TrimSpace(string(out)))
		return
	}
	fmt.Fprintln(s.out, "  remembered ✓ (maind)")
	s.tr.note("remembered → maind: " + note)
}

func (s *replState) printHeader(seeded int) {
	ready := readyProviders(s.creds)
	readyStr := strings.Join(ready, ", ")
	if readyStr == "" {
		readyStr = "no keys — run `prismag setup` (you can still draft prompts)"
	}
	fmt.Fprintf(s.out, "  session %s · store: %s · ready: %s\n", s.session, s.storeName, readyStr)
	if seeded > 0 {
		fmt.Fprintf(s.out, "  resumed — recalled %d earlier turn(s) into context\n", seeded)
	}
	if s.tr.ok() {
		fmt.Fprintf(s.out, "  transcript → %s\n", s.tr.path)
	}
	if seeded == 0 {
		if n := countSessions(); n > 0 {
			fmt.Fprintf(s.out, "  ↩ %d past session(s) — resume one with `prismag resume`\n", n)
		}
	}
	fmt.Fprintln(s.out, "  type a @@tagged prompt · end a line with \\ to continue · :help for commands")
	fmt.Fprintln(s.out)
}

func readyProviders(creds availability.Credentials) []string {
	var out []string
	if creds.Anthropic {
		out = append(out, "anthropic")
	}
	if creds.OpenAI {
		out = append(out, "openai")
	}
	if creds.OpenRouter {
		out = append(out, "openrouter")
	}
	if creds.CursorSDK {
		out = append(out, "cursor")
	}
	return out
}

func replList(reg *registry.Registry, creds availability.Credentials) string {
	var b strings.Builder
	b.WriteString("\n")
	for _, name := range reg.Names() {
		a, _ := reg.Resolve(name)
		st := availability.Resolve(a.Provider, creds, availability.ContextCLI)
		fmt.Fprintf(&b, "  @@%-14s → %-32s %s\n", name, a.Model, availability.Format(st))
	}
	return b.String()
}

func replHelp() string {
	return `
  Commands (start with ':'):
    :list            show configured aliases, models, and what each needs
    :context         show the chained context the next block will see
    :remember <note> store a single note in maind (curated memory)
    :save            store this whole session in maind (recall it later)
    :sessions        list past sessions you can resume
    :resume <n|id>   switch to a past session (recall its context)
    :retry           re-run your last prompt
    :clear           stop recalling earlier turns for new prompts
    :transcript      print this session's transcript path
    :models          hint for live model discovery
    :exec [shell|yes|off]  let blocks take real actions (write files, run_shell); asks before each
    :help            this help
    :quit            end the session (Ctrl-D also works)

  Anything else is a prompt. Use one or more @@alias: task blocks, e.g.
    @@opus: design a token-bucket rate limiter
    @@composer: implement what opus designed
  End a line with \ to keep typing the same prompt on the next line.
  Resume a past session from your shell: prismag resume`
}

func replPrompt() string {
	if !colorEnabled() {
		return "prismag> "
	}
	return "\x1b[38;2;120;160;255mprismag>\x1b[0m "
}

func replContinuation() string {
	if !colorEnabled() {
		return "   ...> "
	}
	return "\x1b[38;2;90;110;160m   ...>\x1b[0m "
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if len([]rune(s)) > 60 {
		s = string([]rune(s)[:57]) + "..."
	}
	return s
}

// --- transcript ---

type transcript struct {
	path    string
	f       *os.File
	created bool // true if we created this file (vs. reopened on resume)
	wrote   bool // true once any turn or note was logged
}

func sessionsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "prismag", "sessions"), nil
}

func newTranscript(session, storeName string) (*transcript, error) {
	dir, err := sessionsDir()
	if err != nil {
		return &transcript{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return &transcript{}, err
	}
	name := fmt.Sprintf("%s-%s.md", time.Now().Format("2006-01-02"), session)
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return &transcript{}, err
	}
	cwd, _ := os.Getwd()
	header := fmt.Sprintf("# PRISMAG session %s\n\n- started: %s\n- cwd: %s\n- store: %s\n\n---\n\n",
		session, time.Now().Format(time.RFC3339), cwd, storeName)
	_, _ = f.WriteString(header)
	return &transcript{path: path, f: f, created: true}, nil
}

func reopenTranscript(path string) (*transcript, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return &transcript{}, err
	}
	fmt.Fprintf(f, "_resumed: %s_\n\n", time.Now().Format(time.RFC3339))
	// Reopened files already have content — never delete them on close.
	return &transcript{path: path, f: f, created: false, wrote: true}, nil
}

func (t *transcript) ok() bool { return t != nil && t.f != nil }

func (t *transcript) turn(input, output string) {
	if !t.ok() {
		return
	}
	t.wrote = true
	ts := time.Now().Format("15:04:05")
	fmt.Fprintf(t.f, "## you · %s\n\n%s\n\n## prismag\n\n%s\n\n---\n\n", ts, strings.TrimSpace(input), strings.TrimSpace(output))
}

func (t *transcript) note(s string) {
	if !t.ok() {
		return
	}
	t.wrote = true
	fmt.Fprintf(t.f, "_%s · %s_\n\n", time.Now().Format("15:04:05"), s)
}

func (t *transcript) close() {
	if !t.ok() {
		return
	}
	// A freshly created session that never logged anything leaves no trace.
	if t.created && !t.wrote {
		_ = t.f.Close()
		_ = os.Remove(t.path)
		return
	}
	_, _ = t.f.WriteString(fmt.Sprintf("_session ended: %s_\n", time.Now().Format(time.RFC3339)))
	_ = t.f.Close()
}
