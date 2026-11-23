"""Checks for scripts/test/test-omp.sh."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "test" / "test-omp.sh"
SKIP_LINE = "RESULT skip: omp is not installed"


class OmpSmokeScriptTests(unittest.TestCase):
    def test_shell_syntax(self):
        bash = shutil.which("bash")
        self.assertIsNotNone(bash)
        result = subprocess.run(
            [bash, "-n", str(SCRIPT)], capture_output=True, text=True, timeout=30
        )
        self.assertEqual(0, result.returncode, result.stdout + result.stderr)

    def test_herdr_without_omp_skips_cleanly(self):
        bash = shutil.which("bash")
        self.assertIsNotNone(bash)
        with tempfile.TemporaryDirectory(prefix="omp-smoke-test-") as tmp:
            tools = Path(tmp) / "bin"
            tools.mkdir()
            fake_herdr = tools / "herdr"
            fake_herdr.write_text("#!/bin/sh\nexit 0\n")
            fake_herdr.chmod(0o755)
            env = os.environ.copy()
            # No omp is supplied, and the system directories contain only the basic tools a shell script needs.
            env["PATH"] = os.pathsep.join([str(tools), "/usr/bin", "/bin"])
            result = subprocess.run(
                [bash, str(SCRIPT)], cwd=str(ROOT), env=env,
                capture_output=True, text=True, timeout=30,
            )
        output = result.stdout + result.stderr
        self.assertEqual(0, result.returncode, output)
        lines = output.splitlines()
        self.assertEqual(1, lines.count(SKIP_LINE), output)
        self.assertEqual([SKIP_LINE], [line for line in lines if line.startswith("RESULT ")], output)


if __name__ == "__main__":
    unittest.main()
