#!/usr/bin/env python3
"""Validate the built shadcn/ui website and its design-system lint policy."""

from html.parser import HTMLParser
import json
import os
from pathlib import Path
import re
import struct
import sys
import unittest
from urllib.parse import unquote, urlsplit
import xml.etree.ElementTree as ElementTree


ROOT = Path(__file__).resolve().parent.parent
SITE = ROOT / "site"
DIST = Path(os.environ.get("SITE_DIST", SITE / "dist")).resolve()
CANONICAL = "https://opencrude.github.io/"
REPOSITORY = "https://github.com/opencrude/crude"
# Real product captures shown in the hero and the Tour, one per scene.
TOUR_SCENES = ("workspace", "agents", "review", "skills", "palette", "settings")
SHADCN_RULES = (
    "shadcn/no-restyle",
    "shadcn/no-raw-colors",
    "shadcn/no-arbitrary-values",
    "shadcn/no-inline-styles",
    "shadcn/no-unknown-classes",
    "shadcn/require-static-classes",
)
class Document(HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.tags = []
        self.ids = set()
        self.links = []
        self.metas = {}
        self.scripts = []
        self.styles = []

    def handle_starttag(self, tag, attrs):
        values = {}
        for name, value in attrs:
            values[name] = value if value is not None else ""
        self.tags.append(tag)
        if "id" in values:
            self.ids.add(values["id"])
        if tag in {"a", "link"}:
            self.links.append(values)
        if tag == "meta":
            key = values.get("property") or values.get("name")
            if key:
                self.metas[key] = values.get("content", "")
        if tag == "script":
            self.scripts.append(values)
        if tag == "style":
            self.styles.append(values)


def parse(path):
    parser = Document()
    parser.feed(path.read_text(encoding="utf-8"))
    parser.close()
    return parser


def png_dimensions(path):
    with path.open("rb") as stream:
        header = stream.read(24)
    if header[:8] != b"\x89PNG\r\n\x1a\n" or header[12:16] != b"IHDR":
        raise AssertionError(f"not a PNG: {path}")
    return struct.unpack(">II", header[16:24])


def webp_dimensions(path):
    with path.open("rb") as stream:
        header = stream.read(30)
    if header[:4] != b"RIFF" or header[8:12] != b"WEBP":
        raise AssertionError(f"not a WebP: {path}")
    chunk = header[12:16]
    if chunk == b"VP8L":
        bits = int.from_bytes(header[21:25], "little")
        return (bits & 0x3FFF) + 1, ((bits >> 14) & 0x3FFF) + 1
    if chunk == b"VP8X":
        return (
            int.from_bytes(header[24:27], "little") + 1,
            int.from_bytes(header[27:30], "little") + 1,
        )
    if chunk == b"VP8 ":
        return (
            int.from_bytes(header[26:28], "little") & 0x3FFF,
            int.from_bytes(header[28:30], "little") & 0x3FFF,
        )
    raise AssertionError(f"unknown WebP chunk {chunk!r}: {path}")


def local_target(reference, base=DIST):
    reference = reference.strip()
    if not reference or reference.startswith("#"):
        return None
    parsed = urlsplit(reference)
    if parsed.scheme or parsed.netloc:
        return None
    if reference.startswith("/"):
        raise AssertionError(f"root-relative URL breaks project Pages: {reference}")
    path = unquote(parsed.path)
    if not path:
        return None
    candidate = (base / path).resolve()
    try:
        candidate.relative_to(base.resolve())
    except ValueError as error:
        raise AssertionError(f"site reference escapes the site directory: {reference}") from error
    return candidate


def rule_severity(value):
    if isinstance(value, str):
        return value
    if isinstance(value, list) and value:
        return value[0]
    return None


class SiteTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if not (DIST / "index.html").is_file():
            raise unittest.SkipTest(
                f"{DIST} has no built site; run `npm --prefix site run build` first"
            )
        cls.index = parse(DIST / "index.html")
        cls.package = json.loads((SITE / "package.json").read_text(encoding="utf-8"))
        cls.components = json.loads((SITE / "components.json").read_text(encoding="utf-8"))
        cls.policy = json.loads((SITE / "design-system.lint.json").read_text(encoding="utf-8"))
        cls.app = (SITE / "src/App.tsx").read_text(encoding="utf-8")
        cls.css = (SITE / "src/index.css").read_text(encoding="utf-8")
        cls.main = (SITE / "src/main.tsx").read_text(encoding="utf-8")

    def test_built_site_is_complete(self):
        for name in (
            "index.html", "404.html", "favicon.svg", "apple-touch-icon.png",
            "robots.txt", "sitemap.xml", ".nojekyll", "assets/og.png",
            *(f"assets/{scene}.webp" for scene in TOUR_SCENES),
        ):
            with self.subTest(name=name):
                self.assertTrue((DIST / name).is_file(), f"missing built site file: {name}")
        self.assertTrue(list((DIST / "assets").glob("index-*.js")), "missing built JavaScript")
        self.assertTrue(list((DIST / "assets").glob("index-*.css")), "missing built stylesheet")

    def test_document_metadata(self):
        self.assertIn("html", self.index.tags)
        self.assertIn("body", self.index.tags)
        self.assertTrue(self.index.metas.get("description"))
        self.assertTrue(self.index.metas.get("viewport"))
        self.assertEqual(self.index.metas.get("og:url"), CANONICAL)
        self.assertEqual(self.index.metas.get("og:image"), CANONICAL + "assets/og.png")
        self.assertEqual(self.index.metas.get("twitter:card"), "summary_large_image")
        canonical = [
            link.get("href") for link in self.index.links
            if link.get("rel") == "canonical"
        ]
        self.assertEqual(canonical, [CANONICAL])
        self.assertEqual(png_dimensions(DIST / "assets/og.png"), (1200, 630))
        html = (DIST / "index.html").read_text(encoding="utf-8")
        self.assertIn('lang="en"', html)
        self.assertIn('class="dark"', html)

    def test_local_assets_and_stylesheets_resolve(self):
        references = [link.get("href", "") for link in self.index.links]
        references += [script.get("src", "") for script in self.index.scripts if script.get("src")]
        for reference in references:
            target = local_target(reference)
            if target is None:
                continue
            with self.subTest(reference=reference):
                self.assertTrue(target.is_file(), f"missing local target: {reference}")

        for stylesheet in (DIST / "assets").glob("index-*.css"):
            text = stylesheet.read_text(encoding="utf-8")
            for asset in re.findall(r"url\(([^)]+)\)", text):
                asset = asset.strip("'\"")
                if asset.startswith(("data:", "http:", "https:", "#")):
                    continue
                with self.subTest(stylesheet=stylesheet.name, asset=asset):
                    self.assertTrue(
                        (stylesheet.parent / asset).resolve().is_file(),
                        f"missing CSS asset: {asset}",
                    )

    def test_social_and_crawler_files_agree(self):
        sitemap = ElementTree.parse(DIST / "sitemap.xml")
        namespace = {"sitemap": "http://www.sitemaps.org/schemas/sitemap/0.9"}
        locations = [node.text for node in sitemap.findall("sitemap:url/sitemap:loc", namespace)]
        self.assertEqual(locations, [CANONICAL])
        robots = (DIST / "robots.txt").read_text(encoding="utf-8")
        self.assertIn(f"Sitemap: {CANONICAL}sitemap.xml", robots)
        self.assertIn("Allow: /", robots)

    def test_404_routes_home_without_indexing(self):
        html = (DIST / "404.html").read_text(encoding="utf-8")
        self.assertIn('name="robots" content="noindex"', html)
        self.assertIn('http-equiv="refresh"', html)
        self.assertIn(CANONICAL, html)
        self.assertIn("Back to Crude", html)

    def test_shadcn_lint_is_installed_and_mandatory(self):
        self.assertEqual(self.package["scripts"]["lint"], "eslint .")
        self.assertEqual(self.package["scripts"]["build"], "tsc --noEmit && vite build")
        self.assertIn("@shadcn/lint", self.package["devDependencies"])

        eslint = (SITE / "eslint.config.js").read_text(encoding="utf-8")
        self.assertIn('from "@shadcn/lint"', eslint)
        self.assertIn("plugins:", eslint)
        self.assertIn("...designSystem.rules", eslint)
        self.assertIn("...designSystem.overrides", eslint)

        for rule in SHADCN_RULES:
            with self.subTest(rule=rule):
                self.assertEqual(
                    rule_severity(self.policy["rules"].get(rule)),
                    "error",
                    f"{rule} must be enabled as an error",
                )
        off_rules = {
            rule
            for override in self.policy["overrides"]
            for rule, severity in override.get("rules", {}).items()
            if severity == "off"
        }
        self.assertEqual(
            off_rules,
            {
                "shadcn/no-restyle",
                "shadcn/no-arbitrary-values",
                "shadcn/require-static-classes",
            },
            "only component-owned appearance and structural values may be exempt",
        )

    def test_shadcn_discovery_points_at_the_site_theme(self):
        self.assertEqual(self.components["tailwind"]["css"], "src/index.css")
        self.assertEqual(self.components["aliases"]["ui"], "@/components/ui")
        self.assertEqual(self.components["aliases"]["utils"], "@/lib/utils")
        self.assertIn('@import "tailwindcss";', self.css)
        self.assertIn('--font-sans: "Geist Variable"', self.css)
        self.assertIn('--font-mono: "Geist Mono Variable"', self.css)
        self.assertIn("@theme inline", self.css)
        self.assertIn("--color-primary: var(--primary)", self.css)
        self.assertIn('import "@fontsource-variable/geist";', self.main)
        self.assertIn('import "@fontsource-variable/geist-mono";', self.main)

    def test_source_uses_shadcn_components_and_real_product_content(self):
        for component in ("button", "card", "badge", "tabs", "accordion"):
            with self.subTest(component=component):
                self.assertIn(f'from "@/components/ui/{component}"', self.app)
        self.assertEqual(self.app.count("<h1"), 1, "the page needs exactly one h1")
        for anchor in ("tour", "why", "capabilities", "install", "faq"):
            with self.subTest(anchor=anchor):
                self.assertIn(f'id="{anchor}"', self.app)
        for scene in TOUR_SCENES:
            with self.subTest(scene=scene):
                self.assertIn(f'image: "{scene}.webp"', self.app)
        self.assertIn("Skip to content", self.app)
        self.assertIn("alt={alt}", self.app)
        # Declared image geometry must match the captures, or the layout shifts.
        width = int(re.search(r"SHOT_WIDTH = (\d+);", self.app).group(1))
        height = int(re.search(r"SHOT_HEIGHT = (\d+);", self.app).group(1))
        for scene in TOUR_SCENES:
            with self.subTest(capture=scene):
                self.assertEqual(webp_dimensions(DIST / f"assets/{scene}.webp"), (width, height))

    def test_install_commands_use_the_canonical_repository(self):
        self.assertIn(REPOSITORY, self.app)
        self.assertIn("/latest/download/install.sh", self.app)
        self.assertIn("/latest/download/install.ps1", self.app)
        self.assertIn("--accept-beta-risk", self.app)
        self.assertIn("-AcceptBetaRisk", self.app)
        self.assertIn("Linux x86-64", self.app)
        self.assertIn("Windows x64", self.app)

    def test_site_does_not_reference_the_old_repository(self):
        for base in (SITE / "src", SITE / "public", DIST):
            for path in base.rglob("*"):
                if path.is_file() and path.suffix in {".html", ".css", ".js", ".txt", ".xml", ".svg", ".tsx", ".ts"}:
                    with self.subTest(file=str(path.relative_to(ROOT))):
                        self.assertNotIn("uoosef403", path.read_text(encoding="utf-8"))

    def test_no_external_scripts_or_stylesheets(self):
        for script in self.index.scripts:
            if script.get("src"):
                self.assertFalse(urlsplit(script["src"]).scheme, f"external script: {script['src']}")
        for link in self.index.links:
            if link.get("rel") in {"stylesheet", "icon"}:
                self.assertFalse(
                    urlsplit(link.get("href", "")).scheme,
                    f"external {link['rel']}",
                )
        self.assertFalse(self.index.styles, "inline style elements are not allowed")

    def test_site_payload_and_artifact_hygiene(self):
        total = 0
        for path in DIST.rglob("*"):
            if path.is_file():
                total += path.stat().st_size
                self.assertFalse(path.name.startswith(".env"), f"private file in site: {path}")
                self.assertNotIn("node_modules", path.parts, "built site contains node_modules")
        self.assertLess(total, 5 * 1024 * 1024, "built site should stay below 5 MiB")

    def test_source_has_no_placeholder_copy(self):
        for path in (SITE / "src").rglob("*"):
            if path.is_file() and path.suffix in {".tsx", ".ts", ".css"}:
                text = path.read_text(encoding="utf-8")
                self.assertNotRegex(text, r"(?i)\b(lorem ipsum|todo|coming soon)\b")


if __name__ == "__main__":
    unittest.main(verbosity=2 if "-v" in sys.argv else 1)
