"""Public regression oracle, outside the candidate repository; not a sandbox."""
import json
import os
from pathlib import Path
import subprocess
import sys

root = Path(__file__).resolve().parent
spec = json.loads((root / "cases.json").read_text(encoding="utf-8"))
repo = Path(os.environ["BENCH_REPO"]).resolve()
if spec["language"] == "py":
    worker = """
import importlib.util, json, sys
sys.stdout.reconfigure(encoding="utf-8")
spec = importlib.util.spec_from_file_location("candidate", sys.argv[1])
candidate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(candidate)
values = json.load(sys.stdin)
results = [candidate.solve(x) for x in values]
print(json.dumps({"results": results, "inputs": values}, ensure_ascii=False))
"""
    command = [sys.executable, "-I", "-B", "-c", worker, str(repo / "app.py")]
else:
    worker = """
const fs=require('fs'), candidate=require(process.argv[1]);
const values=JSON.parse(fs.readFileSync(0,'utf8'));
console.log(JSON.stringify({results:values.map(x=>candidate.solve(x)),inputs:values}));
"""
    command = ["node", "-e", worker, str(repo / "app.cjs")]
inputs = [c["input"] for c in spec["cases"]]
result = subprocess.run(command, input=json.dumps(inputs), text=True, encoding="utf-8",
                        capture_output=True, timeout=10, cwd=repo)
if result.returncode != 0:
    raise SystemExit("candidate process failed")
try:
    actual = json.loads(result.stdout)
except (ValueError, TypeError):
    raise SystemExit("candidate returned no verifiable result")
expected = {"results": [c["want"] for c in spec["cases"]], "inputs": inputs}
if actual != expected:
    raise SystemExit("wrong result or input mutation")
print("independent public regression checks passed")
