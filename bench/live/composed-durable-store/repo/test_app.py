import tempfile
import unittest
from pathlib import Path
from app import solve


class StoreTest(unittest.TestCase):
    def test_basic(self):
        with tempfile.TemporaryDirectory() as folder:
            path = str(Path(folder) / "store.json")
            self.assertEqual(solve(dict(path=path, op="put", key="a", value=4)), {"inserted": True})
            self.assertEqual(solve(dict(path=path, op="get", key="a")), {"value": 4})


if __name__ == "__main__":
    unittest.main()
