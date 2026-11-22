#!/usr/bin/env python3
"""Fail when a first-party Markdown link points at a missing file or heading.

Checks inline links, images and reference definitions in AGENTS.md,
README.md, docs/ and the third_party/*/CRUDE.md provenance notes, the same
links the Markdown language server (Marksman) reports. A fragment must match a
heading slug (GitHub rules: lowercase, punctuation removed, spaces to hyphens,
-1, -2 for repeats) or an explicit <a id="..."> anchor. Links inside code
spans and fenced blocks are ignored.

Usage: scripts/md_links.py   (run by scripts/docs_check.sh)
"""

import collections
import os
import re
import subprocess
import sys

FENCE = re.compile(r"^(```|~~~).*?^\1", re.M | re.S)
INLINE = re.compile(r"\]\(<?([^)\s>]+)>?(?:\s+\"[^\"]*\")?\)")
REFDEF = re.compile(r"^\s*\[[^\]]+\]:\s*<?(\S+?)>?(?:\s|$)")
HEADING = re.compile(r"^#{1,6}\s+(.+?)\s*#*\s*$", re.M)
ANCHOR = re.compile(r"<a\s+(?:id|name)=\"([^\"]+)\"")
SCHEME = re.compile(r"^[a-z][a-z0-9+.-]*:", re.I)


def first_party(path):
    if path.startswith("third_party/"):
        return path.endswith("/CRUDE.md")
    return path in ("AGENTS.md", "README.md") or path.startswith("docs/")


def slug(text):
    text = re.sub(r"<[^>]+>", "", text.strip().lower())
    text = re.sub(r"\[([^\]]*)\]\([^)]*\)", r"\1", text)
    text = re.sub(r"[^\w\- ]", "", text)
    return text.replace(" ", "-")


_anchors = {}


def anchors(path):
    if path not in _anchors:
        text = open(path, encoding="utf-8", errors="replace").read()
        seen = collections.Counter()
        found = set(ANCHOR.findall(text))
        for heading in HEADING.findall(FENCE.sub("", text)):
            base = slug(heading)
            found.add(base if seen[base] == 0 else f"{base}-{seen[base]}")
            seen[base] += 1
        _anchors[path] = found
    return _anchors[path]


def main():
    files = subprocess.check_output(
        ["git", "ls-files", "--cached", "--others", "--exclude-standard", "*.md"], text=True).split()
    problems = []
    for doc in sorted(f for f in files if first_party(f) and os.path.exists(f)):
        text = open(doc, encoding="utf-8", errors="replace").read()
        body = FENCE.sub(lambda m: "\n" * m.group(0).count("\n"), text)
        for number, line in enumerate(body.split("\n"), 1):
            line = re.sub(r"`[^`\n]*`", "", line)
            targets = INLINE.findall(line)
            ref = REFDEF.match(line)
            if ref:
                targets.append(ref.group(1))
            for target in targets:
                if SCHEME.match(target):
                    continue
                path, _, fragment = target.partition("#")
                resolved = os.path.normpath(os.path.join(os.path.dirname(doc), path)) if path else doc
                if not os.path.exists(resolved):
                    problems.append(f"{doc}:{number}: link to a missing file: {target}")
                elif fragment and resolved.endswith(".md") and fragment not in anchors(resolved):
                    problems.append(f"{doc}:{number}: link to a missing heading: {target}")
    for problem in problems:
        print("FAIL:", problem)
    if problems:
        print(f"{len(problems)} broken Markdown link(s). Fix the path or the heading, or add <a id=\"...\"></a>.")
        return 1
    print("OK: Markdown links and headings resolve")
    return 0


if __name__ == "__main__":
    sys.exit(main())
