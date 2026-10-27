# Website, shadcn/ui and GitHub Pages

> Status: Current guide for the shadcn/ui website, its mandatory lint gate, the Vite build and command-only Pages deployment.

Canonical site: <https://opencrude.github.io/crude/>

The website source lives in [`site/`](../../site) and is a React 19 + Vite 7
project. It uses **Tailwind CSS v4** and **shadcn/ui** components, with the
**Geist Variable** sans and **Geist Mono** fonts self-hosted from
`@fontsource-variable`. The built artifact is `site/dist`; GitHub Pages serves
exactly that reviewed directory as a project site under `/crude/`.

## Why GitHub Pages

GitHub Pages remains the right host for this project:

- The repository is public, so Pages is free for the `opencrude` organization.
- It provides HTTPS, a stable project URL, custom-domain support and the same
  repository permissions already used for releases.
- The site is a static build. Pages needs no server, database or runtime beyond
  the browser, and the deploy workflow uploads only `site/dist`.
- Deployment is `workflow_dispatch` only, matching the repository rule that
  Actions never start from a push, pull request, tag or schedule.

Alternatives are reasonable only when their extra capability is needed:
Cloudflare Pages offers edge functions and custom headers; Netlify and Vercel
offer form handling and preview deploys; a separate website repository adds
another source of truth. None of those is required for a landing page whose
content already lives next to the product.

## Stack and design rules

| Piece | Choice |
|---|---|
| UI | React 19 + shadcn/ui (`new-york` style) |
| Build | Vite 7 + TypeScript 5.9 |
| Styling | Tailwind CSS v4 with shadcn theme tokens |
| Primitives | Radix UI (`radix-ui` package) |
| Fonts | `Geist Variable` for UI, `Geist Mono Variable` for code |
| Icons | Lucide React, plus the small inline GitHub mark |
| Lint | `@shadcn/lint` in ESLint 9, all six core rules at `error` |

Rules for changes:

- Compose pages from the shared components in `site/src/components/ui/`. Do not
  rebuild a Button, Card, Tabs or Accordion look with a one-off `<div>`.
- Use theme tokens (`bg-background`, `text-muted-foreground`, `border-border`,
  `text-primary`, `text-brand`, `text-success`, `text-warning`) instead of raw
  colors. The dark palette mirrors the product theme: `#0d1117` background,
  `#f0883e` primary, `#a5d6ff` brand.
- Use component variants and sizes for appearance. Do not restyle a component
  through `className`; app code may use layout classes such as `mt-8` or
  `w-full`. Add a variant to the component when a new treatment is genuinely
  reusable.
- Do not add arbitrary values such as `p-[13px]` in app code. Use the spacing,
  radius and type scales; the component directory is exempt only for structural
  values that the components own.
- Keep the site static and self-contained. Do not add external scripts, fonts,
  analytics or cookies without a deliberate privacy and performance decision.
- Internal URLs are relative. Never add a root-relative `/...` link: a project
  site is served under `/crude/`, and a root URL escapes the site.
- Use the product's real captures. Do not invent a UI screenshot or present a
  fixture conversation as live model output.
- Keep the beta warning and explicit installer consent visible.
- Preserve `lang`, one `h1` per page, image `alt` text, skip links, keyboard
  focus and reduced-motion handling.

## Mandatory shadcn lint

`@shadcn/lint` is the design-system gate for this site. It is enabled in
`site/eslint.config.js` and its policy is in `site/design-system.lint.json`:

```json
{
  "rules": {
    "shadcn/no-restyle": ["error", { "allow": ["layout"] }],
    "shadcn/no-raw-colors": "error",
    "shadcn/no-arbitrary-values": ["error", { "allow": ["layout"] }],
    "shadcn/no-inline-styles": "error",
    "shadcn/no-unknown-classes": "error",
    "shadcn/require-static-classes": "error"
  }
}
```

The only overrides are inside `site/src/components/ui/**`, where components own
their own appearance and may need structural values such as focus rings. Those
overrides disable `no-restyle`, `no-arbitrary-values` and
`require-static-classes`; `no-raw-colors` and `no-inline-styles` stay enabled.
Do not disable a rule in app code to get a change through. Fix the component
usage, add a variant, or document a genuinely necessary exception next to the
code.

Every website change must run:

```bash
npm --prefix site run lint
npm --prefix site run build
python3 scripts/test_site.py -v
```

`make site-check` runs all three from a clean dependency install.

## Site layout

| Path | Purpose |
|---|---|
| `site/package.json`, `site/package-lock.json` | Pinned website dependencies and `lint`/`build` scripts |
| `site/vite.config.ts` | React + Tailwind v4 plugins, `@` alias, relative base, `dist` output |
| `site/components.json` | shadcn discovery: `new-york`, `src/index.css`, `@/components/ui` |
| `site/design-system.lint.json` | The shadcn rule policy and component-directory override |
| `site/eslint.config.js` | ESLint 9 + TypeScript + `@shadcn/lint` |
| `site/src/App.tsx` | The landing page composition and content |
| `site/src/components/ui/` | Button, Card, Badge, Tabs, Accordion and Separator |
| `site/src/index.css` | Tailwind import, dark shadcn tokens, Geist font stack, grid utility |
| `site/src/main.tsx` | React entry point and self-hosted Geist imports |
| `site/public/` | Product captures, Open Graph image, favicon, robots, sitemap, 404 redirect |
| `site/dist/` | Generated build; never committed and never edited by hand |

## Local preview and checks

```bash
npm --prefix site ci
make site-serve          # vite dev server
# open the printed local URL
```

Run the complete website acceptance check:

```bash
make site-check
```

`site-check` installs from the lockfile, runs ESLint with `@shadcn/lint`, runs
the TypeScript + Vite production build, then runs
`scripts/test_site.py`. The test validates the built artifact for metadata,
canonical/Open Graph URLs, local asset resolution, CSS font references,
sitemap/robots agreement, the 404 redirect, the shadcn lint policy, Geist font
wiring, shadcn component imports, installer URLs, payload size and the absence
of the old repository name.

For visual acceptance, use a real browser at desktop and constrained widths.
The repository capture workflow (`scripts/tuishot.sh`) is for the TUI product,
not the website; use the browser directly for pages.

## Deploy

Enable Pages once for the repository with **Build and deployment → Source:
GitHub Actions**. Then deploy deliberately:

```bash
gh workflow run pages.yml
gh run watch
```

The workflow installs from `site/package-lock.json`, runs the mandatory lint,
builds `site/dist`, validates the built site, uploads only that directory and
deploys with the official Pages actions pinned by commit. It never runs on a
push. The `github-pages` environment records the deployment URL.

If Pages is not enabled, the deploy step fails closed; enabling Pages is a
repository setting, not a workflow side effect.

## Custom domain

A custom domain can be added later without rebuilding the site:

1. Add a `CNAME` file to `site/public/` containing exactly the domain (for
   example `crude.dev`). Vite copies it into `dist`.
2. In DNS, point a subdomain at `opencrude.github.io` with a `CNAME` record, or
   use the GitHub-provided `A`/`AAAA` records for an apex domain.
3. Set the custom domain in the repository's Pages settings and enable HTTPS.
4. Update the canonical URL, Open Graph/Twitter URLs, `robots.txt`, `sitemap.xml`
   and the `CANONICAL` constant in `scripts/test_site.py` in the same change.

Do not add a `CNAME` before the domain is actually configured: Pages would
serve the project site at the wrong canonical origin.

## Limits

GitHub Pages is static hosting. It does not provide server-side code, private
pages, custom response headers, form processing or authentication. The soft
limits for a public Pages site are 1 GiB of published content and 100 GiB of
bandwidth per month; the current build is well below both. Release downloads
remain on GitHub Releases, not on Pages. Unknown paths reach a small 404 page
that redirects to the canonical site.
