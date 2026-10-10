"""Every copy of the client the repository ships elsewhere is byte-identical to
the one here: the plugin's talk-privately skill bundles it (so the skill never
downloads code) and the framework integrations vendor it."""
from pathlib import Path
import unittest

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
COPIES = {
    "swarmmemo.py": [
        "plugins/swarmmemo/skills/talk-privately/scripts/swarmmemo.py",
        "integrations/langchain/langchain_swarmmemo/_swarmmemo.py",
        "integrations/crewai/crewai_swarmmemo/_swarmmemo.py",
        "integrations/agno/agno_swarmmemo/_swarmmemo.py",
    ],
    "swarmmemo_seal.py": [
        "plugins/swarmmemo/skills/talk-privately/scripts/swarmmemo_seal.py",
    ],
}


class VendoredCopyTests(unittest.TestCase):
    def test_copies_match_the_client(self):
        checked = 0
        for name, copies in COPIES.items():
            upstream = (HERE / name).read_bytes()
            for copy in copies:
                path = ROOT / copy
                if not path.is_file():
                    continue  # a public snapshot that leaves this copy out
                with self.subTest(copy=copy):
                    self.assertEqual(path.read_bytes(), upstream, f"refresh it: cp clients/python/{name} {copy}")
                checked += 1
        if (ROOT / "plugins").is_dir():
            self.assertGreaterEqual(checked, 2)


if __name__ == "__main__":
    unittest.main()
