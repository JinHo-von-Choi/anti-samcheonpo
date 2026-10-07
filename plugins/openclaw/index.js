// samcheonpo forwarder for OpenClaw: passes tool and turn events to the
// samcheonpo hook client in the Claude hook dialect and applies its decision.
// No judgement happens here; the daemon decides.
import { spawnSync } from "node:child_process"
import path from "node:path"

const BIN = process.env.SAMCHEONPO_BIN ?? "samcheonpo"

const TOOLS = { exec: "Bash", read: "Read", write: "Write", edit: "Edit", apply_patch: "apply_patch", grep: "Grep", glob: "Glob" }

function hook(event, payload) {
  const r = spawnSync(BIN, ["hook", event, "openclaw"], { input: JSON.stringify({ hook_event_name: event, ...payload }), encoding: "utf8", timeout: event === "Stop" ? 160000 : 3000 })
  const out = (r.stdout ?? "").trim()
  if (!out) return {}
  try { return JSON.parse(out) } catch { return {} }
}

function context(r) {
  return r.hookSpecificOutput?.additionalContext ?? ""
}

export default {
  id: "samcheonpo",
  name: "samcheonpo",
  description: "samcheonpo forwarder",
  register(api) {
    // tool hooks carry the session key but not the workspace or session id
    const sessions = new Map() // sessionKey -> { id, cwd }
    const calls = new Map() // toolCallId -> { tool, input }
    const session = (ctx) => sessions.get(ctx?.sessionKey) ?? { id: ctx?.sessionId ?? ctx?.sessionKey, cwd: ctx?.workspaceDir ?? process.cwd() }
    const abs = (cwd, p) => (typeof p === "string" && p ? path.resolve(cwd, p) : p)
    const input = (cwd, tool, a = {}) => {
      switch (tool) {
        case "exec": return { command: a.command ?? "", workdir: abs(cwd, a.workdir ?? a.cwd) }
        case "read": return { file_path: abs(cwd, a.path ?? a.file_path) }
        case "write": return { file_path: abs(cwd, a.path ?? a.file_path), content: a.content ?? "" }
        case "edit": return { file_path: abs(cwd, a.path ?? a.file_path), old_string: a.oldText ?? a.old_string ?? "", new_string: a.newText ?? a.new_string ?? "" }
        case "apply_patch": return { command: a.input ?? a.patch ?? "" }
        default: return a
      }
    }

    api.on("before_prompt_build", async (event, ctx) => {
      const s = { id: ctx.sessionId ?? ctx.sessionKey, cwd: ctx.workspaceDir ?? process.cwd() }
      sessions.set(ctx.sessionKey, s)
      const c = context(hook("UserPromptSubmit", { session_id: s.id, cwd: s.cwd, prompt: event.prompt ?? "" }))
      return c ? { prependContext: c } : undefined
    })

    api.on("before_tool_call", async (event, ctx) => {
      const s = session(ctx)
      const tin = input(s.cwd, event.toolName, event.params)
      const r = hook("PreToolUse", { session_id: s.id, cwd: s.cwd, tool_name: TOOLS[event.toolName] ?? event.toolName, tool_use_id: event.toolCallId, tool_input: tin })
      if (r.hookSpecificOutput?.permissionDecision === "deny") return { block: true, blockReason: r.hookSpecificOutput.permissionDecisionReason ?? "samcheonpo" }
      calls.set(event.toolCallId, { tool: event.toolName, input: tin })
    })

    // observation only: OpenClaw has no hook that changes the tool result the
    // model reads within the run (tool_result_persist rewrites the stored
    // transcript only), so advice waits for the next turn's prompt build
    api.on("after_tool_call", async (event, ctx) => {
      const call = calls.get(event.toolCallId)
      if (!call) return // blocked or unknown
      calls.delete(event.toolCallId)
      const s = session(ctx)
      const text = (event.result?.content ?? []).filter((p) => p.type === "text").map((p) => p.text).join("\n")
      const exit = event.result?.details?.exitCode
      const failed = typeof exit === "number" ? exit !== 0 : !!event.error
      hook(failed ? "PostToolUseFailure" : "PostToolUse", {
        session_id: s.id, cwd: s.cwd, tool_name: TOOLS[call.tool] ?? call.tool, tool_use_id: event.toolCallId, tool_input: call.input,
        tool_response: { stdout: text }, error: failed ? (typeof exit === "number" ? `Exit code ${exit}\n${text}` : String(event.error ?? text ?? "error")) : undefined,
      })
    })

    api.on("llm_output", async (event, ctx) => {
      const u = event.usage ?? event.lastAssistant?.usage
      if (!u || !((u.input ?? 0) + (u.output ?? 0) + (u.cacheRead ?? 0) + (u.cacheWrite ?? 0))) return
      const s = session(ctx)
      hook("Usage", { session_id: s.id, cwd: s.cwd, message_id: event.runId, model: event.model,
        usage: { input_tokens: u.input ?? 0, output_tokens: u.output ?? 0, cache_read_input_tokens: u.cacheRead ?? 0, cache_creation_input_tokens: u.cacheWrite ?? 0 } })
    })

    api.on("before_agent_finalize", async (event, ctx) => {
      const s = session(ctx)
      const r = hook("Stop", { session_id: s.id, cwd: event.cwd ?? s.cwd, stop_hook_active: !!event.stopHookActive, last_assistant_message: event.lastAssistantMessage ?? "" })
      if (r.decision === "block" && r.reason) return { action: "revise", reason: r.reason, retry: { instruction: r.reason, idempotencyKey: `samcheonpo-${event.runId}`, maxAttempts: 1 } }
    }, { timeoutMs: 160000 })

    api.on("session_start", async (event, ctx) => {
      const s = session(ctx)
      hook("SessionStart", { session_id: s.id, cwd: s.cwd, source: "startup" })
    })

    api.on("session_end", async (event, ctx) => {
      const s = session(ctx)
      hook("SessionEnd", { session_id: s.id, cwd: s.cwd, reason: event.reason ?? "exit" })
      sessions.delete(ctx?.sessionKey)
    })
  },
}
