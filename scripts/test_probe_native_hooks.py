import json
from pathlib import Path
import tempfile
import unittest

from probe_native_hooks import export_capture, probe_passed


class NativeProbeTests(unittest.TestCase):
    def test_interruption_needs_verified_resume_in_same_session(self):
        summary = dict(scenario="interrupt", timed_out=False, interrupt_sent=True,
                       resume_exit_code=0, resume_timed_out=False,
                       resumed_effect_present=True, resume_same_session=True)
        self.assertTrue(probe_passed(summary))
        for key, value in [("resume_same_session", False), ("resume_timed_out", True),
                           ("resumed_effect_present", False), ("resume_exit_code", 1)]:
            with self.subTest(key=key):
                self.assertFalse(probe_passed(dict(summary, **{key: value})))

    def test_successful_exit_without_final_background_output_is_failure(self):
        summary = dict(scenario="background", exit_code=0, timed_out=False,
                       late_result=None, stop_followup_effect_present=True)
        self.assertFalse(probe_passed(summary))
        self.assertTrue(probe_passed(dict(summary, late_result="late-done")))

    def test_child_file_without_native_lineage_is_not_full_support(self):
        summary = dict(scenario="subagent", exit_code=0, timed_out=False,
                       child_effect_present=True, child_stop_followup_present=True,
                       child_lineage_observed=False, denial_observed=True, denied_effect_absent=True)
        self.assertFalse(probe_passed(summary))
        self.assertTrue(probe_passed(dict(summary, child_lineage_observed=True)))

    def test_failed_export_preserves_unverified_and_excludes_private_mcp(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            raw, public = root / "raw", root / "public"
            raw.mkdir(); public.mkdir()
            summary = dict(agent="agy", scenario="background", exit_code=0,
                           timed_out=False, late_result=None,
                           stop_followup_effect_present=False, session_ids=["private-session"])
            (raw / "summary.json").write_text(json.dumps(summary))
            (raw / "probe.json").write_text(json.dumps(dict(work=str(raw / "work"))))
            payload = dict(conversationId="private-session", workspacePaths=[str(raw / "work")],
                           transcriptPath="/home/private/transcript.jsonl")
            (raw / "100.json").write_text(json.dumps(dict(event="SessionStart", payload=payload)))
            (raw / "101.json").write_text(json.dumps(dict(event="PostToolUse", payload=dict(
                tool_name="mcp__private", tool_response="private-connected-source-content"))))
            (public / "manifest.json").write_text(json.dumps(dict(captures=[], unverified=["background_result"])))
            export_capture("agy", raw, public)
            manifest = json.loads((public / "manifest.json").read_text())
            self.assertFalse(manifest["background_probe"]["passed"])
            self.assertIn("background_result", manifest["unverified"])
            self.assertEqual(len(manifest["captures"]), 1)
            published = "".join(p.read_text() for p in public.glob("*.json"))
            for secret in ["private-session", "private-connected-source-content", "/home/private", str(raw)]:
                self.assertNotIn(secret, published)


if __name__ == "__main__":
    unittest.main()
