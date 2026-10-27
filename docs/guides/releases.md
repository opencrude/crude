# Releases and installation

> Status: Current guide for installation and release maintenance.

[Download the latest release](https://github.com/opencrude/crude/releases/latest).

## Supported packages

| Package | Build host | End-user requirements |
|---|---|---|
| Linux amd64 | Native Linux, cgo + pinned baseline-CPU libghostty-vt | x86-64, glibc 2.35+ policy baseline, Bash, curl, GNU tar and coreutils; inspect each local binary's actual symbol requirements |
| Windows amd64 | Native Windows, cgo + Zig | Windows x64 with ConPTY, PowerShell 5.1+; only available when included and verified in that release |

No macOS, ARM64, 32-bit or Alpine/musl release artifacts are advertised. The
embedded terminal requires cgo; a stub-only build is not a release. See
[native dependency bootstrap](../../deps/README.md) and
[terminal contracts](../reference/in-app-terminals.md).

The single build tool is `scripts/release.py`: native `build --version <tag>`
requires a clean committed tree, checks protocol-source freshness without
regeneration, requires a baseline-CPU stamped native Ghostty library and cgo, injects
the tag/full commit/UTC commit date, executes `--version`, collects Go dependency
notices with go-licenses v2.0.1, then packages the executable, README and licenses.
Ghostty, its native dependencies, Go, vendored Pathwalk and Codex notices are
included explicitly. Original Crude code is Apache-2.0; upstream licenses remain
intact. `LICENSE-MIT` is not a dual-license offer for Crude.

Archive names are crude_&lt;tag&gt;_linux_amd64.tar.gz and
crude_&lt;tag&gt;_windows_amd64.zip, with root crude/crude.exe. The `combine` command
rejects missing/extra/corrupted platform artifacts and writes one SHA256SUMS.
Checksums detect corruption; fetching both over GitHub HTTPS does not provide an
independent signature or protect against a compromised release publisher.

### Protocol identities and reproducible source cohort

Run `go run ./internal/wiregen` explicitly after production-source edits and commit
the reviewed codecs/schema/cohort output before release.
`go run ./internal/wiregen -check` is nonmutating; release builds, Make build/test/
benchmark targets and `scripts/build.sh` invoke it. Direct `go build` never
implicitly regenerates or proves freshness.

Admission compares exact `Hello.Schema` and the full 64-hex
`buildinfo.ProtocolBuild` source cohort. That cohort covers production platform/tag
alternatives, local forks, embedded assets and native pins; canonical native cgo
builds of the same source may have different executable IDs. Broad LF attributes
keep source bytes stable across checkouts. This is source equality, not user
authentication, release signing or native-platform acceptance. Local `BuildID` /
`ExecutableID` identify staging/status images, not protocol cohorts. See the
[protocol reference](../reference/core-client-protocol.md#wire-format-and-bounded-delivery).

## Install and update

Download install.sh or install.ps1 from a **published release**, review it, then
run it. No compiler, source checkout or elevation is required. Rerun to update.
Default latest excludes GitHub prereleases; an explicit version can select one.

| Option | Linux | Windows |
|---|---|---|
| Acknowledge active-development risks | `--accept-beta-risk` | `-AcceptBetaRisk` |
| Exact version | `--version v0.1.0-beta.1` | `-Version v0.1.0-beta.1` |
| Install directory | `--install-dir /absolute/path` or CRUDE_INSTALL_DIR | `-InstallDir <path>` |
| Leave PATH alone | `--no-path` | `-NoPath` |
| Public fork | `--repository owner/repo` | `-Repository owner/repo` |

Consent is required on every installation/update. Without the flag, only a
genuinely interactive terminal can accept by typing exactly yes. A piped or
noninteractive invocation fails closed. The warning explicitly covers bugs,
agent mistakes, work loss, independent backups and committing important work.

- Linux installs under ~/.local/bin by default, retains redistribution notices
  alongside it, and adds one idempotent PATH line to .profile and the active
  Bash/Zsh rc file. Fish receives manual fish_add_path guidance. Use --no-path
  for managed shell setups. A verified executable is published by same-directory
  atomic rename. A stale installer lock requires manual inspection/removal.
- Windows defaults to LOCALAPPDATA/Crude/bin, retains notices, holds a per-directory
  lock, atomically replaces individual files (binary last), and rolls back failed
  updates. Raw user PATH placeholders/value kind and the process PATH are
  preserved; machine PATH is never changed. Interrupted/failed rollback retains
  transaction preimages/backups and refuses another update until inspected.
- Neither installer stops a core, deletes app data, installs fonts or changes
  terminal settings. An update changes the executable, not a core already running
  from a staged image. Finish agent work and close/stop the old core yourself
  before opening the new cohort if a mismatch is reported. Automatic handover
  refuses even idle retained workspaces and uses only the verified owner helper
  with an exact generation fence; it never kills the core. All staged images
  currently remain retained because unlink is not lifecycle proof, so disk use
  may grow across updates. Never use force as an automatic updater step.
- The opinionated setup advice recommends Kitty/Alacritty or Windows Terminal,
  truecolor and JetBrainsMono Nerd Font **NF**, not NFM. Installed-font discovery
  is advisory: the actual terminal font cannot be proven from an installer.
  Missing/unverifiable prerequisites produce warnings, official install pointers
  and an invitation to ask Crude itself to help, with explicit approval for changes.

`scripts/test_install.py` and `scripts/test_install.ps1` exercise real archives,
checksums and file operations while intercepting transport. Native release jobs
also install/repeat the actual platform package. Portable PowerShell fixtures do
not establish native Windows registry, executable-lock or ConPTY acceptance.

## Release modes

Releases are deliberately two different products:

| Mode | What runs | When to use it |
|---|---|---|
| `verified` (default) | Source validation, the complete `verify.yml` chain (Linux lint/design/docs/performance/build/tests/race/fuzz and native Windows tests), native builds, installer checks, exact inventory, draft creation | Any release you intend to publish as a normal build |
| `instant` | Source validation, native builds, installer checks, exact inventory, draft creation | A time-sensitive build when the full chain is impractical; it deliberately skips `verify.yml` |

`instant` is not a weaker verification label on the same evidence: it has no
full-chain evidence at all. It still validates the immutable tag/commit,
performs native Linux/Windows packaging smoke checks, verifies the release
inventory and checksums, and creates a **draft** with an explicit `Instant
release` warning in the body and an `(instant)` title suffix. Never publish an
instant draft as a normal release without inspecting it and recording what was
skipped.

Dispatch either mode with the tag selected as the ref so the workflow code and
the packaged source are the same immutable revision:

```bash
# ordinary release: full verification chain
gh workflow run release.yml --ref v0.2.0 -f tag=v0.2.0 -f mode=verified

# time-sensitive release: deliberately skips the full verification chain
gh workflow run release.yml --ref v0.2.0 -f tag=v0.2.0 -f mode=instant
```

The selected tag must already contain this workflow revision. The workflow
still creates only a draft; publishing remains a separate deliberate
`gh release edit --draft=false` after the assets and notes have been inspected.

## Manual local publication

Build the native platform on the local machine from a clean, committed checkout:

```bash
. scripts/libghostty_env.sh
go run ./internal/wiregen -check
go test ./... -count=1 -timeout=180s
golangci-lint run ./...
make design-audit docs-check bench-gate
python scripts/test_race.py
python scripts/release.py build --version v0.1.1 --output /absolute/release-dir
```

Install the pinned notice collector (`go-licenses` v2.0.1) before packaging.
For a Linux-only release, copy that archive's `.sha256` file to `SHA256SUMS`, then
run `scripts/test_install.py` with `--release-dir /absolute/release-dir` and
`--release-version v0.1.1`. The existing `combine` command remains the strict
two-platform matrix checker; do not weaken it or invent a Windows artifact to
publish one platform.

Inspect archive paths, notices, source/build identity and dynamic requirements.
Publish only platforms actually built and validated natively. Keep release notes
brief and product-focused; retain detailed validation evidence separately.
Audit source/history, push the exact source and immutable tag, create a draft
with explicit assets, download it and verify hashes, then publish it deliberately
using `gh release edit`. Local artifacts are not a release until the uploaded
assets and exact tag are verified. Never overwrite a prior release or move a tag.

## Command-only GitHub workflows

Repository Actions is command-only. No workflow starts on a push, pull request,
tag creation or schedule. Every source workflow is either an explicit
`workflow_dispatch` entry point or the reusable `workflow_call` chain used by
those entry points. The release and website workflows cannot run by accident.

GitHub also lists a generated **Dependency Graph** workflow outside
`.github/workflows`. It is a GitHub-managed supply-chain workflow, not a source
workflow. If the repository must have no automatic workflow at all, disable
Dependency graph under Settings → Advanced Security; the source workflows here
remain dispatch-only either way.

Use the exact entry points:

```bash
gh workflow run tui.yml
gh workflow run native-preflight.yml
gh workflow run public-install-smoke.yml -f tag=v0.2.0 -f commit=<full-sha>
gh workflow run release.yml --ref v0.2.0 -f tag=v0.2.0 -f mode=verified
gh workflow run release.yml --ref v0.2.0 -f tag=v0.2.0 -f mode=instant
gh workflow run pages.yml
```

When deliberately dispatched, `.github/workflows/tui.yml` calls
`.github/workflows/verify.yml`.
Linux runs the website lint/build, full-repo lint, design/docs/performance
gates, build, full tests and full race tests. Windows builds/tests with native
cgo and runs installer fixtures on both Windows PowerShell 5.1 and PowerShell 7.
Actions are pinned by SHA; Zig downloads have pinned SHA-256 checksums. No
pull_request_target or inherited secrets.

Native Zig/Ghostty dependencies are cached by platform, architecture, exact pins
and bootstrap/action source. Bootstrap still validates the installed pin and
baseline CPU stamp. Its cache is saved before tests, including on runs where a
later test fails. Linux explicitly installs ripgrep and ShellCheck; the design
audit fails immediately if ripgrep is unavailable. Windows runs a targeted
path/PTY/persistence preflight before the full suite, limits package-level test
parallelism to two to avoid saturating durable database I/O, and keeps temporary
databases under an owner-only directory on the runner work disk. The Linux race
suite also limits package parallelism to two to avoid concurrent instrumented
SQLite suites exhausting their existing time budgets. No durability or test gate
is disabled or raised.

`native-preflight.yml` is a manual Linux/Windows regression check for release
repairs. Run it on a repair branch before rerunning the complete verification;
it exercises the failing path/watch/control/recovery cases three times and both
native Windows installer shells. It does not replace the full publication gate.

`.github/workflows/release.yml` accepts an existing v-prefixed tag through
workflow_dispatch with **that tag selected as the ref** and an explicit
`verified` or `instant` mode. Tag and build commit must match, and be reachable
from master. In `verified` mode it runs the same `verify.yml` chain; in
`instant` mode that job is skipped and the missing evidence is recorded on the
draft. Both modes build/test native archives and create a **draft only** with
both packages, checksums and the installer scripts. Only the final draft job has
contents:write. It never overwrites an existing release or silently promotes a
prerelease.

`public-install-smoke.yml` performs post-publication checks on standard Linux and
Windows runners, including Windows PowerShell 5.1 and PowerShell 7. It downloads
the real latest installers anonymously, installs with the beta-risk opt-in, and
repeats with the explicit tag. It verifies source identity, retained application
data, redistribution notices and user-local PATH. No mocked transport, checkout,
credential or product timeout change is used.

`.github/workflows/pages.yml` deploys the reviewed shadcn/ui site to GitHub
Pages. It is `workflow_dispatch` only: a push never publishes the website. The
job installs from `site/package-lock.json`, runs the mandatory
`@shadcn/lint` gate, builds `site/dist`, validates the built artifact, uploads
only that directory and uses the official Pages actions pinned by commit. The
site uses relative URLs so it works at the project URL without a custom domain.
See the [website guide](website.md).

Before any public launch:

1. All contributing agents must have finished, their exact final reports reviewed,
   and no outstanding process/capture/editor changes left to arrive.
2. Review the complete diff, run the [validation gates](validation.md), and verify
   every platform included in the release. Do not skip failures or raise
   test/performance limits.
3. Audit tracked content **and Git history** for secrets/private data. Review
   findings; never make a genuine credential public or rewrite history without
   deliberate approval. `.gitleaksignore` contains only five exact, reviewed
   historical false-positive fingerprints (Go field identifiers and a key chord).
4. Keep temporary downloads, private profiles, databases, caches, build outputs and
   old repair backups out of publication. Do not purge other agents' recovery
   artifacts to make the source tree look tidy. Source archives contain tracked
   files, not ignored build directories.
5. Commit and push every reviewed change. Check git status is empty and HEAD is
   exactly origin/master. Retain successful native checks for every included
   platform.
6. Choose the version and release notes. Tag only that reviewed commit; inspect
   the resulting draft, archive notices, installer risk/setup output and native
   install/update results. Verify no newer local/remote changes appeared.
7. Publish the reviewed draft only after those gates are met. Source visibility
   and release publication are separate actions; never relax a gate to get a
   binary out. A successful earlier release does not attest that a later source
   commit or package has passed those gates.

Optional hosted builds use standard ubuntu-22.04/windows-2022 runners only.
Do not enable paid runners or overages.

Useful human-operated commands (substitute the reviewed tag/commit):

```bash
gh run list --workflow tui.yml
gh run view <run-id> --log-failed
gh workflow run release.yml --ref <tag> -f tag=<tag>
gh release view <tag>
# After local native validation, exact asset/tag review and draft download checks:
gh release edit <tag> --draft=false
```

## README screenshots

The images in the root assets directory are real Linux product captures of
Crude's own source through `scripts/tuishot.sh`, using an isolated profile. The
conversation is explicitly marked as a local fixture. No private configuration,
real API key, customer project or fake benchmark is shown. Wide and constrained
captures were inspected; every dedicated capture window was closed. See
[visual validation](validation.md#visual-capture) for the canonical workflow.
