# samcheonpo forwarder for Hermes: passes tool and turn events to the
# samcheonpo hook client in the Claude hook dialect and applies its decision.
# No judgement happens here; the daemon decides.
import json
import os
import subprocess

BIN = os.environ.get("SAMCHEONPO_BIN") or "samcheonpo"

TOOLS = {"terminal": "Bash", "read_file": "Read", "write_file": "Write", "patch": "Edit", "search_files": "Grep"}


def _path(p):
    return os.path.abspath(os.path.expanduser(p)) if isinstance(p, str) and p else p


def _input(tool, args):
    a = args if isinstance(args, dict) else {}
    if tool == "terminal":
        return {"command": a.get("command", ""), "workdir": _path(a.get("workdir"))}
    if tool == "read_file":
        return {"file_path": _path(a.get("path"))}
    if tool == "write_file":
        return {"file_path": _path(a.get("path")), "content": a.get("content", "")}
    if tool == "patch":
        return {"file_path": _path(a.get("path")), "old_string": a.get("old_string", ""),
                "new_string": a.get("new_string", ""), "replace_all": bool(a.get("replace_all"))}
    return a


def _hook(event, payload):
    payload = dict(payload, hook_event_name=event)
    payload.setdefault("cwd", os.getcwd())
    try:
        r = subprocess.run([BIN, "hook", event, "hermes"], input=json.dumps(payload, default=str),
                           capture_output=True, text=True, timeout=160 if event == "Stop" else 3)
        out = (r.stdout or "").strip()
        return json.loads(out) if out else {}
    except Exception:
        return {}


def _context(r):
    hs = r.get("hookSpecificOutput") or {}
    return hs.get("additionalContext") or ""


def register(ctx):
    verified = set()  # turns whose end went through pre_verify

    def on_session_start(session_id="", **_):
        _hook("SessionStart", {"session_id": session_id, "source": "startup"})

    def pre_llm_call(session_id="", user_message="", **_):
        c = _context(_hook("UserPromptSubmit", {"session_id": session_id, "prompt": user_message or ""}))
        return {"context": c} if c else None

    def pre_tool_call(tool_name="", args=None, session_id="", tool_call_id="", **_):
        r = _hook("PreToolUse", {"session_id": session_id, "tool_name": TOOLS.get(tool_name, tool_name),
                                 "tool_use_id": tool_call_id, "tool_input": _input(tool_name, args)})
        hs = r.get("hookSpecificOutput") or {}
        if hs.get("permissionDecision") == "deny":
            return {"action": "block", "message": hs.get("permissionDecisionReason") or "samcheonpo"}
        return None

    def transform_tool_result(tool_name="", args=None, result=None, session_id="", tool_call_id="", status="", error_message=None, **_):
        text = result if isinstance(result, str) else json.dumps(result, default=str)
        out, failed, err = text, status == "error", error_message or ""
        if tool_name == "terminal":
            try:
                body = json.loads(text)
                out = body.get("output") or ""
                code = body.get("exit_code")
                failed = isinstance(code, int) and code != 0
                err = "Exit code %d\n%s" % (code, out) if failed else ""
            except Exception:
                pass
        event = "PostToolUseFailure" if failed else "PostToolUse"
        payload = {"session_id": session_id, "tool_name": TOOLS.get(tool_name, tool_name), "tool_use_id": tool_call_id,
                   "tool_input": _input(tool_name, args), "tool_response": {"stdout": out}}
        if failed:
            payload["error"] = err or "error"
        r = _hook(event, payload)
        c = _context(r) or r.get("reason") or ""
        return text + "\n\n" + c if c else None

    def post_api_request(session_id="", api_request_id="", model="", response_model="", usage=None, **_):
        u = usage if isinstance(usage, dict) else {}
        if not api_request_id or not u:
            return
        _hook("Usage", {"session_id": session_id, "message_id": api_request_id, "model": response_model or model,
                        "usage": {"input_tokens": u.get("input_tokens") or 0,
                                  "output_tokens": (u.get("output_tokens") or 0) + (u.get("reasoning_tokens") or 0),
                                  "cache_read_input_tokens": u.get("cache_read_tokens") or 0,
                                  "cache_creation_input_tokens": u.get("cache_write_tokens") or 0}})

    def pre_verify(session_id="", final_response="", attempt=0, **kw):
        verified.add(session_id)
        r = _hook("Stop", {"session_id": session_id, "stop_hook_active": bool(attempt), "last_assistant_message": final_response or ""})
        if r.get("decision") == "block" and r.get("reason"):
            return {"action": "continue", "message": r["reason"]}
        return None

    def post_llm_call(session_id="", assistant_response="", **_):
        # turns without edited code skip pre_verify: report the end without a hold
        if session_id in verified:
            verified.discard(session_id)
            return
        _hook("Stop", {"session_id": session_id, "stop_hook_active": True, "last_assistant_message": assistant_response or ""})

    def on_session_finalize(session_id="", reason="", **_):
        if session_id:
            _hook("SessionEnd", {"session_id": session_id, "reason": reason or "exit"})

    for name, fn in (("on_session_start", on_session_start), ("pre_llm_call", pre_llm_call), ("pre_tool_call", pre_tool_call),
                     ("transform_tool_result", transform_tool_result), ("post_api_request", post_api_request),
                     ("pre_verify", pre_verify), ("post_llm_call", post_llm_call), ("on_session_finalize", on_session_finalize)):
        ctx.register_hook(name, fn)
