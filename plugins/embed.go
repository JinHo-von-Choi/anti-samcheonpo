// Package plugins embeds the agent plugin files installed by `samcheonpo install`.
package plugins

import "embed"

// ClaudeCode is the Claude Code plugin directory.
//
//go:embed all:claude-code
var ClaudeCode embed.FS

// Opencode is the opencode forwarder plugin.
//
//go:embed opencode/samcheonpo.ts
var Opencode []byte
