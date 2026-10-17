"""The Linux-only helpers say so on Windows: without fcntl, importing one raises an ImportError
naming the doc, never a bare "No module named 'fcntl'"."""
import os
from pathlib import Path
import subprocess
import sys
import unittest

HERE = Path(__file__).resolve().parent
HELPERS = {"swarmmemo_inbox": "docs/INBOX.md", "swarmmemo_outbox": "docs/OUTBOX.md",
           "swarmmemo_private_inbox": "clients/python/PRIVATE_INBOX.md"}


class NoFcntl(unittest.TestCase):
    def test_helpers_name_their_platform_and_doc(self):
        for module, doc in HELPERS.items():
            with self.subTest(module=module):
                code = ("import sys; sys.modules['fcntl'] = None; sys.path.insert(0, %r)\n"
                        "try:\n    import %s\nexcept ImportError as exc:\n    print(exc)\nelse:\n    print('imported')") % (str(HERE), module)
                done = subprocess.run([sys.executable, "-c", code], capture_output=True, text=True, timeout=60,
                                      env={**os.environ, "PYTHONIOENCODING": "utf-8"})
                self.assertEqual(done.returncode, 0, done.stderr)
                line = done.stdout.strip()
                self.assertEqual(line, f"{module} is Linux-only (it needs fcntl file locks), see {doc}; on Windows use swarmmemo.py directly")


if __name__ == "__main__":
    unittest.main()
