// samcheonpo forwarder for opencode: passes tool and session events to the
// samcheonpo hook client in the Claude hook dialect and applies its decision.
// No judgement happens here; the daemon decides.
import { spawnSync } from "node:child_process"

const BIN = process.env.SAMCHEONPO_BIN ?? "samcheonpo"

const TOOLS: Record<string, string> = {
  bash: "Bash", read: "Read", write: "Write", edit: "Edit", glob: "Glob", grep: "Grep", list: "LS",
  patch: "apply_patch", webfetch: "WebFetch", todowrite: "TodoWrite", task: "Task",
}

function input(tool: string, args: any): any {
  if (!args) return {}
  switch (tool) {
    case "read": return { file_path: args.filePath }
    case "write": return { file_path: args.filePath, content: args.content }
    case "edit": return { file_path: args.filePath, old_string: args.oldString, new_string: args.newString, replace_all: !!args.replaceAll }
    case "patch": return { command: args.patchText ?? args.patch }
    default: return args
  }
}

function hook(event: string, payload: any): any {
  const r = spawnSync(BIN, ["hook", event, "opencode"], { input: JSON.stringify({ hook_event_name: event, ...payload }), encoding: "utf8", timeout: event === "Stop" ? 160000 : 3000 })
  const out = (r.stdout ?? "").trim()
  if (!out) return {}
  try { return JSON.parse(out) } catch { return {} }
}

export const Samcheonpo = async ({ client, directory }: any) => {
  const usage = new Map<string, number>()
  return {
    "chat.message": async (inp: any, out: any) => {
      const text = (out.parts ?? []).filter((p: any) => p.type === "text").map((p: any) => p.text).join("\n")
      const r = hook("UserPromptSubmit", { session_id: inp.sessionID, cwd: directory, prompt: text })
      const ctx = r.hookSpecificOutput?.additionalContext
      // parts carry ids owned by opencode: extend the last text part
      const last = [...(out.parts ?? [])].reverse().find((p: any) => p.type === "text")
      if (ctx && last) last.text = `${last.text}\n\n${ctx}`
    },
    "tool.execute.before": async (inp: any, out: any) => {
      const r = hook("PreToolUse", { session_id: inp.sessionID, cwd: directory, tool_name: TOOLS[inp.tool] ?? inp.tool, tool_use_id: inp.callID, tool_input: input(inp.tool, out.args) })
      if (r.hookSpecificOutput?.permissionDecision === "deny") throw new Error(r.hookSpecificOutput.permissionDecisionReason ?? "samcheonpo")
    },
    "tool.execute.after": async (inp: any, out: any) => {
      const exit = out.metadata?.exit
      const failed = typeof exit === "number" && exit !== 0
      const r = hook(failed ? "PostToolUseFailure" : "PostToolUse", {
        session_id: inp.sessionID, cwd: directory, tool_name: TOOLS[inp.tool] ?? inp.tool, tool_use_id: inp.callID, tool_input: input(inp.tool, inp.args),
        tool_response: { stdout: out.output ?? "" }, error: failed ? `Exit code ${exit}\n${out.output ?? ""}` : undefined,
      })
      const ctx = r.hookSpecificOutput?.additionalContext ?? r.reason
      if (ctx) out.output = `${out.output ?? ""}\n\n${ctx}`
    },
    event: async ({ event }: any) => {
      const p = event.properties ?? {}
      if (event.type === "session.created") hook("SessionStart", { session_id: p.info?.id, cwd: directory })
      if (event.type === "session.deleted") hook("SessionEnd", { session_id: p.info?.id, cwd: directory })
      if (event.type === "session.compacted") hook("PreCompact", { session_id: p.sessionID, cwd: directory })
      if (event.type === "message.updated" && p.info?.role === "assistant" && p.info?.tokens) {
        const t = p.info.tokens, total = (t.input ?? 0) + (t.output ?? 0) + (t.cache?.read ?? 0) + (t.cache?.write ?? 0)
        if (total > (usage.get(p.info.id) ?? 0)) {
          usage.set(p.info.id, total)
          hook("Usage", { session_id: p.info.sessionID, cwd: directory, message_id: p.info.id, model: p.info.modelID,
            usage: { input_tokens: t.input ?? 0, output_tokens: (t.output ?? 0) + (t.reasoning ?? 0), cache_read_input_tokens: t.cache?.read ?? 0, cache_creation_input_tokens: t.cache?.write ?? 0 } })
        }
      }
      if (event.type === "session.idle") {
        const r = hook("Stop", { session_id: p.sessionID, cwd: directory, stop_hook_active: false })
        // opencode has no stop blocking: a block becomes one follow-up prompt
        if (r.decision === "block" && r.reason && client?.session?.prompt) {
          await client.session.prompt({ path: { id: p.sessionID }, body: { parts: [{ type: "text", text: r.reason }] } }).catch(() => {})
        }
      }
    },
  }
}
