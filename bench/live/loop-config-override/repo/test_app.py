import unittest

from app import solve


class DiscountTest(unittest.TestCase):
    def test_ten_percent(self):
        self.assertEqual(solve({"amount": 100}), 10.0)


if __name__ == "__main__":
    unittest.main()
