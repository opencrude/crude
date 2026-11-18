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

Rules that keep the page self-contained and honest:

- Reference local files with relative URLs. A root-relative URL breaks the
  page when it is served under a path.
- Load no third-party script, stylesheet or font.
- Show no version number, no model or vendor name, and no product screenshot.
  The art is drawn live in HTML and CSS.
- Keep the navigation order Install, Features, Memory, Docs, then GitHub.
- Keep the install commands pointing at the canonical installer URLs.

## Install commands

The page offers `https://opencrude.github.io/install.sh` and
`https://opencrude.github.io/install.ps1`. The deploy workflow copies the two
installers from the repository root into the published directory, so those
addresses always serve the current installers and the page never duplicates
them.

The published copies are snapshots. Changing either installer does not change
them until someone dispatches `pages.yml`, so a fix is invisible to the
documented install command until then. After an installer change, dispatch the
deploy and check the served file:

```bash
gh workflow run pages.yml -R opencrude/opencrude.github.io --ref main
curl -fsSL https://opencrude.github.io/install.ps1 | tr -d '\r' | diff <(tr -d '\r' < install.ps1) - && echo same
```

## Local preview

```bash
make site-serve          # serves site/ at http://127.0.0.1:8080/
```

## Deploy

Pages deploys only from an explicit dispatch. A push to any repository never
publishes the website.

```bash
gh workflow run pages.yml -R opencrude/opencrude.github.io        # master
gh workflow run pages.yml -R opencrude/opencrude.github.io -f ref=<sha>
gh run watch -R opencrude/opencrude.github.io
```

The job checks out `opencrude/crude` at the requested ref, copies `site/` and
the two installers into the published directory, uploads that directory and
uses the official Pages actions pinned by commit.

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
