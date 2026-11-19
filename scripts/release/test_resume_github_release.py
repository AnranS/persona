import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import resume_github_release as resume


class ResumeGitHubReleaseTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="release-resume-tests-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.commit = "a" * 40
        self.repository = "example/project"
        self.tag = "v1.2.3"

    def asset(self, name, body):
        return {"name": name, "digest": "sha256:" + hashlib.sha256(body).hexdigest(), "body": body}

    def run_with(self, assets):
        def output(command, **kwargs):
            if command[:3] == ["git", "show", "-s"]:
                return "202611110749\n"
            return json.dumps({"assets": [{key: value for key, value in item.items() if key != "body"}
                                         for item in assets]})

        def run(command, **kwargs):
            name = command[command.index("--pattern") + 1]
            body = next(item["body"] for item in assets if item["name"] == name)
            directory = Path(command[command.index("--dir") + 1])
            directory.joinpath(name).write_bytes(body)
            return subprocess.CompletedProcess(command, 0)

        return output, run

    def test_existing_manifest_build_and_bytes_are_reused(self):
        manifest = json.dumps({"commit": self.commit, "build": 202611110800}).encode()
        archive = b"previous signed archive"
        assets = [self.asset("ci-latest.json", manifest), self.asset("personad-darwin-arm64.gz", archive)]
        output, run = self.run_with(assets)
        resume_path = self.root / "resume.json"
        with patch.object(resume.subprocess, "check_output", side_effect=output), \
                patch.object(resume.subprocess, "run", side_effect=run):
            build, has_manifest = resume.prepare(self.repository, self.tag, self.commit, resume_path, "gh")
            resume.restore(self.repository, self.tag, self.root, "gh")
        self.assertEqual(build, 202611110800)
        self.assertTrue(has_manifest)
        self.assertEqual((self.root / "bin/personad-darwin-arm64.gz").read_bytes(), archive)
        self.assertEqual((self.root / "bin/ci-latest.json").read_bytes(), manifest)

    def test_missing_manifest_uses_commit_time_and_bad_digest_is_rejected(self):
        assets = [self.asset("personad-darwin-arm64.gz", b"archive")]
        output, run = self.run_with(assets)
        resume_path = self.root / "resume.json"
        with patch.object(resume.subprocess, "check_output", side_effect=output), \
                patch.object(resume.subprocess, "run", side_effect=run):
            build, has_manifest = resume.prepare(self.repository, self.tag, self.commit, resume_path, "gh")
        self.assertEqual(build, 202611110749)
        self.assertFalse(has_manifest)
        assets[0]["digest"] = "sha256:" + "0" * 64
        with patch.object(resume.subprocess, "check_output", side_effect=output), \
                patch.object(resume.subprocess, "run", side_effect=run), \
                self.assertRaisesRegex(resume.ResumeError, "digest"):
            resume.restore(self.repository, self.tag, self.root, "gh")

    def test_windows_archives_are_not_downloaded_into_ci(self):
        archive = b"verified elsewhere"
        assets = [self.asset("persona-link-windows-amd64.exe.gz", archive)]
        output, run = self.run_with(assets)
        with patch.object(resume.subprocess, "check_output", side_effect=output), \
                patch.object(resume.subprocess, "run", side_effect=run) as download:
            resume.restore(self.repository, self.tag, self.root, "gh")
        download.assert_not_called()
        self.assertFalse((self.root / "bin/persona-link-windows-amd64.exe.gz").exists())


if __name__ == "__main__":
    unittest.main()
