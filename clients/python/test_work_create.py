"""Client.work_create and `work create` on a disposable local board (set
SWARMMEMO_TEST_BINARY); never a public service."""
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
import urllib.error
import urllib.request

import swarmmemo as memo


@unittest.skipUnless(os.environ.get("SWARMMEMO_TEST_BINARY"), "set SWARMMEMO_TEST_BINARY for a real board")
class LiveWorkCreateTests(unittest.TestCase):
    def setUp(self):
        folder = tempfile.TemporaryDirectory()
        self.addCleanup(folder.cleanup)
        self.work = Path(folder.name)
        with socket.socket() as reservation:
            reservation.bind(("127.0.0.1", 0))
            port = reservation.getsockname()[1]
        self.env = {**os.environ, "DATA_DIR": str(self.work / "data"), "LISTEN_ADDR": f"127.0.0.1:{port}", "ALLOW_INSECURE_LOCAL": "true",
                    "ALLOWANCE_LEDGER": "on"}
        self.server = subprocess.Popen([os.environ["SWARMMEMO_TEST_BINARY"], "serve"], env=self.env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        self.addCleanup(self.stop)
        self.origin = f"http://127.0.0.1:{port}"
        for _ in range(200):
            try:
                with urllib.request.urlopen(self.origin + "/health", timeout=1): break
            except OSError:
                if self.server.poll() is not None: self.fail("server exited during startup")
                time.sleep(0.05)
        self.keys = {}
        for name in ("requester", "reviewer"):
            self.keys[name] = self.work / f"{name}.json"
            memo.keygen(self.keys[name])
            self.api(name).command("post", room="lobby", text=f"{name} is here", request_id=memo.uuid.uuid4().hex)
        self.api("reviewer").command("agent.register", handle="review-bot")
        self.api("requester").command("agent.register", handle="requester-bot")

    def stop(self):
        self.server.terminate()
        self.server.wait(timeout=10)

    def api(self, who):
        return memo.Client(self.origin, memo.load_key(self.keys[who]))

    def fp(self, who):
        return memo.hashlib.sha256(memo.public_bytes(memo.load_key(self.keys[who]))).hexdigest()

    def operator(self, *argv):
        return subprocess.run([os.environ["SWARMMEMO_TEST_BINARY"], *argv], env=self.env, check=True, capture_output=True, text=True).stdout

    def fund_board(self):
        """Give the board a credit budget for the operator to grant from."""
        params = json.loads(self.operator("params", "show", "allowance"))["body"]
        params["resources"]["credit"].update(budget=100_000, spend_ceiling=100_000, inbound_cap=100_000, grant_share_ppm=1_000_000)
        (self.work / "allowance.json").write_text(json.dumps(params))
        self.operator("params", "set", "allowance", str(self.work / "allowance.json"), "--reason", "test credit")

    def grant(self, who, credits):
        """Operator-granted credit, which is transferable, so it can be held as a reward."""
        self.fund_board()
        self.operator("allowance", "grant", self.fp(who), "credit", str(credits), "--reason", "test")

    def request(self, text):
        posted = self.api("requester").command("post", room="lobby", kind="request", text=text, request_id=memo.uuid.uuid4().hex)
        return posted["receipt"]["id"]

    def read(self, message_id):
        with urllib.request.urlopen(f"{self.origin}/api/work/{message_id}", timeout=5) as response:
            return json.loads(response.read())["data"]["work"]

    def test_rewarded_work_with_a_named_reviewer(self):
        self.grant("requester", 1000)
        message_id = self.request("Review my Go patch")
        self.api("requester").work_create(message_id, reward=300, reviewer="review-bot", reviewer_fee=20,
                                          reward_note="+ thanks", title="Review my Go patch", capabilities=["code-review"])
        work = self.read(message_id)
        self.assertEqual((work["state"], work["title"], work["reward"]["amount"], work["reward"]["state"]), ("open", "Review my Go patch", 300, "held"))
        self.assertEqual((work["reviewer"]["id"], work["reviewer_fee"]["amount"]), (self.fp("reviewer"), 20))
        self.assertEqual(work["reward_note"], "+ thanks")

    def test_cli_creates_unpaid_work(self):
        message_id = self.request("Write a haiku")
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            code = memo.main(["--url", self.origin, "--key", str(self.keys["requester"]), "work", "create", message_id,
                              "--title", "Write a haiku", "--capability", "writing", "--ttl", "86400"])
        self.assertEqual(code, 0, out.getvalue())
        work = self.read(message_id)
        self.assertEqual((work["state"], work["title"], work["capabilities"]), ("open", "Write a haiku", ["writing"]))
        self.assertNotIn("reward", work)

    def test_server_errors_come_through(self):
        message_id = self.request("Paid, but no credit to hold")
        with self.assertRaises(memo.APIError) as raised:
            self.api("requester").work_create(message_id, title=" ", capabilities=[])
        self.assertEqual(raised.exception.code, "invalid_work_data")
        with self.assertRaises(memo.APIError):  # no transferable credit: refused, nothing created
            self.api("requester").work_create(message_id, reward=300, title="Paid", capabilities=[])
        with self.assertRaises(urllib.error.HTTPError) as missing:
            self.read(message_id)
        self.assertEqual(missing.exception.code, 404)


class WorkListKindsTest(unittest.TestCase):
    def test_cli_takes_every_kind_the_server_lists(self):
        # works.list kind: the eight effective states plus rewarded and earn (tali b9502e26).
        import swarmmemo
        parser = swarmmemo.build_parser()
        for kind in swarmmemo.WORK_LIST_KINDS:
            with self.subTest(kind=kind):
                self.assertEqual(parser.parse_args(["work", "list", "--kind", kind]).kind, kind)
        self.assertEqual(len(swarmmemo.WORK_LIST_KINDS), 10)


class ReviewRelationshipsTest(unittest.TestCase):
    def test_cli_takes_every_relationship_the_server_takes(self):
        import re, pathlib, swarmmemo
        go = (pathlib.Path(__file__).resolve().parents[2] / "internal/reviews/review.go").read_text()
        server = re.findall(r'"([a-z_]+)"', re.search(r"var Relationships = \[\]string\{([^}]*)\}", go).group(1))
        self.assertEqual(sorted(swarmmemo.REVIEW_RELATIONSHIPS), sorted(server))
        parser = swarmmemo.build_parser()
        for value in server:
            with self.subTest(value=value):
                self.assertEqual(parser.parse_args(["review", "x402:GET https://x.example/y", "good", "t", "--relationship", value]).relationship, value)


class WorkAcceptTierTest(unittest.TestCase):
    def test_accept_names_a_standing_tier(self):
        import json, swarmmemo
        client = swarmmemo.Client("https://example.org", swarmmemo.crypto()[0].generate())
        sent = []
        client.command = lambda operation, **fields: sent.append((operation, fields)) or {"ok": True}
        client.work_accept("a" * 32, 1, generation="b" * 32, result_sha256="c" * 64, tier="bug+fix")
        operation, fields = sent[0]
        self.assertEqual(operation, "work.accept")
        self.assertEqual(json.loads(fields["data"])["tier"], "bug+fix")
        args = swarmmemo.build_parser().parse_args(["work", "accept", "a" * 32, "1", "--tier", "bug+fix"])
        self.assertEqual(args.tier, "bug+fix")


if __name__ == "__main__":
    unittest.main()

