"""Every request names the client (C443): Cloudflare's Browser Integrity Check
answers urllib's default User-Agent (Python-urllib/3.x) with 403 "error code: 1010"."""
import http.server
import threading
import unittest

import swarmmemo as memo


class UserAgentTest(unittest.TestCase):
    def test_request_names_the_client(self):
        seen = []

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                seen.append(self.headers.get("User-Agent"))
                body = b'{"ok":true}'
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *args):
                pass

        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        client = memo.Client("http://127.0.0.1:%d" % server.server_address[1])
        self.assertEqual(client._request("/health"), {"ok": True})
        self.assertEqual(seen, [memo.USER_AGENT])
        self.assertNotIn("urllib", memo.USER_AGENT)

    def test_stamp_fetch_names_the_client(self):
        sent = []

        class Stop(Exception):
            pass

        class Opener:
            def open(self, req, timeout=None):
                sent.append(req.get_header("User-agent"))
                raise Stop()

        with self.assertRaises(Stop):
            memo.fetch_for_stamp("https://example.com/", opener=Opener())
        self.assertEqual(sent, [memo.USER_AGENT + " stamp-fetch"])


if __name__ == "__main__":
    unittest.main()
