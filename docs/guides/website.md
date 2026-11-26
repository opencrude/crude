# Website and GitHub Pages

> Status: current guide. It describes `site/`, the local preview and the
> command-only Pages deployment of <https://opencrude.github.io/>.

The website is hand-written HTML, CSS and JavaScript in `site/`. There is no
build step, no package manager and no generated artifact: GitHub Pages serves
`site/` as it is.

## Why GitHub Pages

Pages serves a directory from a repository without a server. The organization
site repository `opencrude/opencrude.github.io` serves the root URL, so the
canonical address has no path suffix. Everything the page needs must therefore
work from the site root with relative references only.

## Site contents

| Path | Purpose |
|---|---|
| `site/index.html` | The page: hero, chapters, features, memory, install, FAQ |
| `site/assets/crude.css` | All styling, including the media queries and reduced-motion rules |
| `site/assets/crude.js` | Scroll engine, in-page links, copy buttons, glitch scheduler |
| `site/assets/fonts/` | Self-hosted WOFF2 files, preloaded by `index.html` |
| `site/og.png` | Social card, 1200x630 |
| `site/apple-touch-icon.png`, `site/favicon.svg` | Icons |
| `site/robots.txt`, `site/sitemap.xml`, `site/site.webmanifest` | Crawler and install metadata |
| `site/index.md`, `site/llms.txt`, `site/llms-full.txt` | Plain-text copies for search engines and language models |
| `site/linux.sh`, `site/windows.ps1` | The install commands: each runs the installer of the latest release |

Rules that keep the page self-contained and honest:

- Reference local files with relative URLs. A root-relative URL breaks the
  page when it is served under a path.
- Load no third-party script, stylesheet or font.
- Show no version number, no model or vendor name, and no product screenshot.
  The art is drawn live in HTML and CSS.
- Keep the navigation order Install, Features, Memory, Docs, then GitHub.
- Keep the install commands `curl -fsSL https://opencrude.github.io/linux.sh | sh`
  and `irm https://opencrude.github.io/windows.ps1 | iex` in every copy of the
  page, the README and the agent prompt.

## Scrolling and keys

Every visual state of the story is a function of the scroll position. Each
chapter must finish before the screen fades out above the palette; the gap
above `.more` keeps the last chapter's final step on screen.

- Down, Up, Page Down, Page Up, Space and Shift+Space move to the next or
  previous settled state: the welcome screen, its caption, each step of each
  chapter, then the palette. `crude.js` derives these stops from the same data
  that drives the scenes (`STEPS`, the `.r` reveal offsets, the typed text and
  the core chapter's window timing), so editing a scene moves its stops too.
  Below the palette the keys scroll normally.
- `1`–`4` follow the header links, `/` opens the palette, `c` copies the install
  command while it is on screen. In the palette, Up and Down choose a feature
  and Esc clears the filter; a second Esc leaves the field.
- The install rows take no focus after they fade out, so Tab never lands on
  something the reader cannot see.

## Install commands

The page shows one piped command per system:

```bash
curl -fsSL https://opencrude.github.io/linux.sh | sh
```

```powershell
irm https://opencrude.github.io/windows.ps1 | iex
```

Pages cannot send an HTTP redirect, so `site/linux.sh` and `site/windows.ps1`
do the redirect themselves. Each reads the installer of the latest published
release completely from
`https://github.com/opencrude/crude/releases/latest/download/`, then runs it.
Nothing is written to the current folder. A new release changes what these
commands install without a Pages deploy; only a change to the two files
themselves needs one.

- `linux.sh` is POSIX `sh`. A piped script has the download as its input, so it
  gives the installer the terminal (`/dev/tty`) and the questions still work.
  Without a terminal the installer runs unattended. Options follow
  `sh -s --`, for example `curl -fsSL https://opencrude.github.io/linux.sh | sh -s -- --yes`.
- `windows.ps1` is ASCII, because Windows PowerShell 5.1 reads the served file as
  Latin-1. It tries the download three times and decodes the installer as UTF-8.
  A failed piped installation stops with its error and leaves the PowerShell
  window open.

The deploy also publishes the same two files as `install.sh` and `install.ps1`,
because released builds of Crude still tell users to update with
`https://opencrude.github.io/install.sh` and `install.ps1`.

`scripts/test_install.py` and `scripts/test_install.ps1` run both files against
the real installers with only the HTTP client replaced. After a deploy that
changes them, check the served copies:

```bash
for f in linux.sh windows.ps1; do curl -fsSL "https://opencrude.github.io/$f" | diff "site/$f" - && echo "$f same"; done
```

### uBlock Origin

uBlock Origin's default filters contain a rule against ClickFix attacks. It
blocks every page-initiated clipboard write whose text starts with
`irm … | iex` and shows "Beware, uBlock Origin blocked a potential ClickFix
attack" instead of copying. The rule excludes `github.com` but not
`opencrude.github.io`, so the Windows Copy button can trigger it. The Linux
command does not match the rule.

Do not copy through another clipboard API or reshape the command to slip past
the rule. The fix is an exception for `opencrude.github.io` in
[uBlockOrigin/uAssets](https://github.com/uBlockOrigin/uAssets/issues), as
brew.sh, astral.sh and deno.com have. Until a user's filter lists include it,
the person can select the command and copy it with Ctrl+C.

## Local preview

```bash
make site-serve          # serves site/ at http://127.0.0.1:8080/
```

## Deploy

Pages deploys only from an explicit dispatch. A push to any repository never
publishes the website.

```bash
gh workflow run pages.yml -R opencrude/opencrude.github.io        # master
gh workflow run pages.yml -R opencrude/opencrude.github.io -f ref=<full 40-character sha>
gh run watch -R opencrude/opencrude.github.io
```

The job checks out `opencrude/crude` at the requested ref, copies `site/` into
the published directory with `linux.sh` and `windows.ps1` also named
`install.sh` and `install.ps1`, uploads that directory and uses the official
Pages actions pinned by commit.

After a deploy, open <https://opencrude.github.io/> and confirm the page, the
install commands and the social card.

## Custom domain

1. Create the `CNAME` file in `site/` with the domain on one line.
2. In DNS, point the domain at `opencrude.github.io` with a `CNAME` record, or
   use the four `A` records that GitHub Pages documents for an apex domain.
3. Set the domain in the Pages settings of `opencrude/opencrude.github.io`.
4. Change the canonical URL, the sitemap, `robots.txt` and the social card to
   the new address, then deploy again.

## Limits

Pages serves static files and adds no headers. Any redirect or rewrite the
page needs must happen in the page itself or in a `404.html` that you add
deliberately.
