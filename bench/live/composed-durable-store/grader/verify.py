"""Public composition/regression oracle. It is not a private holdout or sandbox."""
import json
import os
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
import random
import subprocess
import sys
import tempfile

repo = Path(os.environ["BENCH_REPO"]).resolve()
worker = """
import importlib.util,json,sys
spec=importlib.util.spec_from_file_location('candidate',sys.argv[1])
module=importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
print(json.dumps(module.solve(json.load(sys.stdin)),ensure_ascii=False))
"""


def call(request, expected):
    result = subprocess.run([sys.executable, "-I", "-B", "-c", worker, str(repo / "app.py")],
                            input=json.dumps(request), capture_output=True, text=True,
                            cwd=repo, timeout=3)
    if result.returncode:
        raise SystemExit("candidate execution failed")
    try:
        observed = json.loads(result.stdout)
    except ValueError:
        raise SystemExit("candidate returned no structured result")
    if observed != expected:
        raise SystemExit("independent state transition failed")


def check_key(case):
    database, key, value = case
    request = dict(path=database, key=key)
    call(dict(request, op="put", value=value), {"inserted": True})
    call(dict(request, op="put", value=value + 1), {"inserted": False})
    call(dict(request, op="get"), {"value": value})
    call(dict(request, op="put", value=True), {"error": "invalid"})
    call(dict(request, op="unknown"), {"error": "operation"})
    call(dict(request, op="get"), {"value": value})
    call(dict(request, op="delete"), {"deleted": True})
    call(dict(request, op="get"), {"value": None})
    call(dict(request, op="delete"), {"deleted": False})


with tempfile.TemporaryDirectory() as directory:
    rng = random.Random(20261104)
    cases = [(str(Path(directory) / f"state-{index}.json"),
              f"키-{index}-{rng.randrange(10**9)}", rng.randint(-10000, 10000))
             for index in range(12)]
    # Each case owns its database; requests within a case still run in order
    # and in separate processes. Independent cases can overlap startup work.
    with ThreadPoolExecutor(max_workers=3) as pool:
        list(pool.map(check_key, cases))
    call(dict(path=str(Path(directory) / "empty.json"), key="", op="put", value=0),
         {"error": "invalid"})
print("independent persistence, idempotence, Unicode and error-path checks passed")
