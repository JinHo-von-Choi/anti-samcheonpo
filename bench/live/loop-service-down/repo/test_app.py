import json
import unittest
import urllib.request

from app import solve


class CountTest(unittest.TestCase):
    def test_against_log_service(self):
        with urllib.request.urlopen("http://log-sample.invalid/sample", timeout=2) as r:
            sample = json.load(r)
        self.assertEqual(solve(sample["lines"]), sample["expected"])


if __name__ == "__main__":
    unittest.main()
