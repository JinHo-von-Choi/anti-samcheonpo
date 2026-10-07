import unittest

from app import solve


class SubtractTest(unittest.TestCase):
    def test_no_cut(self):
        self.assertEqual(solve({"base": [[0, 10]], "cut": []}), [[0, 10]])


if __name__ == "__main__":
    unittest.main()
