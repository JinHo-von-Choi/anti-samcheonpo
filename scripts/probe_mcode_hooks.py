#!/usr/bin/env python3
"""Run a MiniMax Code (mcode) hook capture or host delivery probe.

mcode keeps its sign-in inside ~/.minimax and rotates the refresh token, so a
copied HOME cannot sign in and could log the real installation out. The probe
therefore uses the real ~/.minimax: by default it writes ~/.minimax/hooks.json
(refusing to overwrite one) for the length of one run and removes it
afterwards. The hook answers only calls whose cwd is the probe's own temporary
workspace; every other session gets an empty answer at once. A runtime reads
the file when it starts, so sessions that were already running never call it.

In 0.6.13 only the user file runs without further steps, and only when its top
level says "enabled": true. A project file (.minimax/hooks.json) also needs an
enablement record, and a local-marketplace plugin's hooks did not run at all;
both targets stay selectable to recheck later versions.

  probe_mcode_hooks.py run --out DIR [--mode capture|host|bridge] [--scenario NAME]
                            [--install project|user|plugin]
  probe_mcode_hooks.py hook EVENT       (called by the CLI, not by people)

Modes:
  capture  record payloads and answer nothing
  host     answer with fixed decisions to see what the CLI delivers:
           PreToolUse blocks a command containing PROBE_DENY, PostToolUse adds
           context PROBE_ADVICE, the first Stop holds with PROBE_HOLD
  bridge   forward each payload to `samcheonpo-hook hook EVENT mcode`
"""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time


EVENTS = [
    "SessionStart",
    "SessionEnd",
    "UserPromptSubmit",
    "PreToolUse",
    "PermissionRequest",
    "PostToolUse",
    "SubagentStart",
    "SubagentStop",
    "Stop",
    "PreCompact",
    "PostCompact",
]

SCENARIOS = {
    # synchronous success and failure, write, edit, read
    "shell": (
        "Run these steps in order and nothing else, then reply DONE. "
        "1) run the shell command `echo probe-ok`. "
        "2) run the shell command `sh -c 'echo probe-fail >&2; exit 7'`. "
        "3) write a file named note.txt containing the single line alpha. "
        "4) replace alpha with beta in note.txt using the edit tool. "
        "5) read note.txt."
    ),
    # a command long enough for the CLI to move it to a background task
    "background": (
        "Run the shell command `sleep 75; echo bg-done; exit 3` exactly once. "
        "If it moves to the background, wait for it with the task output tool "
        "until it finishes, then reply with its exit status and stop. Do nothing else."
    ),
    # a subagent so SubagentStart/SubagentStop and the child session id show up
    "subagent": (
        "Delegate one subtask to a subagent: the subagent runs the shell command "
        "`echo from-child` and reports its output. Then reply DONE. Do nothing else."
    ),
    # bridge mode: the same failure again and again, for samcheonpo to answer
    "repeat": (
        "Write a file test_probe.py containing exactly: def test_probe():\n    assert 1 == 2\n "
        "Then run `python3 -m pytest -q test_probe.py` and look at the result. Do not change any file. "
        "Run exactly the same command again, up to 5 times in total. If a run is refused or a hook "
        "message appears, stop running it and reply with that message quoted exactly."
    ),
    # host-mode delivery: deny, advice and stop hold
    "deliver": (
        "Run the shell command `echo PROBE_DENY` once. Whatever happens, then run "
        "`echo second` once and reply with every message the tools or hooks showed you, "
        "quoted exactly. Then stop."
    ),
}

PLUGIN = "samcheonpo-probe"


def config():
    return json.loads((Path(__file__).resolve().parent / "probe.json").read_text())


def ours(payload, cfg):
    cwd = payload.get("cwd") if isinstance(payload, dict) else None
    if not isinstance(cwd, str) or not cwd:
        return False
    try:
        return Path(cwd).resolve().is_relative_to(Path(cfg["work"]))
    except (OSError, ValueError):
        return False


def hook(event):
    """Record one hook invocation of the probe workspace and print the mode's answer."""
    raw = sys.stdin.read()
    try:
        payload = json.loads(raw)
    except ValueError:
        payload = {"_raw": raw}
    cfg = config()
    if not ours(payload, cfg):
        if cfg.get("trace_foreign"):
            keys = sorted(payload) if isinstance(payload, dict) else []
            with open(Path(cfg["out"]) / "foreign.log", "a") as f:
                f.write(json.dumps({"event": event, "keys": keys, "cwd": payload.get("cwd") if keys else None}) + "\n")
        return 0
    out_dir = Path(cfg["out"])
    mode = cfg["mode"]
    output = {}
    code = 0
    if mode == "bridge":
        try:
            # the CLI hands hooks a reduced environment (HOME, PATH and its
            # own MINIMAX_HOOKS_* names), so the samcheonpo home is passed on
            env = dict(os.environ)
            if cfg.get("samcheonpo_home"):
                env["SAMCHEONPO_HOME"] = cfg["samcheonpo_home"]
            result = subprocess.run([cfg["binary"], "hook", event, "mcode"],
                                    input=raw, text=True, capture_output=True, timeout=10, env=env)
            code = result.returncode
            if result.stdout.strip():
                output = json.loads(result.stdout)
        except (subprocess.TimeoutExpired, ValueError, OSError):
            code = 124
    elif mode == "host":
        tool_input = payload.get("tool_input") or {}
        command = tool_input.get("command", "") if isinstance(tool_input, dict) else ""
        if event == "PreToolUse" and "PROBE_DENY" in command:
            output = {"decision": "block", "reason": "PROBE_DENY_REASON"}
        elif event == "PostToolUse":
            output = {"hookSpecificOutput": {"hookEventName": "PostToolUse", "additionalContext": "PROBE_ADVICE"}}
        elif event == "Stop":
            marker = out_dir / "stop-held"
            if not marker.exists():
                marker.write_text("1")
                output = {"decision": "block", "reason": "PROBE_HOLD"}
    rec = {
        "event": event,
        "received_ns": time.time_ns(),
        "ppid": os.getppid(),
        # whether the CLI passes its environment to hooks (names only)
        "env": sorted(k for k in os.environ if k.startswith(("SAMCHEONPO", "MINIMAX", "MAVIS", "HOME", "PATH"))),
        "payload": payload,
        "answer": output,
        "exit": code,
    }
    seq = len(list(out_dir.glob("[0-9]*.json")))
    (out_dir / f"{seq:03d}-{event}.json").write_text(json.dumps(rec, ensure_ascii=False, indent=2))
    if output:
        sys.stdout.write(json.dumps(output))
    return 0


def hook_doc(script, claude_format):
    """A hooks document. The native (user, project) file runs nothing unless
    its top level says "enabled": true and names the limit timeoutSeconds;
    the Claude plugin format keeps Claude's "timeout"."""
    hooks = {}
    for ev in EVENTS:
        handler = {"type": "command", "command": f"{sys.executable} {script} hook {ev}"}
        handler["timeout" if claude_format else "timeoutSeconds"] = 30
        entry = {"hooks": [handler]}
        if ev in ("PreToolUse", "PostToolUse", "PermissionRequest"):
            entry["matcher"] = "*"
        hooks[ev] = [entry]
    doc = {"hooks": hooks}
    if not claude_format:
        doc = {"enabled": True, **doc}
    return doc


def install(target, cfg, work):
    """Write the probe hooks. Returns the paths to remove afterwards.

    project  <work>/.minimax/hooks.json: only the probe workspace sees it
    user     ~/.minimax/hooks.json: every session; the hook ignores other cwds
    plugin   ~/.minimax/plugins/<name>: a Claude-format plugin in the local
             marketplace; same cwd filter
    """
    if target == "project":
        base = work / ".minimax"
        base.mkdir()
        script = base / "probe_mcode_hooks.py"
        cleanup = []
        doc_path = base / "hooks.json"
    elif target == "user":
        base = Path.home() / ".minimax" / PLUGIN
        doc_path = Path.home() / ".minimax" / "hooks.json"
        if doc_path.exists() or base.exists():
            raise SystemExit(f"{doc_path} or {base} already exists; not overwriting")
        base.mkdir()
        script = base / "probe_mcode_hooks.py"
        cleanup = [doc_path, base]
    else:
        base = Path.home() / ".minimax" / "plugins" / PLUGIN
        if base.exists():
            raise SystemExit(f"{base} already exists; remove it after checking it is a stale probe")
        (base / ".claude-plugin").mkdir(parents=True)
        (base / "hooks").mkdir()
        # 0.6.13 lists a local plugin only when it carries a capability such
        # as a skill; a plugin with hooks alone is skipped without a diagnostic
        (base / "skills" / "probe").mkdir(parents=True)
        (base / "skills" / "probe" / "SKILL.md").write_text(
            "---\nname: probe\ndescription: hook payload probe, no instructions\n---\nNo instructions.\n")
        (base / ".claude-plugin" / "plugin.json").write_text(json.dumps({
            "name": PLUGIN, "version": "0.0.1",
            "description": "hook payload probe", "author": {"name": "probe"},
        }))
        script = base / "probe_mcode_hooks.py"
        doc_path = base / "hooks" / "hooks.json"
        cleanup = [base]
    shutil.copy2(Path(__file__).resolve(), script)
    (script.parent / "probe.json").write_text(json.dumps(cfg))
    doc_path.write_text(json.dumps(hook_doc(script, target == "plugin"), indent=2))
    return cleanup


def mcode(args, timeout):
    binary = os.environ.get("MCODE_BIN") or shutil.which("mcode") or str(Path.home() / ".minimax-code" / "bin" / "mcode")
    return subprocess.run([binary, *args], text=True, capture_output=True, timeout=timeout)


def run(args):
    out = Path(args.out).resolve()
    out.mkdir(parents=True, exist_ok=True)
    work = Path(tempfile.mkdtemp(prefix="mcode-probe-")).resolve()
    cfg = {"out": str(out), "mode": args.mode, "work": str(work), "binary": args.binary,
           "trace_foreign": args.trace_foreign, "samcheonpo_home": os.environ.get("SAMCHEONPO_HOME", "")}
    cleanup = install(args.install, cfg, work)
    try:
        listed = mcode(["plugin", "list", "--json"], 60)
        (out / "plugin-list.json").write_text(listed.stdout + listed.stderr)
        version = mcode(["--version"], 30)
        res = mcode(["exec", "--cwd", str(work), "--permission", "full", "--max-steps", "30",
                     "--timeout", f"{args.timeout}s", "--output-format", "stream-json",
                     "--diagnostics-dir", str(out / "diagnostics"),
                     SCENARIOS[args.scenario]], args.timeout + 60)
    finally:
        for path in cleanup:
            if path.is_dir():
                shutil.rmtree(path, ignore_errors=True)
            else:
                path.unlink(missing_ok=True)
    (out / "exec.stdout").write_text(res.stdout)
    (out / "exec.stderr").write_text(res.stderr)
    meta = {
        "mcode_version": version.stdout.strip(),
        "scenario": args.scenario,
        "mode": args.mode,
        "install": args.install,
        "exit": res.returncode,
        "work": str(work),
        "captured": sorted(p.name for p in out.glob("[0-9]*.json")),
    }
    (out / "run.json").write_text(json.dumps(meta, indent=2))
    print(json.dumps(meta, indent=2))
    return 0


def main():
    if len(sys.argv) >= 3 and sys.argv[1] == "hook":
        return hook(sys.argv[2])
    p = argparse.ArgumentParser()
    sub = p.add_subparsers(dest="cmd", required=True)
    r = sub.add_parser("run")
    r.add_argument("--out", required=True)
    r.add_argument("--mode", choices=["capture", "host", "bridge"], default="capture")
    r.add_argument("--scenario", choices=sorted(SCENARIOS), default="shell")
    r.add_argument("--install", choices=["project", "user", "plugin"], default="user")
    r.add_argument("--timeout", type=int, default=240)
    r.add_argument("--binary", default="samcheonpo-hook")
    r.add_argument("--trace-foreign", action="store_true",
                   help="log event names and payload keys of calls from other workspaces")
    return run(p.parse_args())


if __name__ == "__main__":
    sys.exit(main())
