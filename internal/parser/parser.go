// Package parser splits a PRISMAG prompt into routed tasks.
//
// Syntax (see PROJECT.md → DSL):
//
//	shared preamble (optional)
//	@@<alias>: <task description>
//	@@<alias>: <task that may span
//	           multiple lines>
//
// A tag must sit at the start of a line (optionally indented), which keeps
// mid-text colons ("ratio 3:1") and email-like "user@host:" strings from being
// mistaken for tags. The canonical trigger is "@@"; a single "@" is also
// accepted because a bare "@" only collides with the IDE's mention menu, not the
// terminal.
package parser

import (
	"regexp"
	"strings"
)

// RoutedTask is one tagged block of work.
type RoutedTask struct {
	Alias    string // normalized alias for registry lookup (lowercased)
	RawAlias string // alias exactly as written, for display
	Task     string // task body, trimmed
	Index    int    // 0-based position in the prompt
}

// ParsedPrompt is the result of parsing a prompt.
type ParsedPrompt struct {
	Preamble string       // shared context before the first tag (may be empty)
	Tasks    []RoutedTask // tasks in the order they appear
}

// Line-anchored: optional indent, one or two '@', an alias, optional spaces, ':'.
// Aliases may contain letters, digits, and . _ - (so "composer2.5" works).
var tagRE = regexp.MustCompile(`(?m)^[ \t]*@@?([A-Za-z][A-Za-z0-9_.-]*)[ \t]*:`)

// looksTaggedRE matches a line that starts with @@ but is not a well-formed tag,
// so we can tell "you forgot a colon / used a bad char" from "no tags at all".
var looksTaggedRE = regexp.MustCompile(`(?m)^[ \t]*@@`)

// Parse splits input into a shared preamble and its routed tasks.
func Parse(input string) ParsedPrompt {
	// Each loc is [matchStart, matchEnd, aliasStart, aliasEnd].
	locs := tagRE.FindAllStringSubmatchIndex(input, -1)
	if len(locs) == 0 {
		return ParsedPrompt{Preamble: strings.TrimSpace(input)}
	}

	preamble := strings.TrimSpace(input[:locs[0][0]])
	tasks := make([]RoutedTask, 0, len(locs))

	for i, loc := range locs {
		rawAlias := input[loc[2]:loc[3]]
		bodyStart := loc[1]
		bodyEnd := len(input)
		if i+1 < len(locs) {
			bodyEnd = locs[i+1][0]
		}
		tasks = append(tasks, RoutedTask{
			Alias:    strings.ToLower(rawAlias),
			RawAlias: rawAlias,
			Task:     strings.TrimSpace(input[bodyStart:bodyEnd]),
			Index:    i,
		})
	}

	return ParsedPrompt{Preamble: preamble, Tasks: tasks}
}

// HasTags reports whether input contains at least one @@alias: (or @alias:) tag.
func HasTags(input string) bool {
	return tagRE.MatchString(input)
}

// LooksTagged reports whether the input has a line starting with @@ that did not
// parse as a valid tag — used to give a helpful error instead of "no tags found".
func LooksTagged(input string) bool {
	return !tagRE.MatchString(input) && looksTaggedRE.MatchString(input)
}
