import tempfile
import unittest
import json
from pathlib import Path

from codex_pilot import observed_controls, restore_trial_registrations


class ObservedControlsTest(unittest.TestCase):
    def test_runtime_drift_is_not_hidden_by_declared_manifest(self):
        settings = {"model": "existing-model", "model_reasoning_effort": "high", "approval_policy": "never", "sandbox_mode": "workspace-write"}
        with tempfile.TemporaryDirectory() as directory:
            home = Path(directory)
            (home / "sessions").mkdir()
            results = home / "trials.jsonl"
            results.write_text(json.dumps({"session_id": "trial-one"}) + "\n")
            source = home / "sessions/rollout-trial-one.jsonl"
            record = {"type": "turn_context", "payload": {"model": "existing-model", "effort": "high", "approval_policy": "never", "sandbox_policy": {"type": "workspace-write"}}}
            source.write_text(json.dumps(record) + "\n")
            self.assertTrue(observed_controls(home, results, settings)[0]["verified"])
            record["payload"]["effort"] = "low"
            with source.open("a") as file:
                file.write(json.dumps(record) + "\n")
            self.assertFalse(observed_controls(home, results, settings)[0]["verified"])
            source.unlink()
            self.assertFalse(observed_controls(home, results, settings)[0]["verified"])


class RegistrationRestorationTest(unittest.TestCase):
    def test_only_trial_registrations_are_restored(self):
        original = b'model = "existing-model"\n\n[projects."/user/project"]\ntrust_level = "trusted"\n'
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "config.toml"
            path.write_bytes(original + b'\n[projects."/tmp/samcheonpo-ab-test/task-A/repo"]\ntrust_level = "trusted"\n')
            result = restore_trial_registrations(path, original, {"samcheonpo-ab-test"})
            self.assertTrue(result["restored"])
            self.assertEqual(path.read_bytes(), original)

    def test_concurrent_user_changes_are_preserved(self):
        original = b'model = "existing-model"\n'
        changed = b'model = "user-changed-model"\n\n[projects."/tmp/samcheonpo-ab-test/task-A/repo"]\ntrust_level = "trusted"\n'
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "config.toml"
            path.write_bytes(changed)
            result = restore_trial_registrations(path, original, {"samcheonpo-ab-test"})
            self.assertFalse(result["restored"])
            self.assertEqual(path.read_bytes(), changed)


if __name__ == "__main__":
    unittest.main()
