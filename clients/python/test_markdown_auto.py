"""C324: the client signs format "markdown" by default when a post's text is
Markdown that renders without loss. Its detector is a port of the board's
(internal/markdown AutoRender) and agrees with every vector Go wrote to
testdata/markdown_auto.json; an explicit format or data always wins."""
import contextlib
import io
import json
from pathlib import Path
import unittest
from unittest.mock import patch

import swarmmemo as memo

VECTORS = json.loads((Path(__file__).resolve().parents[2] / "testdata/markdown_auto.json").read_text())["vectors"]
TABLE = "| a | b |\n|---|---|\n| **x** | `y` |"


class MarkdownAutoTests(unittest.TestCase):
    def test_agrees_with_the_go_vectors(self):
        self.assertGreater(len(VECTORS), 20)
        for v in VECTORS:
            with self.subTest(v["name"]):
                self.assertEqual(memo.markdown_auto(v["text"]), v["render"])

    def test_size_bound(self):
        self.assertFalse(memo.markdown_auto("# Title\n\n" + "word " * (memo.MARKDOWN_AUTO_BYTES // 5)))

    def sent(self, client, *args, **kwargs):
        sent = []

        def fake(self, path, body=None):
            sent.append(body)
            return {"ok": True}
        with patch.object(memo.Client, "_request", fake):
            client.post("lobby", "main", *args, **kwargs)
        return sent[0]

    def test_post_signs_markdown_by_default(self):
        key = memo.crypto()[0].from_private_bytes(bytes(32))
        signed, anonymous = memo.Client("https://example.org", key), memo.Client("https://example.org")
        self.assertEqual(self.sent(signed, TABLE)["data"], memo.MARKDOWN_POST_DATA)
        self.assertNotIn("data", self.sent(signed, "Plain words, 2*3*4."))
        self.assertNotIn("data", self.sent(signed, TABLE, format="plain"))
        self.assertEqual(self.sent(signed, "plain", format="markdown")["data"], memo.MARKDOWN_POST_DATA)
        edit = '{"schema":1,"supersedes":"' + "a" * 32 + '"}'
        self.assertEqual(self.sent(signed, TABLE, data=edit)["data"], edit)  # the caller's data wins
        self.assertNotIn("data", self.sent(anonymous, TABLE))  # an unsigned post cannot sign a format

    def test_cli_format_flag(self):
        key = memo.crypto()[0].from_private_bytes(bytes(32))
        for argv, want in (([], memo.MARKDOWN_POST_DATA), (["--format", "plain"], None)):
            sent = []

            def fake(self, path, body=None):
                sent.append(body)
                return {"ok": True}
            with patch.object(memo, "load_key", return_value=key), patch.object(memo.Client, "_request", fake), contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(memo.main(["--key", "k.json", "post", "lobby", "main", TABLE, *argv]), 0)
            self.assertEqual(sent[0].get("data"), want)


if __name__ == "__main__":
    unittest.main()
