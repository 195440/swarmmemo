"""Client.gate and `gate` on a disposable local board (set SWARMMEMO_TEST_BINARY);
never a public service: the SwarmMemo Gate's proof of work, identity and agent task."""
import base64
import contextlib
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


class GateSolveTests(unittest.TestCase):
    def test_solution_meets_the_bits(self):
        prefix = "0f" * 16
        solution = memo.gate_solve(prefix, 12)
        digest = memo.hashlib.sha256((prefix + solution).encode()).digest()
        self.assertLess(int.from_bytes(digest, "big"), 1 << (256 - 12))


@unittest.skipUnless(os.environ.get("SWARMMEMO_TEST_BINARY"), "set SWARMMEMO_TEST_BINARY for a real board")
class LiveGateTests(unittest.TestCase):
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

    def payload(self, token):
        prefix, body, _ = token.split(".")
        self.assertEqual(prefix, "smg1")
        return json.loads(base64.urlsafe_b64decode(body + "=" * (-len(body) % 4)))

    def test_work_identity_and_agent_task(self):
        anonymous = memo.Client(self.origin).gate("https://shop.example", nonce="session-42")["data"]
        p = self.payload(anonymous["token"])
        self.assertEqual((p["origin"], p["nonce"], p["passed"], p["proofs"]), ("https://shop.example", "session-42", True, ["pow"]))
        signed = memo.Client(self.origin, memo.load_key(self.key)).gate("https://shop.example")["data"]
        p = self.payload(signed["token"])
        self.assertEqual(p["proofs"], ["pow", "identity"])
        self.assertEqual(len(p["subject"]), 64)
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            memo.main(["--url", self.origin, "--key", str(self.key), "gate", "--agent-task"])
        result = json.loads(out.getvalue())["data"]
        self.assertTrue(result["passed"])
        self.assertIn("agent_task", result["proofs"])
        self.assertTrue(result["card"]["url"].startswith("/gate/agent/r/"))


if __name__ == "__main__":
    unittest.main()
