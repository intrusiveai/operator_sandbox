"""Bounded build commands and the validation-once reproducibility workflow."""
import contextlib
import io
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import build_host_release as builder


class BuildCommandTest(unittest.TestCase):
    def test_capture_and_failure_diagnostics(self):
        log = io.StringIO()
        with contextlib.redirect_stderr(log):
            result = builder.run([sys.executable, "-c", "print('machine-result')"], Path.cwd())
            self.assertEqual(result, "machine-result")
            with self.assertRaises(subprocess.CalledProcessError):
                builder.run([sys.executable, "-c", "print('compiler failure'); raise SystemExit(7)"], Path.cwd())
        self.assertIn("START (limit 120s)", log.getvalue())
        self.assertIn("OK after", log.getvalue())
        self.assertIn("FAILED after", log.getvalue())
        self.assertIn("compiler failure", log.getvalue())

    def test_timeout_kills_descendants_and_preserves_stage(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            marker = root / "orphan-finished"
            launched = root / "launched"
            child = "import time; from pathlib import Path; time.sleep(2); Path(" + repr(str(marker)) + ").write_text('bad')"
            parent = "import subprocess,sys,time; from pathlib import Path; subprocess.Popen([sys.executable,'-c'," + repr(child) + "]); Path(" + repr(str(launched)) + ").touch(); time.sleep(30)"
            log = io.StringIO()
            started = time.monotonic()
            with contextlib.redirect_stderr(log), self.assertRaisesRegex(RuntimeError, "command exceeded 1s"):
                builder.run([sys.executable, "-c", parent], root, timeout=1)
            self.assertLess(time.monotonic()-started, 5)
            self.assertTrue(launched.exists())
            time.sleep(1.3)
            self.assertFalse(marker.exists(), "timed-out compiler left its child running")
            self.assertIn("TIMEOUT after", log.getvalue())

    def test_noncaptured_output_is_visible_before_command_finishes(self):
        # Separate process: exercise real inherited stdout, not a mocked writer.
        code = "import sys; from pathlib import Path; sys.path.insert(0,'scripts'); import build_host_release as b; b.run([sys.executable,'-c',\"import time; print('progress-now',flush=True); time.sleep(1)\"],Path.cwd(),capture=False)"
        process = subprocess.Popen([sys.executable, "-c", code], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        try:
            self.assertEqual(process.stdout.readline().strip(), "progress-now")
            self.assertIsNone(process.poll())
            _, log = process.communicate(timeout=5)
            self.assertEqual(process.returncode, 0, log)
        finally:
            if process.poll() is None:
                process.kill()
                process.wait()
            process.stdout.close()
            process.stderr.close()


class ReproducibilityTest(unittest.TestCase):
    def test_validation_once_and_full_inventory_comparison(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            calls = []
            def build(source, output, version, platforms, python, *, validate_contract=True):
                calls.append(validate_contract)
                output.mkdir()
                (output / "payload").write_bytes(b"same release bytes")
            with patch.object(builder, "build", side_effect=build):
                builder.build_candidates(root, root / "first", "0.1.4", ["darwin/arm64"], Path(sys.executable), root / "second")
            self.assertEqual(calls, [True, False])
            for change in ("bytes", "inventory", "mode"):
                with self.subTest(change=change):
                    target = root / "second/payload"
                    target.write_bytes(b"different" if change == "bytes" else b"same release bytes")
                    if change == "inventory": (root / "second/extra").touch()
                    if change == "mode":
                        (root / "second/extra").unlink()
                        target.chmod(0o700)
                    with self.assertRaisesRegex(ValueError, "release builds differ"):
                        builder.compare_outputs(root / "first", root / "second")

    def test_first_failure_never_rebuilds_or_compares(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            with patch.object(builder, "build", side_effect=RuntimeError("validation failed")) as build, patch.object(builder, "compare_outputs") as compare:
                with self.assertRaisesRegex(RuntimeError, "validation failed"):
                    builder.build_candidates(root, root / "first", "0.1.4", [], Path(sys.executable), root / "second")
                self.assertEqual(build.call_count, 1)
                compare.assert_not_called()

    def test_standalone_build_keeps_validation_and_overlapping_outputs_fail(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            with patch.object(builder, "build") as build:
                builder.build_candidates(root, root / "first", "0.1.4", [], Path(sys.executable))
                self.assertNotIn("validate_contract", build.call_args.kwargs)
                build.reset_mock()
                for second in (root / "first", root / "first/nested", root):
                    with self.assertRaises(ValueError):
                        builder.build_candidates(root, root / "first", "0.1.4", [], Path(sys.executable), second)
                build.assert_not_called()

if __name__ == "__main__": unittest.main()
