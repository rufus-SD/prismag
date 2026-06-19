package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// sessionMeta describes a saved transcript on disk.
type sessionMeta struct {
	Path        string
	ID          string
	Cwd         string
	Started     string
	Turns       int
	FirstPrompt string
	mod         time.Time
}

// turnRec is one you→prismag exchange parsed back out of a transcript.
type turnRec struct {
	input  string
	output string
}

var (
	flagPrune bool
	flagKeep  int
)

var sessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "List saved prismag sessions you can resume",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if flagPrune || flagKeep > 0 {
			pruneSessions(os.Stdout, flagPrune, flagKeep)
		}
		printSessions(os.Stdout)
		return nil
	},
}

var resumeCmd = &cobra.Command{
	Use:   "resume [session]",
	Short: "Resume a past session, recalling its context into a new prismag> loop",
	Long: `Reopen a previous session by number (see 'prismag sessions'), id, or date.
Its earlier turns are recalled into context so you carry on where you left off,
and the transcript continues in the same file.

With no argument in a terminal, pick from a list.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ref := ""
		if len(args) == 1 {
			ref = args[0]
		}
		if ref == "" {
			if !isInteractive() {
				return fmt.Errorf("specify a session (see 'prismag sessions')")
			}
			r, err := pickSession()
			if err != nil {
				return err
			}
			ref = r
		}
		return startREPL(ref)
	},
}

func init() {
	sessionsCmd.Flags().BoolVar(&flagPrune, "prune", false, "delete empty (0-turn) sessions before listing")
	sessionsCmd.Flags().IntVar(&flagKeep, "keep", 0, "keep only the N most recent sessions, delete older")
	resumeCmd.Flags().BoolVar(&flagNoTranscript, "no-transcript", false, "don't append to the transcript on disk")
	rootCmd.AddCommand(sessionsCmd)
	rootCmd.AddCommand(resumeCmd)
}

// recentPrompts returns up to limit prompts from recent sessions, oldest-first,
// to pre-seed REPL history across launches.
func recentPrompts(limit int) []string {
	list, err := listSessions()
	if err != nil || len(list) == 0 {
		return nil
	}
	if len(list) > 15 { // only scan a handful of recent sessions
		list = list[:15]
	}
	var prompts []string
	for i := len(list) - 1; i >= 0; i-- { // oldest session first
		_, turns, perr := parseTranscript(list[i].Path)
		if perr != nil {
			continue
		}
		for _, t := range turns {
			if strings.TrimSpace(t.input) != "" {
				prompts = append(prompts, t.input)
			}
		}
	}
	if len(prompts) > limit {
		prompts = prompts[len(prompts)-limit:]
	}
	return prompts
}

// pruneSessions deletes empty and/or older session transcripts.
func pruneSessions(w io.Writer, dropEmpty bool, keep int) {
	list, err := listSessions()
	if err != nil {
		fmt.Fprintln(w, "  could not read sessions:", err)
		return
	}
	removed := 0
	var kept []sessionMeta
	for _, s := range list {
		if dropEmpty && s.Turns == 0 {
			if os.Remove(s.Path) == nil {
				removed++
			}
			continue
		}
		kept = append(kept, s)
	}
	if keep > 0 && len(kept) > keep {
		for _, s := range kept[keep:] { // kept is most-recent first
			if os.Remove(s.Path) == nil {
				removed++
			}
		}
	}
	fmt.Fprintf(w, "  pruned %d session(s)\n", removed)
}

// listSessions returns saved sessions, most recently modified first.
func listSessions() ([]sessionMeta, error) {
	dir, err := sessionsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []sessionMeta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		meta, _, perr := parseTranscript(path)
		if perr != nil {
			continue
		}
		if info, serr := e.Info(); serr == nil {
			meta.mod = info.ModTime()
		}
		if meta.ID == "" {
			meta.ID = strings.TrimSuffix(e.Name(), ".md")
		}
		out = append(out, meta)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].mod.After(out[j].mod) })
	return out, nil
}

func countSessions() int {
	list, err := listSessions()
	if err != nil {
		return 0
	}
	return len(list)
}

func printSessions(w io.Writer) {
	list, err := listSessions()
	if err != nil {
		fmt.Fprintln(w, "  could not read sessions:", err)
		return
	}
	if len(list) == 0 {
		fmt.Fprintln(w, "  no saved sessions yet — run `prismag` to start one")
		return
	}
	fmt.Fprintln(w)
	for i, s := range list {
		fmt.Fprintf(w, "  %2d  %s  %-22s  %2d turn(s)  %s\n",
			i+1, s.mod.Format("2006-01-02 15:04"), s.ID, s.Turns, s.FirstPrompt)
	}
	fmt.Fprintln(w, "\n  resume with: prismag resume <number|id>")
}

// findSession resolves a reference (1-based index, id, or filename fragment) to
// a saved session and parses its turns.
func findSession(ref string) (sessionMeta, []turnRec, error) {
	list, err := listSessions()
	if err != nil {
		return sessionMeta{}, nil, err
	}
	if len(list) == 0 {
		return sessionMeta{}, nil, fmt.Errorf("no saved sessions yet")
	}
	if n, nerr := strconv.Atoi(ref); nerr == nil {
		if n < 1 || n > len(list) {
			return sessionMeta{}, nil, fmt.Errorf("no session #%d (have %d)", n, len(list))
		}
		meta := list[n-1]
		_, turns, perr := parseTranscript(meta.Path)
		return meta, turns, perr
	}
	for _, s := range list {
		if s.ID == ref || strings.Contains(filepath.Base(s.Path), ref) {
			_, turns, perr := parseTranscript(s.Path)
			return s, turns, perr
		}
	}
	return sessionMeta{}, nil, fmt.Errorf("no session matching %q (see 'prismag sessions')", ref)
}

func pickSession() (string, error) {
	printSessions(os.Stdout)
	list, _ := listSessions()
	if len(list) == 0 {
		return "", fmt.Errorf("nothing to resume")
	}
	fmt.Print("\n  resume #: ")
	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		return "", fmt.Errorf("cancelled")
	}
	return strings.TrimSpace(sc.Text()), nil
}

// parseTranscript reads a transcript file's header and reconstructs its turns.
func parseTranscript(path string) (sessionMeta, []turnRec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return sessionMeta{}, nil, err
	}
	meta := sessionMeta{Path: path}
	var turns []turnRec
	var curIn, curOut strings.Builder
	section := ""
	started := false

	flush := func() {
		if !started {
			return
		}
		turns = append(turns, turnRec{
			input:  strings.TrimSpace(curIn.String()),
			output: strings.TrimSpace(curOut.String()),
		})
		curIn.Reset()
		curOut.Reset()
		started = false
	}

	for _, ln := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(ln, "# PRISMAG session "):
			meta.ID = strings.TrimSpace(strings.TrimPrefix(ln, "# PRISMAG session "))
		case strings.HasPrefix(ln, "- cwd:"):
			meta.Cwd = strings.TrimSpace(strings.TrimPrefix(ln, "- cwd:"))
		case strings.HasPrefix(ln, "- started:"):
			meta.Started = strings.TrimSpace(strings.TrimPrefix(ln, "- started:"))
		case strings.HasPrefix(ln, "## you"):
			flush()
			started = true
			section = "you"
		case strings.HasPrefix(ln, "## prismag"):
			section = "prismag"
		case ln == "---" || strings.HasPrefix(ln, "_session ended") || strings.HasPrefix(ln, "_resumed"):
			section = ""
		default:
			switch section {
			case "you":
				curIn.WriteString(ln)
				curIn.WriteByte('\n')
			case "prismag":
				curOut.WriteString(ln)
				curOut.WriteByte('\n')
			}
		}
	}
	flush()

	meta.Turns = len(turns)
	if len(turns) > 0 {
		meta.FirstPrompt = firstLine(turns[0].input)
	}
	return meta, turns, nil
}
