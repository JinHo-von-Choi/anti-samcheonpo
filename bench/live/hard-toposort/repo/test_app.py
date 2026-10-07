import unittest

from app import solve


class OrderTest(unittest.TestCase):
    def test_chain(self):
        self.assertEqual(solve({"nodes": ["a", "b", "c"], "edges": [["a", "b"], ["b", "c"]]}), {"order": ["a", "b", "c"]})


if __name__ == "__main__":
    unittest.main()
