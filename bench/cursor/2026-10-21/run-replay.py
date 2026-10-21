"""Replay sanitized Cursor captures against the current Go source via an overlay."""
import argparse
import json
import pathlib
import subprocess
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--go", default="go")
args = parser.parse_args()
here = pathlib.Path(__file__).resolve().parent
repo = here.parents[2]
with tempfile.TemporaryDirectory(prefix="cursor-support-replay-") as scratch:
    overlay = pathlib.Path(scratch) / "overlay.json"
    overlay.write_text(json.dumps({"Replace": {
        str(repo / "internal/live/cursor_support_probe_test.go"):
            str(here / "replay_test.go.txt")
    }}))
    result = subprocess.run([
        args.go, "test", "-overlay", str(overlay), "./internal/live",
        "-run", "^TestCursorSupportProbe$", "-count=1", "-timeout=120s", "-v"
    ], cwd=repo)
    raise SystemExit(result.returncode)
