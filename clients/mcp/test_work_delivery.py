"""C428: a scoped (delegated) worker's one-step work claims, staged by the bridge and
delivered through DelegatedClient and the outbox, against a real disposable Go server
(SWARMMEMO_MCP_TEST_BINARY). Three bugs on this path (C425 delegated work data, C426
standing-seat acknowledgement, C427 stage_work result_id) were each fixed on their own;
this runs the whole path end to end. Pure Python: no mcp package needed."""
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import unittest
import urllib.request

from policy import load_profile
import operations as op

PAYEE = "0x" + "ab" * 20


@unittest.skipUnless(os.environ.get("SWARMMEMO_MCP_TEST_BINARY"), "requires explicit disposable Go binary")
class DelegatedOneStepClaimTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory(prefix="swarmmemo-mcp-work-"); self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        with socket.socket() as reservation:
            reservation.bind(("127.0.0.1", 0)); port = reservation.getsockname()[1]
        self.origin = "http://127.0.0.1:" + str(port)
        data = self.root / "server"; data.mkdir(mode=0o700)
        # USDC rewards on (the RPC is only dialled to settle, which never happens here) and the
        # credit ledger on, so the operator can grant the requester its rewards' credit.
        self.env = {**os.environ, "DATA_DIR": str(data), "LISTEN_ADDR": "127.0.0.1:" + str(port), "PUBLIC_URL": self.origin,
                    "ALLOW_INSECURE_LOCAL": "true", "ALLOWANCE_LEDGER": "on", "WORK_USDC_RPC_URL": "https://rpc.example.org/base"}
        server = subprocess.Popen([os.environ["SWARMMEMO_MCP_TEST_BINARY"], "serve"], env=self.env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        def cleanup():
            server.terminate()
            try: server.wait(timeout=5)
            except subprocess.TimeoutExpired: server.kill(); server.wait(timeout=5)
        self.addCleanup(cleanup)
        for _ in range(200):
            try:
                with urllib.request.urlopen(self.origin + "/api/changes?after=-1", timeout=1) as response: self.generation = json.load(response)["generation"]
                break
            except OSError:
                if server.poll() is not None: self.fail("server exited during startup")
                time.sleep(0.05)
        else: self.fail("fixture failed to start")
        new_key = op.memo.crypto()[0].generate
        parent, self.requester = op.memo.Client(self.origin, new_key()), op.memo.Client(self.origin, new_key())
        parent.command("room.create", room="lab", visibility="public")
        seed = os.urandom(32); child = op.memo.crypto()[0].from_private_bytes(seed)
        public = op.memo.b64(op.memo.public_bytes(child))
        operations = ["post", "work.claim", "work.renew", "work.submit"]
        parent.send(parent.prepare_enrollment(child, room="lab", ttl=3600, amount=100000, generation=self.generation, operations=operations))
        key_path = self.root / "child.json"
        key_path.write_text(json.dumps({"version": 1, "public_key": public, "private_key": op.memo.b64(seed)})); key_path.chmod(0o600)
        state = self.root / "state"; state.mkdir(mode=0o700)
        profile = {"schema": 1, "origin": self.origin, "service_id": "swarmmemo.com", "public_key": public, "room": "lab", "mode": "scoped-send", "key_path": str(key_path),
                   "delegation": {"schema": 1, "grant_id": op.hashlib.sha256(op.memo.public_bytes(child)).hexdigest(), "generation": self.generation},
                   "operations": operations, "state_dir": str(state)}
        path = self.root / "profile.json"; path.write_text(json.dumps(profile)); path.chmod(0o600)
        self.profile = load_profile(path)

    def operator(self, *argv):
        done = subprocess.run([os.environ["SWARMMEMO_MCP_TEST_BINARY"], *argv], env=self.env, capture_output=True, text=True, timeout=30)
        self.assertEqual(done.returncode, 0, done.stderr)
        return done.stdout

    def grant_requester(self, credits):
        params = json.loads(self.operator("params", "show", "allowance"))["body"]
        params["resources"]["credit"].update(budget=100_000, spend_ceiling=100_000, inbound_cap=100_000, grant_share_ppm=1_000_000)
        (self.root / "allowance.json").write_text(json.dumps(params))
        self.operator("params", "set", "allowance", str(self.root / "allowance.json"), "--reason", "test credit")
        fingerprint = op.hashlib.sha256(op.memo.public_bytes(self.requester.key)).hexdigest()
        self.operator("allowance", "grant", fingerprint, "credit", str(credits), "--reason", "test")

    def work(self, message_id):
        with urllib.request.urlopen(self.origin + "/api/work/" + message_id, timeout=5) as response: return json.load(response)["data"]["work"]

    def deliver(self, intent_id, staged):
        return op.dispatch(self.profile, "deliver_intent", {"intent_id": intent_id, "intent_sha256": staged["intent_sha256"]})["item"]

    def post_result(self, intent_id, root):
        staged = op.dispatch(self.profile, "stage_post", {"intent_id": intent_id, "text": "Bug: the thing breaks; repro attached", "reply_to": root})
        return self.deliver(intent_id, staged)["response"]["receipt"]["id"]

    def test_standing_task_one_step_claim_with_payout_address(self):
        root = self.requester.command("post", room="lab", kind="request", text="Standing bug bounty: report a bug")["receipt"]["id"]
        self.grant_requester(5000)  # after the post: a grant goes to a registered agent
        self.requester.command("work.create", message_id=root, ttl=10 * 86400, data=json.dumps({
            "schema": 1, "generation": self.generation, "title": "Standing bug bounty", "capabilities": ["bugs"],
            "seats": 2, "tiers": [{"name": "bug", "credits": 300, "usdc": "0.20"}]}))
        self.assertIn("standing", self.work(root))
        result = self.post_result("result", root)
        staged = op.dispatch(self.profile, "stage_work", {"intent_id": "claim", "action": "claim", "work_id": root, "generation": self.generation,
                                                          "result_id": result, "payout_address": PAYEE})
        self.assertEqual(staged["operation"], "work.claim")
        item = self.deliver("claim", staged)
        self.assertEqual(item["state"], "acknowledged", item)
        self.assertEqual(item["response"]["data"]["ack"]["resolved_from"], root)
        work = self.work(result)
        self.assertEqual(work["state"], "submitted")
        self.assertEqual(work["standing_id"], root)
        self.assertEqual(work["attempt_grant_id"], self.profile.grant_id)
        self.assertEqual(work["reward_usdc"]["state"], "promised")
        self.assertEqual(work["reward_usdc"]["pay_to"].lower(), PAYEE)

    def test_ordinary_task_one_step_claim(self):
        root = self.requester.command("post", room="lab", kind="simulation", text="Disposable operator fixture, no external execution.")["receipt"]["id"]
        self.requester.command("work.create", message_id=root, ttl=3600, data=json.dumps({"schema": 1, "generation": self.generation, "title": "Fixed local evidence", "capabilities": []}))
        result = self.post_result("result", root)
        staged = op.dispatch(self.profile, "stage_work", {"intent_id": "claim", "action": "claim", "work_id": root, "generation": self.generation, "result_id": result})
        item = self.deliver("claim", staged)
        self.assertEqual(item["state"], "acknowledged", item)
        work = self.work(root)
        self.assertEqual(work["state"], "submitted")
        self.assertEqual(work["attempt_grant_id"], self.profile.grant_id)


if __name__ == "__main__":
    unittest.main()
