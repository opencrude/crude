#!/usr/bin/env python3
# /// script
# requires-python = ">=3.11"
# dependencies = ["fonttools==4.63.0"]
# ///
# fontTools is this build's only dependency. uv installs the pinned version
# from the metadata above; the type checker's stdlib-only environment for
# scripts/ does not have it.
# pyright: reportMissingImports=false
"""Build Crude RTL, the right-to-left fallback font of Crude's terminal window.

Crude arranges Arabic, Persian and Hebrew text itself and sends the terminal
one glyph per cell; Arabic letters arrive as Unicode presentation forms
(docs/reference/bidi.md). A terminal draws them well only with a font whose
glyphs fill the cell, join at the cell edges and are never combined again by
the font: a ligature over two cells turns the visual pair alef-final,
lam-initial of "ال" into "لا".

Crude RTL takes the Arabic glyphs unchanged from DejaVu Sans Mono 2.37, whose
cell width matches JetBrains Mono (0.6 em). It adds the Hebrew letters and
points, and the rarer Arabic letters that DejaVu Sans Mono lacks, from DejaVu
Sans 2.37 fitted to the same cell. It removes glyph substitution and keeps
mark positioning. No glyph is redrawn.

Usage:
    uv run scripts/build_rtl_font.py [--source dejavu-fonts-ttf-2.37.zip]

Without --source the pinned archive is downloaded and checked. Writes
assets/fonts/CrudeRTL-Regular.ttf, CrudeRTL-Bold.ttf and LICENSE-DejaVu.
"""

import argparse
import hashlib
import io
import sys
import urllib.request
import zipfile
from pathlib import Path

from fontTools import subset
from fontTools.pens.recordingPen import DecomposingRecordingPen
from fontTools.pens.transformPen import TransformPen
from fontTools.pens.ttGlyphPen import TTGlyphPen
from fontTools.ttLib import TTFont

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / "assets" / "fonts"
SOURCE_URL = "https://github.com/dejavu-fonts/dejavu-fonts/releases/download/version_2_37/dejavu-fonts-ttf-2.37.zip"
SOURCE_SHA256 = "7576310b219e04159d35ff61dd4a4ec4cdba4f35c00e002a136f00e96a908b0a"
SOURCE_DIR = "dejavu-fonts-ttf-2.37/"
DESCRIPTION = "Build Crude RTL, the right-to-left fallback font of Crude's terminal window."
FAMILY = "Crude RTL"
VERSION = "Version 2.37; Crude RTL 1"

# Right-to-left scripts the font carries.
HEBREW = [(0x0590, 0x05FF), (0xFB1D, 0xFB4F)]
ARABIC = [(0x0600, 0x06FF), (0x0750, 0x077F), (0x08A0, 0x08FF), (0xFB50, 0xFDFF), (0xFE70, 0xFEFF)]
# Joining Arabic glyphs scale to the full cell so their joins keep touching
# its edges; other glyphs keep their proportions and are centered.
JOINING_FORMS = [(0xFB50, 0xFDFF), (0xFE70, 0xFEFF)]

STYLES = {
    "Regular": ("DejaVuSansMono.ttf", "DejaVuSans.ttf", 400),
    "Bold": ("DejaVuSansMono-Bold.ttf", "DejaVuSans-Bold.ttf", 700),
}


def within(code, ranges):
    return any(lo <= code <= hi for lo, hi in ranges)


def read_source(path):
    if path:
        data = Path(path).read_bytes()
    else:
        with urllib.request.urlopen(SOURCE_URL, timeout=120) as response:
            data = response.read()
    digest = hashlib.sha256(data).hexdigest()
    if digest != SOURCE_SHA256:
        sys.exit(f"source archive sha256 {digest} does not match the pinned {SOURCE_SHA256}")
    return zipfile.ZipFile(io.BytesIO(data))


def load(archive, name):
    return TTFont(io.BytesIO(archive.read(SOURCE_DIR + "ttf/" + name)), recalcTimestamp=False)


def fitted_glyph(source, name, cell, joining):
    """Glyph `name` of `source`, decomposed and fitted to a cell `cell` wide."""
    glyphs = source.getGlyphSet()
    advance = source["hmtx"][name][0]
    recording = DecomposingRecordingPen(glyphs)
    glyphs[name].draw(recording)
    if advance == 0:
        # A combining mark keeps its position relative to the base it follows.
        scale, offset = 1.0, 0.0
    elif joining:
        scale, offset = cell / advance, 0.0
    else:
        scale = min(1.0, cell / advance)
        offset = (cell - advance * scale) / 2
    pen = TTGlyphPen(None)
    recording.replay(TransformPen(pen, (scale, 0, 0, 1, offset, 0)))
    return pen.glyph(), (cell if advance else 0)


def build(archive, style):
    mono_name, sans_name, weight = STYLES[style]
    mono = load(archive, mono_name)
    sans = load(archive, sans_name)
    cell = mono["hmtx"][mono.getBestCmap()[ord("M")]][0]

    keep = sorted(c for c in mono.getBestCmap() if within(c, ARABIC))
    options = subset.Options()
    options.layout_features = ["mark", "mkmk"]  # positioning only
    options.drop_tables += ["GSUB", "kern"]
    options.name_IDs = ["*"]
    options.name_languages = ["*"]
    options.glyph_names = True
    options.notdef_outline = True
    options.recalc_timestamp = False
    subsetter = subset.Subsetter(options)
    subsetter.populate(unicodes=keep)
    subsetter.subset(mono)

    cmap = mono.getBestCmap()
    added = []
    for code, name in sorted(sans.getBestCmap().items()):
        if code in cmap or not (within(code, HEBREW) or within(code, ARABIC)):
            continue
        glyph, advance = fitted_glyph(sans, name, cell, within(code, JOINING_FORMS))
        target = name if name not in mono["glyf"].glyphs else name + ".sans"
        mono["glyf"][target] = glyph
        glyph.recalcBounds(mono["glyf"])
        mono["hmtx"][target] = (advance, getattr(glyph, "xMin", 0))
        for table in mono["cmap"].tables:
            if table.isUnicode():
                table.cmap[code] = target
        added.append(target)
    order = mono.getGlyphOrder()
    mono.setGlyphOrder(order + [g for g in added if g not in order])

    names = mono["name"]
    for name_id in (16, 17, 21, 22):
        names.removeNames(nameID=name_id)
    for name_id, value in {
        1: FAMILY,
        2: style,
        3: f"{FAMILY} {style}; {VERSION}",
        4: FAMILY if style == "Regular" else f"{FAMILY} {style}",
        5: VERSION,
        6: f"CrudeRTL-{style}",
    }.items():
        names.setName(value, name_id, 3, 1, 0x409)
        names.setName(value, name_id, 1, 0, 0)
    mono["OS/2"].usWeightClass = weight
    mono["post"].isFixedPitch = 1
    mono["OS/2"].panose.bProportion = 9  # monospaced
    return mono, len(keep), len(added)


def main():
    parser = argparse.ArgumentParser(description=DESCRIPTION)
    parser.add_argument("--source", help="local copy of the pinned DejaVu 2.37 TTF archive")
    args = parser.parse_args()
    archive = read_source(args.source)
    OUT.mkdir(parents=True, exist_ok=True)
    for style in STYLES:
        font, kept, added = build(archive, style)
        if "GSUB" in font:
            sys.exit("glyph substitution survived the build")
        path = OUT / f"CrudeRTL-{style}.ttf"
        font.save(path)
        print(f"{path.relative_to(ROOT)}: {kept} glyphs from DejaVu Sans Mono, {added} fitted from DejaVu Sans")
    (OUT / "LICENSE-DejaVu").write_bytes(archive.read(SOURCE_DIR + "LICENSE"))


if __name__ == "__main__":
    main()
