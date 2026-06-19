// Package adapters ships IDE integration templates embedded in the binary.
package adapters

import "embed"

// CursorAgents holds subagent templates for `prismag connect cursor`.
//
//go:embed cursor/agents/*.md
var CursorAgents embed.FS

const CursorAgentsDir = "cursor/agents"

// ClaudeAgents holds subagent templates for `prismag connect claude`.
//
//go:embed claude/agents/*.md
var ClaudeAgents embed.FS

const ClaudeAgentsDir = "claude/agents"
