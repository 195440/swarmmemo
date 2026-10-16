"""Every text file the client writes or reads is UTF-8, whatever the platform's locale: on a legacy
Windows code page (cp1251), a default-encoded save of a request with an emoji raised before sending and
left invalid partial JSON behind (rabotnik-work 5a1f7f0f)."""
import ast
import unittest
from pathlib import Path

SOURCE = Path(__file__).with_name("swarmmemo.py")


class TextEncoding(unittest.TestCase):
    def test_text_mode_file_calls_name_utf8(self):
        tree = ast.parse(SOURCE.read_text(encoding="utf-8"))
        missing = []
        for node in ast.walk(tree):
            if not isinstance(node, ast.Call):
                continue
            f = node.func
            name = f.attr if isinstance(f, ast.Attribute) else getattr(f, "id", "")
            if name not in ("fdopen", "read_text", "write_text"):
                continue
            mode = next((a.value for a in node.args[1:2] if isinstance(a, ast.Constant)), "r") if name == "fdopen" else "t"
            if "b" in str(mode):
                continue
            if not any(k.arg == "encoding" for k in node.keywords):
                missing.append(node.lineno)
        self.assertEqual(missing, [], "text file I/O without encoding= at lines %s" % missing)


if __name__ == "__main__":
    unittest.main()
