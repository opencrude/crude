#!/usr/bin/env python3
"""Run the full Go suite with bounded, exhaustive large-package groups."""

import argparse
import re
import subprocess


LARGE_PACKAGES = {"crude/session": "./session", "crude/tui/viewmodel": "./tui/viewmodel"}
STRESS_TESTS = {"TestUploadBlackholeRecoveryDurableOwners"}


def partition(names, count=4):
    """Every top-level case runs once; large upload stress has its own budget."""
    names = sorted(set(names))
    ordinary = [name for name in names if name not in STRESS_TESTS]
    groups = [ordinary[index::count] for index in range(count)]
    groups += [[name] for name in names if name in STRESS_TESTS]
    groups = [group for group in groups if group]
    assert sorted(name for group in groups for name in group) == names
    return groups


def pattern(names):
    return "^(" + "|".join(re.escape(name) for name in names) + ")$"


def run(args):
    print("+ " + " ".join(args), flush=True)
    subprocess.run(args, check=True)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--race", action="store_true", help="enable the Go race detector")
    args = parser.parse_args(argv)
    packages = subprocess.check_output(["go", "list", "./..."], text=True).splitlines()
    rest = [package for package in packages if package not in LARGE_PACKAGES]
    flags = ["go", "test", "-p", "2", "-count=1", "-timeout=300s" if args.race else "-timeout=180s"]
    if args.race:
        flags.append("-race")
    run(flags + rest)
    for package in LARGE_PACKAGES.values():
        listing = subprocess.check_output(["go", "test", package, "-list", "."], text=True)
        names = [line for line in listing.splitlines() if re.fullmatch(r"(?:Test|Example|Fuzz)\w*", line)]
        if not names:
            raise RuntimeError("no test cases discovered for " + package)
        groups = partition(names)
        for index, group in enumerate(groups, 1):
            print(f"{'Race' if args.race else 'Go'} {package}: group {index}/{len(groups)}, {len(group)} cases", flush=True)
            run(flags + [package, "-run", pattern(group)])
    print("FULL_RACE_SUITE_PASSED" if args.race else "FULL_GO_SUITE_PASSED", flush=True)


if __name__ == "__main__":
    main()
