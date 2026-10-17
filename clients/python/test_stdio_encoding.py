"""The CLI's stdio is UTF-8 whatever the console code page (C393): on Windows a piped
stdout/stdin uses the locale's (cp1252, cp1251), so printing a reply with "→" or an emoji
raised UnicodeEncodeError after the post was sent (reported as a request failure, so a retry
duplicated it), and text piped to `-` was decoded as the code page and signed garbled."""
import json
import os
from pathlib import Path
import subprocess
import sys
import unittest

HERE = Path(__file__).resolve().parent
TEXT = "Привет → 🙂"
# Stubs the network: memory put echoes back the value it would have stored, with a reply.
DRIVER = """
import sys
sys.path.insert(0, %r)
import swarmmemo
def service_call(self, service, method, data, cost, request_id=None):
    sys.stderr.write("SENT " + ascii(data["value"]) + "\\n")
    return {"ok": True, "reply": %r, "stored": data["value"]}
swarmmemo.Client.service_call = service_call
sys.exit(swarmmemo.main(sys.argv[1:]))
""" % (str(HERE), TEXT)


def run(stdin_bytes):
    env = {**os.environ, "PYTHONIOENCODING": "cp1252", "PYTHONUTF8": "0"}
    return subprocess.run([sys.executable, "-c", DRIVER, "memory", "put", "note", "-"], input=stdin_bytes,
                          capture_output=True, env=env, timeout=60)


class StdioEncoding(unittest.TestCase):
    def test_cp1252_console_prints_and_reads_utf8(self):
        done = run(TEXT.encode("utf-8"))
        self.assertEqual(done.returncode, 0, done.stderr.decode("utf-8", "replace"))
        self.assertIn("SENT " + ascii(TEXT), done.stderr.decode("utf-8"))  # stdin `-` round-trips, not mojibake
        result = json.loads(done.stdout.decode("utf-8"))
        self.assertEqual(result["reply"], TEXT)
        self.assertEqual(result["stored"], TEXT)

    def test_undecodable_input_is_not_a_request_failure(self):
        done = run(b"caf\xe9")  # Latin-1, not UTF-8
        err = done.stderr.decode("utf-8", "replace")
        self.assertEqual(done.returncode, 1)
        self.assertNotIn("SENT", err)
        self.assertNotIn("request failed", err)
        self.assertIn("UnicodeDecodeError", err)
        self.assertIn("not a request failure", err)


if __name__ == "__main__":
    unittest.main()
