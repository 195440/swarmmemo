"""Every Python base64 decoder follows the one rule of PROTOCOL.md (Encodings)
on testdata/base64_vectors.json: the client's unb64 (keys, signatures, data),
the sealing module's and verify_log.py's (receipts, signatures, log hashes), so
none accepts what the server or another verifier refuses (C446)."""
import json
from pathlib import Path
import unittest

import swarmmemo
import swarmmemo_seal
import verify_log

VECTORS = json.loads((Path(__file__).resolve().parents[2] / "testdata" / "base64_vectors.json").read_text())


class Base64RuleTests(unittest.TestCase):
    def check(self, name, decode, vectors, error):
        for v in vectors["accept"]:
            with self.subTest(decoder=name, value=v["value"]):
                self.assertEqual(decode(v["value"]).hex(), v["hex"])
        for v in vectors["reject"]:
            with self.subTest(decoder=name, value=v["value"], why=v["why"]):
                with self.assertRaises(error):
                    decode(v["value"])

    def test_base64url_decoders(self):
        url = VECTORS["base64url"]
        self.check("swarmmemo.unb64", swarmmemo.unb64, url, ValueError)
        self.check("swarmmemo_seal.unb64", swarmmemo_seal.unb64, url, swarmmemo_seal.SealError)
        self.check("verify_log.b64url", verify_log.b64url, url, verify_log.VerifyError)

    def test_padded_base64_decoder(self):
        self.check("verify_log.b64std", verify_log.b64std, VECTORS["base64"], verify_log.VerifyError)

    def test_refusal_says_what_to_fix(self):
        with self.assertRaises(ValueError) as caught:
            swarmmemo.unb64("aGVsbG9")
        self.assertIn("unpadded base64url", str(caught.exception))
        with self.assertRaises(verify_log.VerifyError) as caught:
            verify_log.b64url("aGVsbG8=", "the receipt's signature")
        self.assertIn("the receipt's signature is not canonical unpadded base64url", str(caught.exception))

    def test_a_noncanonical_signature_never_verifies(self):
        """The split C446 fixes: the same receipt with its signature's unused
        trailing bits set decodes to the same bytes, and must be refused."""
        sig = swarmmemo.b64(bytes(range(64)))
        last = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_".index(sig[-1])
        twin = sig[:-1] + "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"[last | 1]
        self.assertNotEqual(twin, sig)
        with self.assertRaises(ValueError):
            swarmmemo.unb64(twin)
        with self.assertRaises(verify_log.VerifyError):
            verify_log.b64url(twin)


if __name__ == "__main__":
    unittest.main()
