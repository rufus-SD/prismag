// Package workspace gathers repo context (git diff, files) and derives a stable
// session id from the working directory and git branch.
package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SessionID returns a stable id for the current workspace: the git toplevel (or
// cwd) combined with the current branch, hashed to a short hex string. Runs from
// the same repo+branch share a context store.
func SessionID() string {
	root := repoRoot()
	branch := gitBranch()
	seed := root + "@" + branch
	sum := sha256.Sum256([]byte(seed))
	return "ws-" + hex.EncodeToString(sum[:])[:12]
}

func repoRoot() string {
	if out, err := runGit("rev-parse", "--show-toplevel"); err == nil {
		if t := strings.TrimSpace(out); t != "" {
			return t
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

func gitBranch() string {
	if out, err := runGit("rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		if b := strings.TrimSpace(out); b != "" {
			return b
		}
	}
	return "no-branch"
}

// GatherContext builds a shared-context string from the git diff (optional) and
// the contents of the given files/globs (optional). Returns "" when nothing is
// requested or found. maxBytesPerFile caps each file to avoid huge prompts.
func GatherContext(includeDiff bool, files []string, maxBytesPerFile int) (string, error) {
	if maxBytesPerFile <= 0 {
		maxBytesPerFile = 32 * 1024
	}
	var parts []string

	if includeDiff {
		if diff, err := runGit("diff", "--no-color"); err == nil {
			if d := strings.TrimSpace(diff); d != "" {
				parts = append(parts, "## Working tree diff\n\n```diff\n"+d+"\n```")
			}
		}
	}

	seen := map[string]bool{}
	for _, pattern := range files {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return "", fmt.Errorf("bad file pattern %q: %w", pattern, err)
		}
		if len(matches) == 0 {
			// Treat as a literal path so a clear error surfaces if it's missing.
			matches = []string{pattern}
		}
		for _, m := range matches {
			if seen[m] {
				continue
			}
			seen[m] = true
			info, err := os.Stat(m)
			if err != nil || info.IsDir() {
				continue
			}
			data, err := os.ReadFile(m)
			if err != nil {
				return "", fmt.Errorf("read %q: %w", m, err)
			}
			truncated := false
			if len(data) > maxBytesPerFile {
				data = data[:maxBytesPerFile]
				truncated = true
			}
			block := fmt.Sprintf("## File: %s\n\n```\n%s\n```", m, string(data))
			if truncated {
				block += "\n_(truncated)_"
			}
			parts = append(parts, block)
		}
	}

	return strings.Join(parts, "\n\n"), nil
}

func runGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	return string(out), err
}
