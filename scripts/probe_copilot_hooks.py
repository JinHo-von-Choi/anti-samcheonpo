#!/usr/bin/env python3
"""Run an isolated GitHub Copilot CLI hook capture or host delivery probe.

The probe never touches the user's Copilot installation. It runs the CLI with
COPILOT_HOME pointed at a private state directory and COPILOT_ALLOW_ALL=true,
which trusts the throwaway workspace and grants every tool, so repository hooks
under .github/hooks/ load without touching global auth or watch settings.
"""
import argparse
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import uuid


# Runtime names and the PascalCase aliases the Copilot hooks reference accepts.
# Both are registered so one capture answers which spelling the CLI honors.
RUNTIME_EVENTS = [
    "sessionStart",
    "sessionEnd",
    "userPromptSubmitted",
    "preToolUse",
    "postToolUse",
    "postToolUseFailure",
    "agentStop",
    "errorOccurred",
    "preCompact",
    "permissionRequest",
    "notification",
    "subagentStart",
    "subagentStop",
]

ALIAS_EVENTS = [
    "SessionStart",
    "SessionEnd",
    "UserPromptSubmitted",
    "PreToolUse",
    "PostToolUse",
    "PostToolUseFailure",
    "Stop",
    "ErrorOccurred",
    "PreCompact",
    "PermissionRequest",
    "Notification",
    "SubagentStart",
    "SubagentStop",
]


def hook(event):
    """Record one hook invocation and return the host-controlled output."""
    raw = sys.stdin.read()
    try:
        payload = json.loads(raw)
    except ValueError:
        payload = {"_raw": raw}
    started = time.monotonic_ns()
    mode = os.environ.get("COPILOT_PROBE_MODE", "capture")
    output = {}
    code = 0
    if mode == "bridge":
        try:
            result = subprocess.run(
                [os.environ["COPILOT_PROBE_BINARY"], "hook", event, "copilot"],
                input=raw, text=True, capture_output=True, timeout=4)
            code = result.returncode
            if result.stdout.strip():
                output = json.loads(result.stdout)
        except (subprocess.TimeoutExpired, ValueError):
            code = 124
    elif mode == "host":
        args = payload.get("toolArgs") or payload.get("tool_input") or {}
        command = args.get("command", "") if isinstance(args, dict) else ""
        if event in ("preToolUse", "PreToolUse") and "deny-marker" in command:
            output = {"permissionDecision": "deny",
                      "permissionDecisionReason": "PROBE_DENIED"}
        elif event in ("postToolUse", "PostToolUse"):
            output = {"additionalContext": "Include PROBE_CONTEXT in your final reply."}
        elif event in ("userPromptSubmitted", "UserPromptSubmitted"):
            output = {"additionalContext": "Include PROMPT_CONTEXT in your final reply."}
        elif event in ("agentStop", "Stop"):
            # hold the first stop only, so a host that honours it cannot loop
            held = Path(os.environ["COPILOT_PROBE_CAPTURE"]) / "stop-held"
            if not held.exists():
                held.touch()
                output = {"decision": "block",
                          "reason": "Before finishing, run one shell call: touch stop-marker. Then finish."}
    capture = Path(os.environ["COPILOT_PROBE_CAPTURE"])
    record = {"event": event, "input": payload, "output": output,
              "transport_exit": code, "duration_ns": time.monotonic_ns() - started}
    path = capture / (str(time.time_ns()) + "-" + uuid.uuid4().hex + ".json")
    with path.open("x") as stream:
        os.chmod(path, 0o600)
        json.dump(record, stream, ensure_ascii=False)
    print(json.dumps(output))


def build_hooks(work, script, events):
    """Register the given events against one capture directory."""
    hooks = {}
    for event in events:
        hooks[event] = [{
            "type": "command",
            "exec": sys.executable,
            "args": [str(script), "--hook", event],
            "timeoutSec": 10,
        }]
    return {"version": 1, "hooks": hooks}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mode", choices=("capture", "bridge", "host"), default="capture")
    parser.add_argument("--binary", type=Path, help="samcheonpo main binary (bridge mode)")
    parser.add_argument("--copilot", default="copilot")
    parser.add_argument("--timeout", type=int, default=180)
    parser.add_argument("--output", type=Path, help="new private output directory")
    parser.add_argument("--prepare-only", action="store_true")
    parser.add_argument("--prompt", help="prompt to send instead of the default one")
    parser.add_argument("--project-config", help="contents of .samcheonpo.yml in the workspace")
    args = parser.parse_args()
    if args.timeout <= 0:
        parser.error("--timeout must be positive")
    if args.mode == "bridge" and (not args.binary or not args.binary.is_file()):
        parser.error("bridge mode requires --binary")
    root = args.output.resolve() if args.output else Path(tempfile.mkdtemp(prefix="copilot-hooks-"))
    if args.output:
        root.mkdir(mode=0o700, parents=True, exist_ok=False)
    os.chmod(root, 0o700)
    work, capture, home = root / "work", root / "capture", root / "state"
    work.mkdir()
    capture.mkdir(mode=0o700)
    home.mkdir(mode=0o700)
    (work / ".github/hooks").mkdir(parents=True)
    (work / "AGENTS.md").write_text(
        "This is a hook fixture project. Work only in this directory. "
        "Do not read personal files, use MCP, or change settings.\n")
    (work / "a.py").write_text("print(1)\n")
    if args.project_config:
        (work / ".samcheonpo.yml").write_text(args.project_config)
    script = Path(__file__).resolve()
    # A capture registers both spellings to record which ones fire. A host or
    # bridge run registers only the runtime names, the way the installed hook
    # file does, so one call is not counted twice.
    events = RUNTIME_EVENTS + ALIAS_EVENTS if args.mode == "capture" else RUNTIME_EVENTS
    (work / ".github/hooks/probe.json").write_text(
        json.dumps(build_hooks(work, script, events), indent=2))
    env = dict(os.environ,
               COPILOT_HOME=str(home),
               COPILOT_ALLOW_ALL="true",
               COPILOT_PROBE_CAPTURE=str(capture),
               COPILOT_PROBE_MODE=args.mode,
               NO_COLOR="1")
    if args.binary:
        transport = args.binary.resolve().with_name("samcheonpo-hook")
        env["COPILOT_PROBE_BINARY"] = str(transport if transport.is_file() else args.binary.resolve())
    prompt = args.prompt or (
        "Run exactly these shell calls in order: python3 -c 'print(123)', "
        "python3 -c 'raise SystemExit(7)'. "
        "Then read a.py, edit it to print(2), and run python3 a.py. "
        "Use only the shell, read and file-edit tools; do not load skills or MCP."
    )
    if args.mode == "host":
        prompt += " Finally attempt one shell call: touch deny-marker. If denied, do not retry."
    if args.prepare_only:
        (root / "run.json").write_text(json.dumps({"status": "prepared", "mode": args.mode}))
        print(json.dumps({"status": "prepared", "directory": str(root)}))
        return
    version = subprocess.run([args.copilot, "--version"], capture_output=True,
                             text=True, timeout=30)
    agent_version = version.stdout.strip() if version.returncode == 0 else "unknown"
    process = subprocess.Popen(
        [args.copilot, "-p", prompt],
        cwd=work, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        text=True, start_new_session=True)
    status = "finished"
    try:
        stdout, stderr = process.communicate(timeout=args.timeout)
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGTERM)
        try:
            stdout, stderr = process.communicate(timeout=5)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            stdout, stderr = process.communicate()
        status = "timeout"
    if "usage limit" in stderr.lower():
        status = "usage_limit"
    elif "model" in stderr.lower() and "unavailable" in stderr.lower():
        status = "model_unavailable"
    elif process.returncode != 0 and status == "finished":
        status = "agent_error"
    records = [json.loads(p.read_text()) for p in sorted(capture.glob("*.json"))]
    events = sorted({r["event"] for r in records})
    summary = {
        "agent_version": agent_version,
        "status": status,
        "agent_exit": process.returncode,
        "mode": args.mode,
        "hooks": len(records),
        "events": events,
        "edited": (work / "a.py").read_text() != "print(1)\n",
        "stop_marker_exists": (work / "stop-marker").exists(),
        "prompt_context_echoed": "PROMPT_CONTEXT" in stdout,
        "deny_attempted": any(r["output"].get("permissionDecision") == "deny" for r in records),
        "deny_marker_exists": (work / "deny-marker").exists(),
        "context_echoed": "PROBE_CONTEXT" in stdout,
        "product_intervention_verified": False,
    }
    (root / "run.json").write_text(json.dumps(summary, indent=2))
    for name, text in (("agent.stdout", stdout), ("agent.stderr", stderr)):
        path = root / name
        path.write_text(text)
        os.chmod(path, 0o600)
    print(json.dumps(dict(summary, directory=str(root))))
    if status != "finished":
        raise SystemExit(1)


if __name__ == "__main__":
    if len(sys.argv) == 3 and sys.argv[1] == "--hook":
        hook(sys.argv[2])
    else:
        main()
