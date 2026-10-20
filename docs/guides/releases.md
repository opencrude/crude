# Releases and installation

> Status: Current guide. The canonical repository is uoosef403/crude. Builds and publication are manual; automatic GitHub Actions is disabled. Release notes state the exact platform inventory and validation limits.

Repository links, installer defaults and the public-install smoke workflow target
`uoosef403/crude`. Repository visibility is a separate explicit user choice, never
a side effect of a build or push. Public installers require a publicly accessible
published release. Source pushes and historical verification do not establish
that a binary release exists.

## Supported packages

| Package | Build host | End-user requirements |
|---|---|---|
| Linux amd64 | Native Linux, cgo + pinned baseline-CPU libghostty-vt | x86-64, glibc 2.35+ policy baseline, Bash, curl, GNU tar and coreutils; inspect each local binary's actual symbol requirements |
| Windows amd64 | Native Windows, cgo + Zig | Windows x64 with ConPTY, PowerShell 5.1+; only available when included and verified in that release |

No macOS, ARM64, 32-bit or Alpine/musl release artifacts are advertised. The
embedded terminal requires cgo; a stub-only build is not a release. See
[native dependency bootstrap](../../deps/README.md) and
[terminal contracts](../reference/in-app-terminals.md).

The current local publication is Linux-only. A prior full Windows suite failed
after its terminal preflight passed, and its exact failure log was unavailable.
No Windows package is published until that separate native acceptance is resolved.

The single build tool is `scripts/release.py`: native `build --version <tag>`
requires a clean committed tree, baseline-CPU stamped native Ghostty library and cgo, injects
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
  before opening the new build if a build mismatch is reported. Never use force
  as an automatic updater step.
- The opinionated setup advice recommends Kitty/Alacritty or Windows Terminal,
  truecolor and JetBrainsMono Nerd Font **NF**, not NFM. Installed-font discovery
  is advisory: the actual terminal font cannot be proven from an installer.
  Missing/unverifiable prerequisites produce warnings, official install pointers
  and an invitation to ask Crude itself to help, with explicit approval for changes.

`scripts/test_install.py` and `scripts/test_install.ps1` exercise real archives,
checksums and file operations while intercepting transport. Native release jobs
also install/repeat the actual platform package. Portable PowerShell fixtures do
not establish native Windows registry, executable-lock or ConPTY acceptance.

## Manual local publication

Build the native platform on the local machine from a clean, committed checkout:

```bash
. scripts/libghostty_env.sh
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
Publish only platforms actually built and validated natively. State missing
platforms, known unrelated failures and any unrun acceptance in release notes.
Audit source/history, push the exact source and immutable tag, create a draft
with explicit assets, download it and verify hashes, then publish it deliberately
using `gh release edit`. Local artifacts are not a release until the uploaded
assets and exact tag are verified. Never overwrite a prior release or move a tag.

## Optional manual GitHub workflows

Repository Actions is disabled, including generated hosted workflows. No jobs
start on pushes, PRs or tag creation. The source workflows are manual-only so
re-enabling Actions does not restore automatic CI/CD. Re-enable it only for an
explicitly requested dispatch; local publication needs no Actions runs.

When deliberately dispatched, `.github/workflows/tui.yml` calls
`.github/workflows/verify.yml`.
Linux runs lint, design/docs/performance gates, build, full tests and full race
tests. Windows builds/tests with native cgo and runs installer fixtures on both
Windows PowerShell 5.1 and PowerShell 7. Actions are pinned by SHA; Zig downloads
have pinned SHA-256 checksums. No pull_request_target or inherited secrets.

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

Session database initialization creates the complete current schema in one
transaction, with rollback on validation failure. It no longer reparses the
schema through repeated legacy `ALTER TABLE` attempts on every open. Existing
current-format databases retain their schema and content; outdated databases
require backed-up manual alignment, never an automatic upgrade.

`native-preflight.yml` is a manual Linux/Windows regression check for release
repairs. Run it on a repair branch before rerunning the complete verification;
it exercises the failing path/watch/control/recovery cases three times and both
native Windows installer shells. It does not replace the full publication gate.

`.github/workflows/release.yml` accepts an existing v-prefixed tag through
workflow_dispatch with **that tag selected as the ref**. Tag and verification
commit must match, and be reachable from master. It runs the same verification,
builds/tests native archives, and creates a **draft only** with both packages,
checksums and the installer scripts. Only the final draft job has contents:write.
It never overwrites an existing release or silently promotes a prerelease.

The first complete historical native verification was run `39313929845` on the
former hosting destination. Its successful checks apply to that exact commit,
not to untested later source or the current repository's release availability.
Historical tag-push workflows could be suppressed by `[skip ci]`. Current source
has no tag-push trigger at all. A deliberate dispatch still requires the immutable
tag as its ref; do not move the tag, select master instead or misattribute checks.

### Historical v0.1.0 acceptance (2026-10-19)

Crude v0.1.0 was published on the former hosting destination from commit
`984affa35c527db0020637af38e92558878e1f92`. Historical release run `39319066535`
passed all six source, native verification, native package build, and draft jobs.
No release or asset was copied to the current repository by changing its URLs.
The actual Windows ZIP passed installation/reinstallation (29 installer checks),
and the actual Linux archive passed all ten installer checks. Both downloaded
archives match SHA256SUMS and GitHub's recorded digests; their build stamps and
native cgo/amd64 binaries identify the same source commit. Archive paths, notices,
Linux runtime dependencies and Windows DLL imports were inspected before the
reviewed draft was published. An anonymous latest-release lookup then resolved all
six assets. No macOS/ARM64/musl package or publisher-signature claim is implied.

Historical public-install run `39327570783` also passed real anonymous latest
installation and explicit-tag reinstallation
on Linux, Windows PowerShell 5.1 and PowerShell 7. The first PowerShell 5.1 runner
received HTTP 403 from GitHub's release-metadata API before installation; the
unchanged smoke check passed on recheck. No token, release replacement, network
bypass or product timeout change was used to make that check pass.

`public-install-smoke.yml` performs post-publication checks on standard Linux and
Windows runners, including Windows PowerShell 5.1 and PowerShell 7. It downloads
the real latest installers anonymously, installs with the beta-risk opt-in, and
repeats with the explicit tag. It verifies source identity, retained application
data, redistribution notices and user-local PATH. No mocked transport, checkout,
credential or product timeout change is used.

Before any public launch:

1. All contributing agents must have finished, their exact final reports reviewed,
   and no outstanding process/capture/editor changes left to arrive.
2. Review the complete diff, run the [validation gates](validation.md), and verify
   every platform included in the release. Do not skip failures or raise
   test/performance limits. Omit unverified platforms and state that explicitly.
3. Audit tracked content **and Git history** for secrets/private data. Review
   findings; never make a genuine credential public or rewrite history without
   deliberate approval. `.gitleaksignore` contains only five exact, reviewed
   historical false-positive fingerprints (Go field identifiers and a key chord).
4. Keep temporary downloads, private profiles, databases, caches, build outputs and
   old repair backups out of publication. Do not purge other agents' recovery
   artifacts to make the source tree look tidy. Source archives contain tracked
   files, not ignored build directories.
5. Commit and push every reviewed change. Check git status is empty and HEAD is
   exactly origin/master. The audited source may become public at this point;
   that is **not** a binary release or certification. Retain successful local
   native checks for every included platform. Optional hosted checks require an
   explicit dispatch after Actions is deliberately enabled.
6. Choose the version and release notes. Tag only that reviewed commit; inspect
   the resulting draft, archive notices, installer risk/setup output and native
   install/update results. Verify no newer local/remote changes appeared.
7. Publish the reviewed draft only after those gates are met. Source visibility
   and release publication are separate actions; never relax a gate to get a
   binary out. A successful earlier release does not attest that a later source
   commit or package has passed those gates.

### No-payment public CI

[GitHub's billing policy](https://docs.github.com/en/billing/concepts/product-billing/github-actions)
provides free standard GitHub-hosted runner usage for public repositories. Private
repositories have an included quota; reaching it without a payment method blocks
usage. These workflows use only standard ubuntu-22.04/windows-2022 runners, never
paid larger runners, and do not change billing, payment methods or budgets.
Artifact/cache storage has separate allowances; release artifacts are short-lived
(14 days) and no increased storage limit is configured. Do not enable paid
overages to make this project build. GitHub-side/account startup failures still
need explicit resolution; source visibility alone is not proof that CI ran.

Useful human-operated commands (substitute the reviewed tag/commit):

```bash
gh run list --workflow tui.yml
gh run view <run-id> --log-failed
gh workflow run release.yml --ref <tag> -f tag=<tag>
gh release view <tag>
# After source/history audit, finished agents and a clean pushed checkout:
gh repo edit uoosef403/crude --visibility public --accept-visibility-change-consequences
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
