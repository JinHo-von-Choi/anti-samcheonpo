"""Real LangGraph/Deep Agents execution with a deterministic local model.

No API calls or human labels. The model is scripted; SDK middleware routing,
tool execution, side effects and returned messages are real.
"""
import asyncio
import importlib.util
import json
import os
import subprocess
import socket
import tempfile
import time
from pathlib import Path

import pytest
from deepagents import create_deep_agent
from deepagents.backends import FilesystemBackend
from langchain_core.language_models.chat_models import BaseChatModel
from langchain_core.messages import AIMessage, ToolMessage
from langchain_core.outputs import ChatGeneration, ChatResult

spec = importlib.util.spec_from_file_location("samcheonpo_middleware", Path(__file__).with_name("middleware.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class ScriptedModel(BaseChatModel):
    calls: int = 0
    tool_name: str = "probe_write"
    tool_args: dict = {"value": "actual-tool-result"}

    @property
    def _llm_type(self):
        return "sdk-conformance-scripted"

    def bind_tools(self, tools, **kwargs):
        return self

    def _generate(self, messages, stop=None, run_manager=None, **kwargs):
        self.calls += 1
        if self.calls == 1:
            reply = AIMessage(content="", tool_calls=[{"name": self.tool_name, "args": self.tool_args, "id": "probe-call"}])
        else:
            reply = AIMessage(content="finished")
        return ChatResult(generations=[ChatGeneration(message=reply)])


@pytest.mark.parametrize("asynchronous", [False, True])
@pytest.mark.parametrize("deny", [False, True])
def test_real_graph_tool_control(tmp_path, asynchronous, deny):
    destination = tmp_path / "written.txt"
    events = []

    def probe_write(value: str) -> str:
        """Write the probe result inside the temporary test directory."""
        destination.write_text(value)
        return value

    def transport(event, payload):
        events.append((event, payload))
        if event == "PreToolUse" and deny:
            return {"hookSpecificOutput": {"permissionDecision": "deny", "permissionDecisionReason": "probe-denied"}}
        if event == "PostToolUse":
            return {"hookSpecificOutput": {"additionalContext": "probe-advice"}}
        return {}

    middleware = module.create_middleware("sdk-probe", str(tmp_path), transport)
    agent = create_deep_agent(model=ScriptedModel(), tools=[probe_write], middleware=[middleware])
    request = {"messages": [{"role": "user", "content": "Run probe_write once."}]}
    result = asyncio.run(agent.ainvoke(request)) if asynchronous else agent.invoke(request)
    tool_messages = [m for m in result["messages"] if isinstance(m, ToolMessage)]
    assert events[0][0] == "SessionStart"
    assert events[1][0] == "UserPromptSubmit"
    assert events[-1][0] == "Stop"
    assert not any(e == "SessionEnd" for e, _ in events)
    asyncio.run(middleware.aclose()) if asynchronous else middleware.close()
    assert events[-1][0] == "SessionEnd"
    assert len(tool_messages) == 1
    assert any(e == "PreToolUse" and p["tool_use_id"] == "probe-call" for e, p in events)
    if deny:
        assert not destination.exists()
        assert tool_messages[0].status == "error"
        assert "probe-denied" in tool_messages[0].content
        assert not any(e == "PostToolUse" for e, _ in events)
    else:
        assert destination.read_text() == "actual-tool-result"
        assert "actual-tool-result" in tool_messages[0].content
        assert "probe-advice" in tool_messages[0].content
        assert any(e == "PostToolUse" for e, _ in events)


@pytest.mark.parametrize("asynchronous", [False, True])
def test_stop_hold_is_not_success(tmp_path, asynchronous):
    def probe_write(value: str) -> str:
        """Return the test result."""
        return value

    def transport(event, payload):
        return {"decision": "block", "reason": "missing required evidence"} if event == "Stop" else {}

    middleware = module.create_middleware("hold-probe", str(tmp_path), transport)
    agent = create_deep_agent(model=ScriptedModel(), tools=[probe_write], middleware=[middleware])
    request = {"messages": [{"role": "user", "content": "Run the probe."}]}
    with pytest.raises(module.CompletionHeld, match="missing required evidence"):
        asyncio.run(agent.ainvoke(request)) if asynchronous else agent.invoke(request)
    middleware.close()


@pytest.mark.parametrize("asynchronous", [False, True])
def test_tool_exception_is_observed_and_preserved(tmp_path, asynchronous):
    events = []

    def probe_write(value: str) -> str:
        """Raise a real tool exception."""
        raise RuntimeError("probe tool failure")

    def transport(event, payload):
        events.append(event)
        return {}

    middleware = module.create_middleware("error-probe", str(tmp_path), transport)
    agent = create_deep_agent(model=ScriptedModel(), tools=[probe_write], middleware=[middleware])
    request = {"messages": [{"role": "user", "content": "Run the probe."}]}
    with pytest.raises(RuntimeError, match="probe tool failure"):
        asyncio.run(agent.ainvoke(request)) if asynchronous else agent.invoke(request)
    assert "PostToolUseFailure" in events
    assert "Stop" not in events
    middleware.close()


@pytest.mark.parametrize("asynchronous", [False, True])
def test_binary_daemon_sdk_denial(tmp_path, monkeypatch, asynchronous):
    binary = os.environ.get("SAMCHEONPO_TEST_BIN")
    if not binary:
        pytest.skip("set SAMCHEONPO_TEST_BIN to test the real daemon bridge")
    protected = tmp_path / "protected.txt"
    with tempfile.TemporaryDirectory(prefix="sc-sdk-") as daemon_dir:
        daemon_home = Path(daemon_dir)
        (daemon_home / "config.yml").write_text(
            "notify: {desktop: false}\niron_laws: {enabled: false}\nprotected_paths: [" + str(protected) + "]\n")
        monkeypatch.setenv("SAMCHEONPO_HOME", str(daemon_home))
        monkeypatch.setenv("SAMCHEONPO_BIN", binary)
        with (daemon_home / "daemon.log").open("w") as log:
            daemon = subprocess.Popen([binary, "daemon", "--idle", "30s"], stdout=log, stderr=log)
            middleware = module.create_middleware("sdk-real-bridge", str(tmp_path))
            try:
                deadline = time.monotonic() + 5
                while not (daemon_home / "run/samcheonpo.sock").exists():
                    assert daemon.poll() is None, "daemon exited before readiness"
                    assert time.monotonic() < deadline, "daemon readiness timed out"
                    time.sleep(.02)
                module.hook("SessionStart", {"session_id": "sdk-real-bridge", "cwd": str(tmp_path)})
                # Version probing happens off the hook path; a fixed sleep
                # races slow filesystem/startup work on CI hosts.
                while True:
                    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as client:
                        client.settimeout(2)
                        client.connect(str(daemon_home / "run/samcheonpo.sock"))
                        client.sendall((json.dumps({"v":1,"event":"AgentStatus","payload":{"agent":"deepagents"}})+"\n").encode())
                        response = json.loads(client.makefile("r").readline())
                    status = json.loads(response.get("text") or "{}")
                    if status.get("ready") and status.get("caps", {}).get("block_pre"):
                        break
                    assert time.monotonic() < deadline, f"adapter readiness timed out: {status}"
                    time.sleep(.02)
                model = ScriptedModel(tool_name="write_file", tool_args={"file_path": str(protected), "content": "must not be written"})
                agent = create_deep_agent(model=model, backend=FilesystemBackend(root_dir=tmp_path, virtual_mode=False), middleware=[middleware])
                request = {"messages": [{"role": "user", "content": "Write the test file once."}]}
                result = asyncio.run(agent.ainvoke(request)) if asynchronous else agent.invoke(request)
                messages = [m for m in result["messages"] if isinstance(m, ToolMessage)]
                assert not protected.exists(), "daemon denial did not prevent real SDK filesystem write"
                assert len(messages) == 1 and messages[0].status == "error"
                trace = subprocess.run([binary, "interventions", "--session", "sdk-real-bridge", "--json"], capture_output=True, text=True, check=True)
                assert 'pre_deny' in trace.stdout
            finally:
                middleware.close()
                daemon.terminate()
                try:
                    daemon.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    daemon.kill()
                    daemon.wait()

class RecoveryModel(ScriptedModel):
    def _generate(self, messages, stop=None, run_manager=None, **kwargs):
        self.calls += 1
        if self.calls == 2:
            reply = AIMessage(content="", tool_calls=[{"name": self.tool_name, "args": self.tool_args, "id": "recovery-write"}])
        else:
            reply = AIMessage(content="finished")
        reply.usage_metadata = {"input_tokens": 100, "output_tokens": 7, "total_tokens": 107,
                                "input_token_details": {"cache_read": 20, "cache_creation": 10}}
        reply.response_metadata = {"model_name": "sdk-observed-model"}
        return ChatResult(generations=[ChatGeneration(message=reply)])


@pytest.mark.parametrize("asynchronous", [False, True])
def test_stop_hold_reenters_model_and_records_actual_usage(tmp_path, asynchronous):
    destination = tmp_path / "recovered.txt"
    events = []

    def probe_write(value: str) -> str:
        """Write the required evidence."""
        destination.write_text(value)
        return value

    def transport(event, payload):
        events.append((event, payload))
        if event == "Stop" and not destination.exists():
            return {"decision": "block", "reason": "Run probe_write to produce required evidence"}
        return {}

    middleware = module.create_middleware("recover", str(tmp_path), transport)
    model = RecoveryModel()
    agent = create_deep_agent(model=model, tools=[probe_write], middleware=[middleware])
    request = {"messages": [{"role": "user", "content": "Produce required evidence"}]}
    result = asyncio.run(agent.ainvoke(request)) if asynchronous else agent.invoke(request)
    assert destination.read_text() == "actual-tool-result"
    assert model.calls == 3
    assert len([p for e, p in events if e == "Stop"]) == 2
    usage = [p for e, p in events if e == "Usage"]
    assert len(usage) == 3 and len({p["message_id"] for p in usage}) == 3
    assert all(p["usage"] == {"input_tokens": 70, "output_tokens": 7,
                              "cache_read_input_tokens": 20, "cache_creation_input_tokens": 10} for p in usage)
    assert all(p["model"] == "sdk-observed-model" for p in usage)
    assert result["messages"][-1].text == "finished"
    middleware.close()


class ParentModel(ScriptedModel):
    def _generate(self, messages, stop=None, run_manager=None, **kwargs):
        self.calls += 1
        calls = ([{"name": "task", "args": {"subagent_type": "worker", "description": value}, "id": "task-"+value}
                  for value in ["allowed", "protected"]] if self.calls == 1 else [])
        reply = AIMessage(content="" if calls else "parent finished", tool_calls=calls,
                          usage_metadata={"input_tokens": 11, "output_tokens": 3, "total_tokens": 14})
        return ChatResult(generations=[ChatGeneration(message=reply)])


class ChildModel(ScriptedModel):
    root: str

    def _generate(self, messages, stop=None, run_manager=None, **kwargs):
        # State, not a shared mutable call counter: worker graph runs in parallel.
        has_result = any(isinstance(m, ToolMessage) for m in messages)
        calls = []
        if not has_result:
            target = "protected.txt" if "protected" in messages[-1].text else "allowed.txt"
            calls = [{"name": "write_file", "args": {"file_path": str(Path(self.root)/target), "content": target}, "id": "child-write"}]
        reply = AIMessage(content="" if calls else "child finished", tool_calls=calls,
                          usage_metadata={"input_tokens": 13, "output_tokens": 5, "total_tokens": 18})
        return ChatResult(generations=[ChatGeneration(message=reply)])


@pytest.mark.parametrize("asynchronous", [False, True])
def test_parallel_child_sessions_are_instrumented_and_isolated(tmp_path, asynchronous):
    events = []

    def transport(event, payload):
        events.append((event, payload))
        if event == "PreToolUse" and payload["tool_name"] == "Write" and payload["tool_input"]["file_path"].endswith("protected.txt"):
            return {"hookSpecificOutput": {"permissionDecision": "deny", "permissionDecisionReason": "child-protected"}}
        return {}

    agent = module.create_monitored_agent(session_id="parent", root=str(tmp_path), transport=transport,
        model=ParentModel(), backend=FilesystemBackend(root_dir=tmp_path, virtual_mode=False),
        subagents=[{"name": "worker", "description": "Conformance worker", "system_prompt": "Execute requested write once.",
                    "model": ChildModel(root=str(tmp_path))}])
    request = {"messages": [{"role": "user", "content": "Delegate the two conformance writes"}]}
    asyncio.run(agent.ainvoke(request)) if asynchronous else agent.invoke(request)
    assert (tmp_path / "allowed.txt").read_text() == "allowed.txt"
    assert not (tmp_path / "protected.txt").exists()
    children = [p for e, p in events if e == "SessionStart" and p.get("parent_session_id") == "parent"]
    assert len(children) == 2 and len({p["session_id"] for p in children}) == 2
    child_ids = {p["session_id"] for p in children}
    writes = [p for e, p in events if e == "PreToolUse" and p["tool_name"] == "Write"]
    assert len(writes) == 2 and {p["session_id"] for p in writes} == child_ids
    assert {p["session_id"] for e, p in events if e == "SessionEnd"} == child_ids
    usage = [p for e, p in events if e == "Usage"]
    assert len(usage) == 6 and {p["session_id"] for p in usage} == child_ids | {"parent"}
    assert len({p["message_id"] for p in usage}) == 6
    asyncio.run(agent.aclose()) if asynchronous else agent.close()
    assert events[-1][0] == "SessionEnd" and events[-1][1]["session_id"] == "parent"

from contextlib import contextmanager
import sqlite3


@contextmanager
def running_real_monitor(tmp_path, monkeypatch, extra_config=""):
    binary = os.environ.get("SAMCHEONPO_TEST_BIN")
    if not binary:
        pytest.skip("set SAMCHEONPO_TEST_BIN for daemon conformance")
    with tempfile.TemporaryDirectory(prefix="sc-deep-expanded-") as folder:
        home = Path(folder)
        (home / "config.yml").write_text("notify: {desktop: false}\niron_laws: {enabled: false}\n"+extra_config)
        monkeypatch.setenv("SAMCHEONPO_HOME", str(home))
        monkeypatch.setenv("SAMCHEONPO_BIN", binary)
        with (home / "daemon.log").open("w") as log:
            daemon = subprocess.Popen([binary, "daemon", "--idle", "30s"], stdout=log, stderr=log)
            try:
                deadline = time.monotonic()+10
                while True:
                    assert daemon.poll() is None, "monitor exited"
                    assert time.monotonic()<deadline, "monitor readiness timeout"
                    try:
                        with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as client:
                            client.settimeout(1);client.connect(str(home / "run/samcheonpo.sock"))
                            client.sendall((json.dumps({"v":1,"event":"AgentStatus","payload":{"agent":"deepagents"}})+"\n").encode())
                            status=json.loads(json.loads(client.makefile("r").readline()).get("text") or "{}")
                        if status.get("ready") and status.get("caps",{}).get("block_stop"):
                            break
                    except (OSError, ValueError):
                        pass
                    time.sleep(.02)
                yield binary,home
            finally:
                daemon.terminate()
                try:daemon.wait(timeout=5)
                except subprocess.TimeoutExpired:daemon.kill();daemon.wait()


@pytest.mark.parametrize("asynchronous", [False, True])
def test_real_daemon_stop_recovery_and_usage_persistence(tmp_path, monkeypatch, asynchronous):
    evidence = tmp_path / "recovered.txt"
    contract = tmp_path / ".samcheonpo/contract.yml"
    contract.parent.mkdir()
    contract.write_text("spec: progress-contract/2\ngoal: Produce required evidence\ndone:\n - id: recovered\n   check: test -s recovered.txt\n   pure: true\n   evidence: {kind: data, artifacts: [recovered.txt]}\n")
    observed = []
    with running_real_monitor(tmp_path, monkeypatch, "rollout: {mode: validated, validated_rules: {s5.stop_unmet: " + __import__("hashlib").sha256(contract.read_bytes()).hexdigest() + "}}\n") as (binary, home):
        def transport(event, payload):
            result = module.hook(event,payload)
            observed.append((event,result))
            if event == "UserPromptSubmit":
                accepted=subprocess.run([binary,"cmd","accept"],cwd=tmp_path,env=dict(os.environ,CLAUDE_SESSION_ID=payload["session_id"]),capture_output=True,text=True)
                assert accepted.returncode==0,accepted.stderr
            return result

        def probe_write(value: str) -> str:
            """Produce acceptance evidence."""
            evidence.write_text(value)
            return value

        middleware=module.create_middleware("real-recovery",str(tmp_path),transport)
        agent=create_deep_agent(model=RecoveryModel(),tools=[probe_write],middleware=[middleware])
        request={"messages":[{"role":"user","content":"Produce required evidence"}]}
        asyncio.run(agent.ainvoke(request)) if asynchronous else agent.invoke(request)
        middleware.close()
        stops=[r for e,r in observed if e=="Stop"]
        assert len(stops)==2 and stops[0].get("decision")=="block" and stops[1].get("decision")!="block"
        assert evidence.read_text()=="actual-tool-result"
        with sqlite3.connect(home/"ledger.db") as db:
            totals=db.execute("select sum(tokens_in),sum(tokens_out),sum(tokens_cache_read),sum(tokens_cache_write) from event where session_id='real-recovery'").fetchone()
        assert totals==(210,21,60,30),totals


@pytest.mark.parametrize("asynchronous", [False, True])
def test_real_daemon_parallel_children_keep_lineage_and_prevent_write(tmp_path, monkeypatch, asynchronous):
    protected=tmp_path/"protected.txt"
    with running_real_monitor(tmp_path,monkeypatch,"rollout: {mode: shadow}\nprotected_paths: ["+str(protected)+"]\n") as (binary,home):
        agent=module.create_monitored_agent(session_id="real-parent",root=str(tmp_path),model=ParentModel(),
            backend=FilesystemBackend(root_dir=tmp_path,virtual_mode=False),
            subagents=[{"name":"worker","description":"Conformance worker","system_prompt":"Write once.","model":ChildModel(root=str(tmp_path))}])
        request={"messages":[{"role":"user","content":"Delegate the writes"}]}
        asyncio.run(agent.ainvoke(request)) if asynchronous else agent.invoke(request)
        agent.close()
        assert not protected.exists()
        assert (tmp_path/"allowed.txt").read_text()=="allowed.txt"
        with sqlite3.connect(home/"ledger.db") as db:
            links=db.execute("select session_id,parent_session_id from session_lineage where agent='deepagents'").fetchall()
            assert len(links)==2 and {p for _,p in links}=={"real-parent"}
            child_ids={c for c,_ in links}
            observed_sessions={s for (s,) in db.execute("select distinct session_id from event")}
            assert child_ids|{"real-parent"} <= observed_sessions
            tokens=db.execute("select sum(tokens_in),sum(tokens_out) from event").fetchone()
            assert tokens==(74,26),tokens
        native=subprocess.run([binary,"lineage","--agent","deepagents","--session","real-parent","--format","json"],capture_output=True,text=True,check=True)
        assert {link["session_id"] for link in json.loads(native.stdout)["native_lineage"]}==child_ids
        for child in child_ids:
            trace=subprocess.run([binary,"interventions","--session",child,"--json"],capture_output=True,text=True,check=True)
            if 'pre_deny' in trace.stdout:break
        else:pytest.fail("child denial was not persisted")

@pytest.mark.parametrize("asynchronous", [False, True])
def test_unavailable_monitor_never_executes_sdk_tool(tmp_path, monkeypatch, asynchronous):
    binary=os.environ.get("SAMCHEONPO_TEST_BIN")
    if not binary:pytest.skip("set SAMCHEONPO_TEST_BIN")
    monkeypatch.setenv("SAMCHEONPO_BIN",binary)
    monkeypatch.setenv("SAMCHEONPO_HOME",str(tmp_path/"missing-monitor"))
    monkeypatch.setenv("SAMCHEONPO_NO_SPAWN","1")
    destination=tmp_path/"must-not-exist"
    def probe_write(value:str)->str:
        """Write only if the monitor actually approved."""
        destination.write_text(value);return value
    middleware=module.create_middleware("unavailable",str(tmp_path))
    agent=create_deep_agent(model=ScriptedModel(),tools=[probe_write],middleware=[middleware])
    request={"messages":[{"role":"user","content":"Run the tool"}]}
    with pytest.raises(module.MonitorUnavailable):
        asyncio.run(agent.ainvoke(request)) if asynchronous else agent.invoke(request)
    assert not destination.exists()


def test_compiled_or_remote_child_is_not_silently_claimed_protected(tmp_path):
    for child in [dict(name="precompiled",description="x",runnable=object()),dict(name="remote",description="x",graph_id="remote")]:
        with pytest.raises(ValueError,match="explicit instrumentation"):
            module.create_monitored_agent(session_id="parent",root=str(tmp_path),model=ScriptedModel(),subagents=[child])

class GeneralPurposeModel(ScriptedModel):
    root: str

    def _generate(self, messages, stop=None, run_manager=None, **kwargs):
        from langchain_core.messages import HumanMessage
        human=next(m.text for m in reversed(messages) if isinstance(m,HumanMessage))
        has_result=any(isinstance(m,ToolMessage) for m in messages)
        calls=[]
        if not has_result:
            if human=="allowed":
                calls=[{"name":"write_file","args":{"file_path":str(Path(self.root)/"default-child.txt"),"content":"default-child"},"id":"default-write"}]
            else:
                calls=[{"name":"task","args":{"description":"allowed","subagent_type":"general-purpose"},"id":"default-task"}]
        return ChatResult(generations=[ChatGeneration(message=AIMessage(content="finished" if not calls else "",tool_calls=calls))])


@pytest.mark.parametrize("asynchronous",[False,True])
def test_default_general_purpose_child_is_also_instrumented(tmp_path,asynchronous):
    observed=[]
    def transport(event,payload):observed.append((event,payload));return {}
    agent=module.create_monitored_agent(session_id="default-parent",root=str(tmp_path),transport=transport,
        model=GeneralPurposeModel(root=str(tmp_path)),backend=FilesystemBackend(root_dir=tmp_path,virtual_mode=False))
    request={"messages":[{"role":"user","content":"Delegate one default child"}]}
    asyncio.run(agent.ainvoke(request)) if asynchronous else agent.invoke(request)
    assert (tmp_path/"default-child.txt").read_text()=="default-child"
    child=next(p["session_id"] for e,p in observed if e=="SessionStart" and p.get("parent_session_id")=="default-parent")
    assert any(e=="PreToolUse" and p["session_id"]==child and p["tool_name"]=="Write" for e,p in observed)
    agent.close()


class BudgetParentModel(ParentModel):
    def _generate(self,messages,stop=None,run_manager=None,**kwargs):
        result=super()._generate(messages,stop,run_manager,**kwargs)
        result.generations[0].message.usage_metadata={"input_tokens":1000,"output_tokens":2000,"total_tokens":3000}
        result.generations[0].message.response_metadata={"model_name":"claude-sonnet-5-5"}
        return result


def test_child_cannot_bypass_parent_already_spent_budget(tmp_path,monkeypatch):
    with running_real_monitor(tmp_path,monkeypatch,"swarm: {tree_krw: 1}\n") as (binary,home):
        agent=module.create_monitored_agent(session_id="budget-parent",root=str(tmp_path),model=BudgetParentModel(),
            backend=FilesystemBackend(root_dir=tmp_path,virtual_mode=False),
            subagents=[{"name":"worker","description":"Conformance worker","system_prompt":"Write once.","model":ChildModel(root=str(tmp_path))}])
        agent.invoke({"messages":[{"role":"user","content":"Delegate the writes"}]})
        agent.close()
        assert not (tmp_path/"allowed.txt").exists() and not (tmp_path/"protected.txt").exists()
        with sqlite3.connect(home/"ledger.db") as db:
            assert db.execute("select count(*) from session_lineage where parent_session_id='budget-parent'").fetchone()[0]==2
            assert db.execute("select count(*) from intervention i join verdict v on i.verdict_id=v.id where i.blocked=1 and v.rule='s8.budget'").fetchone()[0]>=1, db.execute("select v.rule,i.blocked,v.facts from intervention i join verdict v on i.verdict_id=v.id").fetchall()

@pytest.mark.parametrize("asynchronous",[False,True])
def test_reporting_failure_does_not_replace_original_tool_exception(tmp_path,asynchronous):
    def probe_write(value:str)->str:
        """Raise the original diagnostic error."""
        raise RuntimeError("original tool error")
    def transport(event,payload):
        if event=="PostToolUseFailure":raise module.MonitorUnavailable("reporting unavailable")
        return {}
    middleware=module.create_middleware("error-preserved",str(tmp_path),transport)
    agent=create_deep_agent(model=ScriptedModel(),tools=[probe_write],middleware=[middleware])
    request={"messages":[{"role":"user","content":"Run the tool"}]}
    with pytest.raises(RuntimeError,match="original tool error") as raised:
        asyncio.run(agent.ainvoke(request)) if asynchronous else agent.invoke(request)
    assert any("MonitorUnavailable" in note for note in raised.value.__notes__)
    middleware.close()


@pytest.mark.parametrize("asynchronous", [False, True])
def test_unverified_completion_notice_is_returned_and_cleared(tmp_path, asynchronous):
    stops = []

    def probe_write(value: str) -> str:
        """Return the probe value."""
        return value

    def transport(event, payload):
        if event == "Stop":
            stops.append(payload)
            return {"completion_notice": "unverified end"} if len(stops) == 1 else {}
        return {}

    middleware = module.create_middleware("completion-notice", str(tmp_path), transport)
    agent = create_deep_agent(model=ScriptedModel(), tools=[probe_write], middleware=[middleware])
    request = {"messages": [{"role": "user", "content": "Run the probe."}]}
    first = asyncio.run(agent.ainvoke(request)) if asynchronous else agent.invoke(request)
    assert first["samcheonpo_completion_notice"] == "unverified end"
    second = asyncio.run(agent.ainvoke(request)) if asynchronous else agent.invoke(request)
    assert second["samcheonpo_completion_notice"] == ""
    middleware.close()


@pytest.mark.parametrize("asynchronous", [False, True])
def test_real_daemon_bounded_stop_returns_unverified_notice(tmp_path, monkeypatch, asynchronous):
    with running_real_monitor(tmp_path, monkeypatch):
        agent = module.create_monitored_agent(
            session_id="bounded-notice", root=str(tmp_path),
            model=ScriptedModel(tool_name="write_file", tool_args={"file_path": "/calc.py", "content": "value = 1\n"}),
            backend=FilesystemBackend(root_dir=str(tmp_path), virtual_mode=True),
        )
        try:
            request = {"messages": [{"role": "user", "content": "Implement the calculation."}]}
            result = asyncio.run(agent.ainvoke(request)) if asynchronous else agent.invoke(request)
            assert "미검증" in result["samcheonpo_completion_notice"]
            assert (tmp_path / "calc.py").read_text() == "value = 1\n"
        finally:
            agent.close()
