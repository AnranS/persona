#!/usr/bin/env python3
"""Reuse a GitHub Release's existing build identity and verified bytes during a rerun."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import tempfile


class ResumeError(ValueError):
    pass


def release_assets(repository, tag, gh):
    data = json.loads(subprocess.check_output(
        [gh, "release", "view", tag, "--repo", repository, "--json", "assets"], text=True, timeout=60))
    assets = data.get("assets") if isinstance(data, dict) else None
    if not isinstance(assets, list):
        raise ResumeError("GitHub returned no Release asset list")
    for item in assets:
        if not isinstance(item, dict) or not isinstance(item.get("name"), str) \
                or not isinstance(item.get("digest"), str) or not re.fullmatch(r"sha256:[0-9a-f]{64}", item["digest"]):
            raise ResumeError("GitHub returned an asset without a SHA-256 digest")
    return assets


def download(repository, tag, item, gh):
    name = item["name"]
    with tempfile.TemporaryDirectory(prefix="release-resume-download-") as directory:
        subprocess.run([gh, "release", "download", tag, "--repo", repository,
                        "--pattern", name, "--dir", directory], check=True, timeout=300)
        data = (Path(directory) / name).read_bytes()
    if hashlib.sha256(data).hexdigest() != item["digest"].removeprefix("sha256:"):
        raise ResumeError("downloaded Release asset digest differs: " + name)
    return data


def prepare(repository, tag, commit, resume, gh):
    assets = release_assets(repository, tag, gh)
    descriptor = next((item for item in assets if item["name"] == "latest.json"), None)
    if descriptor is None:
        descriptor = next((item for item in assets if item["name"] == "ci-latest.json"), None)
    if descriptor is not None:
        manifest = json.loads(download(repository, tag, descriptor, gh))
        if manifest.get("commit") != commit:
            raise ResumeError("existing Release manifest is for another source commit")
        build = manifest.get("build")
        if type(build) is not int or not re.fullmatch(r"[0-9]{12}", str(build)):
            raise ResumeError("existing Release manifest has no valid build number")
        has_manifest = True
    else:
        build = subprocess.check_output(
            ["git", "show", "-s", "--format=%cd", "--date=format:%Y%m%d%H%M", commit], text=True).strip()
        if not re.fullmatch(r"[0-9]{12}", build):
            raise ResumeError("cannot derive a release build number from the tag commit")
        build = int(build)
        has_manifest = False
    resume.parent.mkdir(parents=True, exist_ok=True)
    resume.write_text(json.dumps({"build": build, "has_manifest": has_manifest}) + "\n")
    resume.chmod(0o600)
    return build, has_manifest


def restore(repository, tag, directory, gh):
    directory = Path(directory)
    for item in release_assets(repository, tag, gh):
        name = item["name"]
        if name.endswith(".exe.gz"):
            continue  # Native Windows verification stays with the hub publisher's exact verified bytes.
        if not (name.endswith(".gz") or name in {"install.sh", "SHA256SUMS", "VERSION", "latest.json", "ci-latest.json"}):
            continue
        data = download(repository, tag, item, gh)
        target = directory / name if name == "install.sh" else directory / "bin" / name
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("prepare", "restore"))
    parser.add_argument("--repository", required=True)
    parser.add_argument("--tag", required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--directory", type=Path)
    parser.add_argument("--resume", type=Path, required=True)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--gh", default="gh")
    args = parser.parse_args()
    if args.action == "prepare":
        build, has_manifest = prepare(args.repository, args.tag, args.commit, args.resume, args.gh)
        if args.output:
            args.output.write_text("number=%d\nmanifest=%s\n" % (build, "true" if has_manifest else "false"))
    else:
        if args.directory is None:
            parser.error("restore requires --directory")
        restore(args.repository, args.tag, args.directory, args.gh)


if __name__ == "__main__":
    main()
