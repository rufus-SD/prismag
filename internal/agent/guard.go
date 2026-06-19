package agent

import (
	"regexp"
	"strings"
)

// PRISMAG refuses obviously destructive shell commands by default — even when
// the user approves them — so a careless "y" (or an auto-approve config) can't
// wipe a machine. This is deny-by-default; users opt in with
// exec.allow_destructive: true for the rare case they truly need it.
//
// The list targets irreversible, machine-wrecking operations, not ordinary
// deletes: `rm file.txt` and `rm -rf ./build` still go through the normal
// approval prompt. The goal is to make PRISMAG safe to recommend, not to second
// -guess every command.

// catastrophicTargets are paths that turn `rm -rf` into a disaster: filesystem
// root, the home directory, or a top-level glob. Each must stand alone (followed
// by whitespace or end of command), so `/home/me/build` and `./build` are NOT
// matched.
var reCatastrophicTarget = regexp.MustCompile(`(?i)(^|\s)(/|/\*|~|~/|~/\*|\$\{?home\}?|\$\{?home\}?/|\$\{?home\}?/\*|\*|\.|\.\.)(\s|$)`)

var (
	reRM          = regexp.MustCompile(`(?i)(^|[\s;&|(])rm(\s|$)`)
	reRMRecursive = regexp.MustCompile(`(?i)(^|\s)-[a-z]*r`)
	reRMForce     = regexp.MustCompile(`(?i)(^|\s)-[a-z]*f`)
)

// destructiveOther are commands that are destructive regardless of arguments.
var destructiveOther = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bmkfs(\.\w+)?\b`),                // format a filesystem
	regexp.MustCompile(`(?i)\bdd\b.*\bof=/dev/`),              // overwrite a raw device
	regexp.MustCompile(`(?i)>\s*/dev/(sd|nvme|hd|disk|vd)\w`), // redirect onto a disk
	regexp.MustCompile(`(?i)\b(shutdown|reboot|halt|poweroff)\b`),
	regexp.MustCompile(`(?i)\binit\s+[06]\b`),
	regexp.MustCompile(`:\(\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:`), // classic fork bomb
	regexp.MustCompile(`(?i)\bchmod\s+-R\s+[0-7]*7{3}\s+/`),        // chmod -R 777 /
	regexp.MustCompile(`(?i)\bmv\b.*\s/dev/null(\s|$)`),            // move into the void
}

// IsDestructive reports whether a shell command matches a known irreversible,
// machine-wrecking pattern.
func IsDestructive(command string) bool {
	norm := strings.Join(strings.Fields(command), " ")
	if norm == "" {
		return false
	}
	for _, re := range destructiveOther {
		if re.MatchString(norm) {
			return true
		}
	}
	// rm that is recursive AND forced AND aimed at a catastrophic target.
	if reRM.MatchString(norm) && reRMRecursive.MatchString(norm) &&
		reRMForce.MatchString(norm) && reCatastrophicTarget.MatchString(norm) {
		return true
	}
	return false
}
