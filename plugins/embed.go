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

// HermesInit and HermesManifest are the Hermes forwarder plugin.
//
//go:embed hermes/__init__.py
var HermesInit []byte

//go:embed hermes/plugin.yaml
var HermesManifest []byte

// OpenclawIndex, OpenclawManifest and OpenclawPackage are the OpenClaw
// forwarder plugin.
//
//go:embed openclaw/index.js
var OpenclawIndex []byte

//go:embed openclaw/openclaw.plugin.json
var OpenclawManifest []byte

//go:embed openclaw/package.json
var OpenclawPackage []byte
