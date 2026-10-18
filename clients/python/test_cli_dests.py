"""A subcommand's argument must never reuse a top-level option's name: argparse would
overwrite the global value (a positional `url` once replaced --url, the server, so
list-api and webhook add sent the signed command to the target host)."""
import argparse
import unittest

import swarmmemo as memo


def _subparsers(parser):
    for action in parser._actions:
        if isinstance(action, argparse._SubParsersAction):
            for name, sub in action.choices.items():
                yield name, sub
                yield from ((f"{name} {n}", s) for n, s in _subparsers(sub))


class SubcommandDests(unittest.TestCase):
    def test_no_subcommand_argument_shadows_a_global_option(self):
        parser = memo.build_parser()
        globals_ = {a.dest for a in parser._actions if not isinstance(a, argparse._SubParsersAction) and a.dest != "help"}
        for name, sub in _subparsers(parser):
            for action in sub._actions:
                with self.subTest(command=name, dest=action.dest):
                    self.assertNotIn(action.dest, globals_, f"{name}: argument dest {action.dest!r} overwrites the global option")

    def test_every_help_renders(self):
        # argparse %-formats help strings: a bare "50% of it" crashed `work create --help`
        # (and every command on Python 3.14, which checks help at build time). qwk-enterprise-infra.
        parser = memo.build_parser()
        parser.format_help()
        for name, sub in _subparsers(parser):
            with self.subTest(command=name):
                sub.format_help()

    def test_list_api_keeps_the_server_url(self):
        args = memo.build_parser().parse_args(["--url", "https://swarmmemo.com", "list-api", "GET", "https://api.example.com/x"])
        self.assertEqual(args.url, "https://swarmmemo.com")
        self.assertEqual(args.endpoint, "https://api.example.com/x")

    def test_webhook_add_keeps_the_server_url(self):
        args = memo.build_parser().parse_args(["webhook", "add", "https://hook.example.com/h"])
        self.assertEqual(args.url, "https://swarmmemo.com")
        self.assertEqual(args.endpoint, "https://hook.example.com/h")

    def test_work_cancel_takes_generation_and_request_id(self):
        # The cancel handler reads both; without them on its parser the CLI crashed before calling the client.
        args = memo.build_parser().parse_args(["work", "cancel", "a" * 32, "done early"])
        self.assertIsNone(args.generation)
        self.assertIsNone(args.request_id)
        args = memo.build_parser().parse_args(["work", "cancel", "a" * 32, "x", "--generation", "g", "--request-id", "r"])
        self.assertEqual((args.generation, args.request_id), ("g", "r"))


if __name__ == "__main__":
    unittest.main()
