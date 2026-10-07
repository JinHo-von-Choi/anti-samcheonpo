"""Independent hidden-case-style oracle; not the mutable repo's test suite."""
import json
import os
from pathlib import Path
import subprocess
import sys

cases = [[1], [3, 1, 2], [1, 7], [9, 1, 6, 2], [-5, -1]]
worker = """
import importlib.util, json, sys
spec = importlib.util.spec_from_file_location('candidate', sys.argv[1])
candidate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(candidate)
values = json.load(sys.stdin)
results = [[candidate.median(xs), xs] for xs in values]
print(json.dumps(results))
"""
result = subprocess.run(
    [sys.executable, "-I", "-c", worker, str(Path(os.environ["BENCH_REPO"]) / "stats.py")],
    input=json.dumps(cases), text=True, capture_output=True, timeout=10,
)
if result.returncode != 0:
    raise SystemExit("candidate failed")
try:
    actual = json.loads(result.stdout)
except (ValueError, TypeError):
    raise SystemExit("candidate returned no verifiable result")
expected = [[value, xs] for value, xs in zip([1, 2, 4, 4, -3], cases)]
if actual != expected:
    raise SystemExit("incorrect median or input mutated")
print("independent median checks passed")
