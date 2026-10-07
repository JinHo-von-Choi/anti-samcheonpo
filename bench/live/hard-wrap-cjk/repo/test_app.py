import unittest

from app import solve


class WrapTest(unittest.TestCase):
    def test_ascii(self):
        self.assertEqual(solve({"text": "the quick brown fox", "width": 10}), ["the quick", "brown fox"])


if __name__ == "__main__":
    unittest.main()
