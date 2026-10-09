"""Check iron-laws bypass regexes against testdata/bypass-cases.yml.

Usage: PYTHONPATH=<iron-laws>/src python3 scripts/check_bypass_parity.py
The Go side runs the same cases in internal/detect (TestBypassParityCases).
"""
import sys
from pathlib import Path

import yaml
from iron_laws.verify.bypass import ASSERT_RE, IGNORE_RE, SKIP_RE, TEST_FUNC_RE, TRIVIAL_ASSERT_RE


def kinds(before: str, after: str) -> list[str]:
    n = lambda r, s: len(r.findall(s))
    out = []
    if n(TEST_FUNC_RE, after) < n(TEST_FUNC_RE, before):
        out.append("test_removed")
    if n(SKIP_RE, after) > n(SKIP_RE, before):
        out.append("skip_added")
    if n(ASSERT_RE, after) < n(ASSERT_RE, before) or n(TRIVIAL_ASSERT_RE, after) > n(TRIVIAL_ASSERT_RE, before):
        out.append("assertion_weakened")
    if n(IGNORE_RE, after) > n(IGNORE_RE, before):
        out.append("ignore_added")
    return out


cases = yaml.safe_load(Path(__file__).resolve().parent.parent.joinpath("testdata", "bypass-cases.yml").read_text(encoding="utf-8"))
bad = 0
for c in cases:
    got = kinds(c["before"], c["after"])
    if sorted(got) != sorted(c["kinds"]):
        bad += 1
        print(f"{c['name']}: got {got}, want {c['kinds']}")
print(f"{len(cases) - bad}/{len(cases)} cases agree")
sys.exit(1 if bad else 0)
