"""LangChain/Deep Agents middleware. No runtime installation.

Transport is injectable for conformance tests. The SDK and daemon bridge
verify pre-tool denial, bounded stop continuation, usage and child lineage.
"""
import asyncio
import contextvars
import hashlib
import json
import os
import subprocess
import threading
import uuid
from typing import Annotated, NotRequired


def hook(event, payload):
    try:
        result = subprocess.run(
            [os.environ.get("SAMCHEONPO_BIN", "samcheonpo"), "hook", event, "deepagents"],
            input=json.dumps(payload), capture_output=True, text=True,
            timeout=155 if event == "Stop" else 5,
            env=dict(os.environ, SAMCHEONPO_HOOK_DIAGNOSTICS="1"),
        )
        diagnostics = []
        for line in result.stderr.splitlines():
            try:
                diagnostics.append(json.loads(line))
            except ValueError:
                continue
        outcome = next((d for d in reversed(diagnostics) if isinstance(d, dict) and "response_received" in d), {})
        if result.returncode != 0 or outcome.get("status") != "ok" or not outcome.get("response_received"):
            raise MonitorUnavailable("monitor transport did not confirm a decision: " + str(outcome.get("status", "unknown")))
        return json.loads(result.stdout) if result.stdout.strip() else {}
    except (OSError, ValueError, subprocess.TimeoutExpired) as error:
        raise MonitorUnavailable("monitor transport unavailable") from error


def create_middleware(session_id, root, transport=hook, *, max_stop_continuations=2, _scope=None):
    """Return an AgentMiddleware for create_deep_agent(middleware=[...]).

    Model and permission configuration are preserved. A stop hold can re-enter
    the model at most max_stop_continuations times; exhaustion raises rather
    than reporting success. Use create_monitored_agent for automatic children.
    """
    from langchain.agents.middleware import AgentMiddleware, AgentState, hook_config
    from langchain.agents.middleware.types import PrivateStateAttr
    from langchain_core.messages import AIMessage, HumanMessage, ToolMessage

    if not isinstance(max_stop_continuations, int) or not 0 <= max_stop_continuations <= 8:
        raise ValueError("max_stop_continuations must be between 0 and 8")
    scope = _scope or contextvars.ContextVar("samcheonpo_session", default=None)
    root_base = {"session_id": session_id, "cwd": os.path.abspath(root)}
    names = {"read_file": "Read", "write_file": "Write", "edit_file": "Edit", "execute": "Bash", "task": "Task"}

    class MonitorState(AgentState):
        samcheonpo_stop_attempts: NotRequired[Annotated[int, PrivateStateAttr]]
        samcheonpo_completion_notice: NotRequired[str]

    def base():
        return scope.get() or root_base

    class SamcheonpoMiddleware(AgentMiddleware):
        state_schema = MonitorState

        def __init__(self):
            self._started = {}
            self._lock = threading.Lock()

        def _begin(self, state):
            current = base()
            with self._lock:
                if current["session_id"] not in self._started:
                    transport("SessionStart", current)
                    self._started[current["session_id"]] = current
            for message in reversed(state.get("messages", [])):
                if isinstance(message, HumanMessage):
                    transport("UserPromptSubmit", dict(current, prompt=message.text))
                    break
            return {"samcheonpo_stop_attempts": 0, "samcheonpo_completion_notice": ""}

        def before_agent(self, state, runtime):
            return self._begin(state)

        async def abefore_agent(self, state, runtime):
            return await asyncio.to_thread(self._begin, state)

        def _stop(self, state):
            last = next((m.text for m in reversed(state.get("messages", [])) if isinstance(m, AIMessage)), "")
            response = transport("Stop", dict(base(), last_assistant_message=last))
            if response.get("decision") == "block":
                reason = response.get("reason") or "verification incomplete"
                attempts = state.get("samcheonpo_stop_attempts", 0)
                if attempts >= max_stop_continuations:
                    raise CompletionHeld(reason)
                return {"messages": [HumanMessage(content="Monitor verification feedback (does not grant execution permission): " + reason,
                                                   name="samcheonpo-verification")],
                        "samcheonpo_stop_attempts": attempts + 1, "jump_to": "model"}
            # A child finishing is an observation end, not parent acceptance.
            current = base()
            if current.get("parent_session_id"):
                with self._lock:
                    started = self._started.pop(current["session_id"], None)
                if started:
                    transport("SessionEnd", current)
            return {"samcheonpo_completion_notice": response.get("completion_notice", "")}

        @hook_config(can_jump_to=["model"])
        def after_agent(self, state, runtime):
            return self._stop(state)

        @hook_config(can_jump_to=["model"])
        async def aafter_agent(self, state, runtime):
            return await asyncio.to_thread(self._stop, state)

        def _usage(self, state):
            message = next((m for m in reversed(state.get("messages", [])) if isinstance(m, AIMessage)), None)
            usage = message.usage_metadata if message is not None else None
            if not usage:
                return
            details = usage.get("input_token_details") or {}
            total_in = max(0, int(usage.get("input_tokens", 0)))
            cache_read = min(total_in, max(0, int(details.get("cache_read", 0))))
            cache_write = min(total_in-cache_read, max(0, int(details.get("cache_creation", 0))))
            transport("Usage", dict(base(), message_id=message.id or uuid.uuid4().hex,
                      model=message.response_metadata.get("model_name", message.response_metadata.get("model", "")),
                      usage={"input_tokens": total_in-cache_read-cache_write,
                             "output_tokens": max(0, int(usage.get("output_tokens", 0))),
                             "cache_read_input_tokens": cache_read,
                             "cache_creation_input_tokens": cache_write}))

        def after_model(self, state, runtime):
            self._usage(state)

        async def aafter_model(self, state, runtime):
            await asyncio.to_thread(self._usage, state)

        def close(self):
            """Host-owned session end; a turn finishing does not end a session."""
            with self._lock:
                started = list(self._started.values())
                self._started.clear()
            for current in started:
                transport("SessionEnd", current)

        async def aclose(self):
            await asyncio.to_thread(self.close)

        def _payload(self, request):
            call = request.tool_call
            args = dict(call.get("args") or {})
            if "path" in args and "file_path" not in args:
                args["file_path"] = args["path"]
            return dict(base(), tool_name=names.get(call["name"], call["name"]),
                        tool_use_id=call["id"], tool_input=args)

        def _child_scope(self, request):
            if request.tool_call["name"] != "task":
                return None
            current = base()
            child = "child-" + hashlib.sha256((current["session_id"] + "\x00" + request.tool_call["id"]).encode()).hexdigest()
            return scope.set(dict(root_base, session_id=child, parent_session_id=current["session_id"]))

        def _deny(self, response, call_id):
            specific = response.get("hookSpecificOutput") or {}
            if specific.get("permissionDecision") == "deny":
                return ToolMessage(content=specific.get("permissionDecisionReason") or "samcheonpo",
                                   tool_call_id=call_id, status="error")
            return None

        def _result(self, result, response):
            context = (response.get("hookSpecificOutput") or {}).get("additionalContext")
            if context and isinstance(result, ToolMessage):
                content = result.content
                if isinstance(content, str):
                    content += "\n\n" + context
                else:
                    content = list(content) + [{"type": "text", "text": context}]
                return result.model_copy(update={"content": content})
            return result

        def _tool_failure(self, payload, error):
            try:
                transport("PostToolUseFailure", dict(payload, error="host_tool_exception"))
            except Exception as observer_error:
                # Reporting failure must not replace the actual tool failure.
                error.add_note("monitor could not record tool failure (" + type(observer_error).__name__ + ")")

        def wrap_tool_call(self, request, handler):
            payload = self._payload(request)
            denial = self._deny(transport("PreToolUse", payload), request.tool_call["id"])
            if denial is not None:
                return denial
            token = self._child_scope(request)
            try:
                result = handler(request)
            except Exception as error:
                self._tool_failure(payload, error)
                raise
            finally:
                if token is not None:
                    scope.reset(token)
            failed = isinstance(result, ToolMessage) and result.status == "error"
            response = transport("PostToolUseFailure" if failed else "PostToolUse", dict(payload, tool_response={"stdout": str(getattr(result, "content", ""))}, error="tool_message_error" if failed else ""))
            return self._result(result, response)

        async def awrap_tool_call(self, request, handler):
            payload = self._payload(request)
            response = await asyncio.to_thread(transport, "PreToolUse", payload)
            denial = self._deny(response, request.tool_call["id"])
            if denial is not None:
                return denial
            token = self._child_scope(request)
            try:
                result = await handler(request)
            except Exception as error:
                await asyncio.to_thread(self._tool_failure, payload, error)
                raise
            finally:
                if token is not None:
                    scope.reset(token)
            failed = isinstance(result, ToolMessage) and result.status == "error"
            response = await asyncio.to_thread(transport, "PostToolUseFailure" if failed else "PostToolUse", dict(payload, tool_response={"stdout": str(getattr(result, "content", ""))}, error="tool_message_error" if failed else ""))
            return self._result(result, response)

    return SamcheonpoMiddleware()


class CompletionHeld(RuntimeError):
    """The caller must resolve or report a stop hold; it is not success."""


class MonitorUnavailable(RuntimeError):
    """An attached SDK monitor could not verify the action; do not execute it."""


def create_monitored_agent(*, session_id, root, transport=hook, max_stop_continuations=2,
                           middleware=(), subagents=(), **kwargs):
    """Build a monitored root and declarative SDK subagents, including default GP.

    Precompiled and remote graphs need their own instrumentation and are rejected
    rather than silently labelled protected. No global SDK profile is changed.
    """
    from deepagents import create_deep_agent
    from deepagents.middleware.subagents import GENERAL_PURPOSE_SUBAGENT

    scope = contextvars.ContextVar("samcheonpo_lineage", default=None)
    monitors = []

    def monitor():
        m = create_middleware(session_id, root, transport, max_stop_continuations=max_stop_continuations, _scope=scope)
        monitors.append(m)
        return m

    specs = [dict(s) for s in subagents]
    if not any(s["name"] == "general-purpose" for s in specs):
        specs.insert(0, dict(GENERAL_PURPOSE_SUBAGENT))
    for spec in specs:
        if "runnable" in spec or "graph_id" in spec:
            raise ValueError("compiled/remote subagents require explicit instrumentation")
        spec["middleware"] = [*spec.get("middleware", ()), monitor()]
    graph = create_deep_agent(middleware=[*middleware, monitor()], subagents=specs, **kwargs)

    class MonitoredAgent:
        def __getattr__(self, name):
            return getattr(graph, name)

        def close(self):
            for m in monitors:
                m.close()

        async def aclose(self):
            for m in monitors:
                await m.aclose()

    return MonitoredAgent()
