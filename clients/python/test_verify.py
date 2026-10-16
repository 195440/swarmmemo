"""Client.verify_publish and `verify` (C353): the checks parser offline, and on a disposable
local board (set SWARMMEMO_TEST_BINARY) a published record read back by id, by repository and
at /v/ID/badge.svg; never a public service."""
import base64
import contextlib
import hashlib
import io
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import unittest
import urllib.request

import swarmmemo as memo

COMMIT = "0123456789abcdef0123456789abcdef01234567"


class ParseCheckTests(unittest.TestCase):
    def test_name_state_and_note(self):
        self.assertEqual(memo.parse_check("tests=pass:41 passed: all green"), {"property": "tests", "state": "pass", "note": "41 passed: all green"})
        self.assertEqual(memo.parse_check("fuzz=not_checked"), {"property": "fuzz", "state": "not_checked"})
        for bad in ("tests", "=pass", "tests="):
            with self.assertRaises(ValueError):
                memo.parse_check(bad)

    def test_publish_signs_the_record_as_data(self):
        client = memo.Client("https://swarmmemo.com")
        sent = []
        client.send = lambda command, *a, **k: sent.append(command) or {"ok": True}
        client.verify_publish("pytest -q", "linux", "ab" * 32, [{"property": "tests", "state": "pass"}], "Windows",
                              repo="https://github.com/o/r", commit=COMMIT, request_id="r1")
        self.assertEqual(sent[0]["operation"], "verify.publish")
        self.assertEqual(sent[0]["request_id"], "r1")
        self.assertEqual(json.loads(sent[0]["data"]), {"schema": 1, "repo": "https://github.com/o/r", "commit": COMMIT, "command": "pytest -q",
                                                        "environment": "linux", "output_sha256": "ab" * 32,
                                                        "checks": [{"property": "tests", "state": "pass"}], "limits": "Windows"})

    def test_publish_supersedes(self):
        client = memo.Client("https://swarmmemo.com")
        sent = []
        client.send = lambda command, *a, **k: sent.append(command) or {"ok": True}
        client.verify_publish("pytest -q", "linux", "ab" * 32, [{"property": "tests", "state": "pass"}], "Windows",
                              file_sha256="cd" * 32, supersedes="0123456789abcdef")
        self.assertEqual(json.loads(sent[0]["data"])["supersedes"], "0123456789abcdef")
        args = memo.build_parser().parse_args(["verify", "publish", "--file-sha256", "cd" * 32, "--command", "x", "--env", "linux",
                                         "--output-sha256", "ab" * 32, "--check", "t=pass", "--limits", "none",
                                         "--supersedes", "0123456789abcdef"])
        self.assertEqual(args.supersedes, "0123456789abcdef")


@unittest.skipUnless(os.environ.get("SWARMMEMO_TEST_BINARY"), "set SWARMMEMO_TEST_BINARY for a real board")
class LiveVerifyTests(unittest.TestCase):
    def setUp(self):
        folder = tempfile.TemporaryDirectory()
        self.addCleanup(folder.cleanup)
        self.work = Path(folder.name)
        with socket.socket() as reservation:
            reservation.bind(("127.0.0.1", 0))
            port = reservation.getsockname()[1]
        env = {**os.environ, "DATA_DIR": str(self.work / "data"), "LISTEN_ADDR": f"127.0.0.1:{port}", "ALLOW_INSECURE_LOCAL": "true", "SERVICES": "notary"}
        self.server = subprocess.Popen([os.environ["SWARMMEMO_TEST_BINARY"], "serve"], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        self.addCleanup(self.stop)
        self.origin = f"http://127.0.0.1:{port}"
        for _ in range(200):
            try:
                with urllib.request.urlopen(self.origin + "/health", timeout=1): break
            except OSError:
                if self.server.poll() is not None: self.fail("server exited during startup")
                time.sleep(0.05)
        self.key = self.work / "key.json"
        memo.keygen(self.key)

    def stop(self):
        self.server.terminate()
        self.server.wait(timeout=10)

    def test_publish_read_and_badge(self):
        output = self.work / "out.txt"
        output.write_bytes(b"41 passed\n")
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            memo.main(["--url", self.origin, "--key", str(self.key), "verify", "publish", "--repo", "https://github.com/o/r", "--commit", COMMIT,
                       "--command", "pytest -q", "--env", "python 3.12, linux", "--output", str(output),
                       "--check", "tests=pass:41 passed", "--check", "lint=fail", "--limits", "Windows was not tested"])
        record = json.loads(out.getvalue())["data"]["verification"]
        self.assertEqual(record["output_sha256"], hashlib.sha256(b"41 passed\n").hexdigest())
        self.assertEqual((record["passed"], record["ran"], record["result"]), (1, 2, "failed"))
        # The signature checks offline, as the page's block does.
        b = lambda s: base64.urlsafe_b64decode(s + "=" * (-len(s) % 4))
        memo.crypto()[1].from_public_bytes(b(record["verifier"]["public_key"])).verify(b(record["signature"]), record["signed_payload"].encode())
        self.assertEqual(hashlib.sha256(record["signed_payload"].encode()).hexdigest(), record["record_sha256"])
        client = memo.Client(self.origin)
        self.assertEqual(client.command("verify.get", target=record["id"])["data"]["verification"]["id"], record["id"])
        listed = client.verifications(repo="https://github.com/o/r", commit=COMMIT)["data"]["verifications"]
        self.assertEqual([v["id"] for v in listed], [record["id"]])
        with urllib.request.urlopen(self.origin + "/v/" + record["id"] + "/badge.svg") as response:
            badge = response.read().decode()
            self.assertIn("1/2", badge)
            self.assertIn("public", response.headers["Cache-Control"])
        # A corrected record supersedes it: each names the other.
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            memo.main(["--url", self.origin, "--key", str(self.key), "verify", "publish", "--repo", "https://github.com/o/r", "--commit", COMMIT,
                       "--command", "pytest -q", "--env", "python 3.12, linux", "--output-sha256", "cd" * 32,
                       "--check", "tests=pass:41 passed", "--limits", "Windows was not tested", "--supersedes", record["id"]])
        fresh = json.loads(out.getvalue())["data"]["verification"]
        self.assertEqual(fresh["supersedes"], record["id"])
        self.assertEqual(client.command("verify.get", target=record["id"])["data"]["verification"]["superseded_by"], fresh["id"])


if __name__ == "__main__":
    unittest.main()
