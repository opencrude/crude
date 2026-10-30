#!/usr/bin/env python3
"""Build/package native release binaries; combine exact matrix artifacts safely."""

import argparse
from datetime import datetime, timezone
import hashlib
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import zipfile


ROOT = Path(__file__).resolve().parent.parent
VERSION = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?")
TARGETS = {"linux": "tar.gz", "windows": "zip"}


def run(*args):
    return subprocess.check_output(args, cwd=ROOT, text=True).strip()


def validate_version(value):
    if not VERSION.fullmatch(value):
        raise ValueError("version must be vMAJOR.MINOR.PATCH (optionally -prerelease)")
    if "-" in value and any(part.isdigit() and len(part) > 1 and part.startswith("0") for part in value.split("-", 1)[1].split(".")):
        raise ValueError("numeric prerelease identifiers cannot have leading zeroes")
    return value


def asset_name(version, target):
    return f"crude_{validate_version(version)}_{target}_amd64.{TARGETS[target]}"


def sha256(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def copy_syntax_notice(stage):
    """Embedded query data is original-module content ignored by go-licenses."""
    destination = stage / "licenses/SYNTAX-QUERIES-NOTICE"
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(ROOT / "syntax/queries/NOTICE", destination)


def package(stage, output, version, target):
    """Package only the staged executable and its redistribution material."""
    name = asset_name(version, target)
    destination = output / name
    if destination.exists():
        raise ValueError(f"refusing to overwrite existing artifact: {destination}")
    executable = "crude.exe" if target == "windows" else "crude"
    entries = [stage / executable, stage / "LICENSE-MIT", stage / "LICENSE-APACHE", stage / "README.md", stage / "licenses"]
    if not all(path.exists() for path in entries):
        raise ValueError("release is missing its binary or redistribution notices")
    if not (stage / "licenses/SYNTAX-QUERIES-NOTICE").is_file():
        raise ValueError("release is missing its embedded syntax query notice")
    if target == "linux":
        with tarfile.open(destination, "w:gz") as archive:
            for path in entries:
                archive.add(path, arcname=path.name)
    else:
        with zipfile.ZipFile(destination, "w", compression=zipfile.ZIP_DEFLATED) as archive:
            for path in entries:
                if path.is_file():
                    archive.write(path, path.name)
                else:
                    for member in sorted(path.rglob("*")):
                        archive.write(member, member.relative_to(stage).as_posix())
    (output / f"{name}.sha256").write_text(f"{sha256(destination)}  {name}\n", encoding="ascii")
    return destination


def build(args):
    version = validate_version(args.version)
    target = {"linux": "linux", "win32": "windows"}.get(sys.platform)
    if target is None or platform.machine().lower() not in ("amd64", "x86_64"):
        raise ValueError("release builds require a native Linux/Windows x86-64 runner")
    if run("git", "status", "--porcelain"):
        raise ValueError("release builds require a clean committed checkout (including generated terminfo)")
    # A clean commit can still contain stale generated protocol identities.
    # Check source bytes without changing the release checkout on either host.
    run("go", "run", "./internal/wiregen", "-check")
    goos, goarch, cgo = run("go", "env", "GOOS", "GOARCH", "CGO_ENABLED").splitlines()
    if (goos, goarch, cgo) != (target, "amd64", "1"):
        raise ValueError("source scripts/libghostty_env for this native platform; CGO_ENABLED must be 1")
    pin = re.search(r"^GHOSTTY_COMMIT=([0-9a-f]{40})$", (ROOT / "third_party/libghostty-vt/GHOSTTY_PIN").read_text(), re.MULTILINE).group(1)
    prefix = ROOT / "third_party/libghostty-vt"
    library = "ghostty-vt-static.lib" if target == "windows" else "libghostty-vt.a"
    if (prefix / ".crude-pin").read_text().strip() != pin or (prefix / ".crude-cpu").read_text().strip() != "baseline" or not (prefix / "lib" / library).is_file():
        raise ValueError("bootstrap the pinned native libghostty-vt before building")
    commit = run("git", "rev-parse", "HEAD")
    date = datetime.fromtimestamp(int(run("git", "show", "-s", "--format=%ct", "HEAD")), timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    output = Path(args.output).resolve()
    output.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="crude-release-", dir=os.environ.get("CRUDE_TEMP_DIR")) as temp:
        stage = Path(temp)
        executable = stage / ("crude.exe" if target == "windows" else "crude")
        ldflags = f"-s -w -X crude/buildinfo.Version={version} -X crude/buildinfo.Commit={commit} -X crude/buildinfo.Date={date}"
        subprocess.run(["go", "build", "-trimpath", "-buildvcs=true", "-ldflags", ldflags, "-o", str(executable), "."], cwd=ROOT, check=True)
        expected = f"crude {version} ({commit}; {date}; {target}/amd64)"
        actual = run(str(executable), "--version")
        if actual != expected:
            raise ValueError(f"release smoke failed: {actual!r} != {expected!r}")
        for name in ("LICENSE-MIT", "LICENSE-APACHE", "README.md"):
            shutil.copyfile(ROOT / name, stage / name)
        # Dependency tool is version-pinned by CI; it fails on missing notices
        # rather than silently shipping an incomplete Go dependency bundle.
        subprocess.run(["go-licenses", "save", ".", "--ignore=crude", f"--save_path={stage / 'licenses/go'}"], cwd=ROOT, check=True)
        copy_syntax_notice(stage)
        (stage / "licenses/ghostty").mkdir(parents=True)
        shutil.copyfile(ROOT / "third_party/libghostty-vt/GHOSTTY-LICENSE", stage / "licenses/ghostty/LICENSE")
        shutil.copyfile(ROOT / "third_party/libghostty-vt/NATIVE-NOTICES", stage / "licenses/NATIVE-NOTICES")
        shutil.copyfile(ROOT / "NOTICE", stage / "licenses/CRUDE-NOTICE")
        shutil.copyfile(ROOT / "codexio/NOTICE", stage / "licenses/CODEX-NOTICE")
        shutil.copyfile(ROOT / "third_party/pathwalk/LICENSE", stage / "licenses/PATHWALK-LICENSE")
        shutil.copyfile(ROOT / "third_party/pathwalk/GO-LICENSE", stage / "licenses/PATHWALK-GO-LICENSE")
        shutil.copyfile(Path(run("go", "env", "GOROOT")) / "LICENSE", stage / "licenses/GO-LICENSE")
        (stage / "licenses/BUILD.txt").write_text(f"Crude {version}\nCommit {commit}\nDate {date}\n{run('go', 'version')}\nGhostty {pin}\n", encoding="utf-8")
        destination = package(stage, output, version, target)
        print(f"Built and smoke-tested {destination}")


def combine(args):
    """Reject missing, extra, renamed or corrupt assets before draft upload."""
    directory = Path(args.directory)
    expected = sorted(asset_name(args.version, target) for target in TARGETS)
    actual = sorted(path.name for path in directory.iterdir() if path.name != "SHA256SUMS")
    if actual != sorted(expected + [name + ".sha256" for name in expected]):
        raise ValueError(f"unexpected release inventory: {actual}")
    lines = []
    for name in expected:
        line = f"{sha256(directory / name)}  {name}\n"
        if (directory / (name + ".sha256")).read_text(encoding="ascii") != line:
            raise ValueError(f"artifact checksum mismatch: {name}")
        lines.append(line)
    (directory / "SHA256SUMS").write_text("".join(lines), encoding="ascii")
    print("Verified exact Linux/Windows release inventory and wrote SHA256SUMS")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    native = commands.add_parser("build")
    native.add_argument("--version", required=True)
    native.add_argument("--output", default="dist")
    native.set_defaults(function=build)
    manifest = commands.add_parser("combine")
    manifest.add_argument("--version", required=True)
    manifest.add_argument("--directory", default="dist")
    manifest.set_defaults(function=combine)
    args = parser.parse_args()
    try:
        args.function(args)
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"release: {error}\n")


if __name__ == "__main__":
    main()
