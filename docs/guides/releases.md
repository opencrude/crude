# Releases and installation

> Status: Current guide for installation and release maintenance.

[Download the latest release](https://github.com/opencrude/crude/releases/latest).

## Supported packages

| Package | Build host | End-user requirements |
|---|---|---|
| Linux amd64 | Native Linux, cgo + pinned baseline-CPU libghostty-vt | x86-64, glibc 2.35+ policy baseline, Bash, curl, GNU tar and coreutils; inspect each local binary's actual symbol requirements |
| Windows amd64 | Native Windows, cgo + Zig | Windows x64 with ConPTY, PowerShell 5.1+; the installer adds Git Bash for this user when the machine has none; only available when included and verified in that release |

No macOS, ARM64, 32-bit or Alpine/musl release artifacts are advertised. The
embedded terminal requires cgo; a stub-only build is not a release. See
[native dependency bootstrap](../../third_party/libghostty-vt/README.md) and
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
keep known text formats LF and preserve unlisted files byte-for-byte across
checkouts, including embedded assets. This is source equality, not user
authentication, release signing or native-platform acceptance. Local `BuildID` /
`ExecutableID` identify staging/status images, not protocol cohorts. See the
[protocol reference](../reference/core-client-protocol.md#wire-format-and-bounded-delivery).

## Install and update

Download install.sh or install.ps1 from a **published release**, review it, then
run it. No compiler, source checkout or elevation is required. Run the same
command again to update. The latest release is used unless you name a version;
an explicit version can select a prerelease.

| Action | Linux | Windows |
|---|---|---|
| Accept the warning without a prompt | `--yes` | `-Yes` |
| Add the desktop window | `--gui` | `-Gui` |
| Do not add the desktop window | `--no-gui` | `-NoGui` |
| Stop a running Crude without asking | `--stop` | `-Stop` |
| Keep a running Crude open | `--no-stop` | `-NoStop` |
| Exact version | `--version v0.1.0-beta.1` | `-Version v0.1.0-beta.1` |
| Install directory | `--install-dir /absolute/path` or CRUDE_INSTALL_DIR | `-InstallDir <path>` |
| Leave PATH alone | `--no-path` | `-NoPath` |
| Public fork | `--repository owner/repo` | `-Repository owner/repo` |

The installer prints one short line per step: the download, the program, the
stop, the window and the next command. It does not print a licence essay, a
terminal-font lecture or a list of optional tools. The Git shell prints only
when it could not be installed.

Release files come from the GitHub API asset path first and the published
download path second, because a network can carry one and fail the other. Each
source gets three bounded attempts, and the pinned checksum decides what is
accepted.

### The Git shell

Agent commands run in one shell. On Windows, Git Bash starts in about 40 ms and
gives the agent the GNU tools it expects, so the installer makes sure one is
present.

- If the machine already has Git Bash where Crude looks for it, the installer
  uses it and prints nothing.
- If it is absent, the installer unpacks a pinned Git for Windows build into
  `%LOCALAPPDATA%\Crude\git` for this user only. The folder is writable by the
  user, so no elevation, no UAC prompt, no PATH entry and no system change
  happens. It prints nothing.
- The download has three bounded attempts. If all of them fail, the program
  install still completes, and the installer prints one short notice: Crude
  works best with Git Bash, where to get Git for Windows, and that Crude uses
  it the next time it starts. The next run tries again.
- The package is checked against a pinned SHA-256, unpacked beside the old copy,
  started once to prove it is GNU bash, and moved into place in one step. A
  failed check, unpack or start leaves no half-written shell, so Crude never
  finds a broken one.

Crude resolves this copy from its own path, so a user who installs nothing still
gets a working shell. A Git Bash the user installed later wins, because it is
found first.

Consent is required on every installation and update. Without `--yes` an
interactive terminal asks `Install anyway? [y/N]`. A piped or noninteractive
invocation fails closed and states the flag that accepts the warning. The
warning covers bugs, agent mistakes, work lost and the need for backups.

### A running Crude

Crude keeps working in a detached core after a window closes, and that core
runs the installed program. The installer finds only the processes that run
this exact installation: the program itself, which an open window also runs, and
a terminal program that a window folder holds.

- The question comes before the download, so a refusal costs nothing. Without
  an answer it stops and changes nothing: close Crude and run the installer
  again, or pass `--stop`.
- With `--stop` it asks the core to stop, waits ten seconds, and only then asks
  again before it forces the stop. A forced stop can lose text that was never
  written to a file.
- With `--no-stop` it installs the new program and leaves the old core running.
  The new version starts at the next launch.

No force path runs automatically, and no other program is signalled.

### The window

`--gui` adds a window that opens Crude. Once the window is installed, every
later run of the installer updates it together with the program: the profile,
the font, the menu entry and the icon.

Without `--gui` or `--no-gui`, a terminal in a graphical session asks one
question, `Do you want the GUI version as well?`, and the default is yes. A
session without a display does not ask and does not download. An unattended
`--yes` run never starts a window download, and still updates a window that is
already installed.

- Windows opens Crude in Windows Terminal, which Windows 11 provides and Windows
  10 gets from the Microsoft Store. When it is missing, the window step says so
  and names the exact install command, and the program itself is still
  installed. Crude installs no second terminal beside it.
- On Windows the window is one JSON fragment in
  `%LOCALAPPDATA%\Microsoft\Windows Terminal\Fragments\Crude`, which Windows
  Terminal reads at start, so a `Crude` profile appears and the user's own
  `settings.json` is never edited. The fragment names the installed program, the
  user folder, the Crude icon and the Nerd Font when the machine can draw it. It
  keeps the pane open when Crude fails, so the message stays readable. A
  terminal that is already running keeps the profiles it read when it started,
  so the installer says to run Reload settings before the profile is listed.
- The Start menu entry `Crude.lnk` opens one maximized window with the Crude
  profile and the installed program, so it also works before the terminal has
  read the profile, and it carries the Crude icon from the release archive.
- Windows Terminal binds keys for the whole application, not for one profile, so
  the window keeps the user's own key bindings. Crude's keys pass through
  unchanged: Ctrl+C and Ctrl+V behave as in any terminal, and the terminal
  reports modified keys precisely, so Shift+Enter and Ctrl+Enter reach Crude.
  Ctrl+Shift+C stays the terminal's own copy.
- Linux installs one pinned WezTerm build for this user, after a checksum check:
  `wezterm-20240203-110809-5046fc22.Ubuntu22.04.tar.xz` under
  `~/.local/share/crude/window`. No system package and no elevation. It writes
  one configuration that opens the installed `crude` program and fills the
  desktop area. It also adds `crude-window` next to the program, with an
  application-menu entry and `StartupWMClass=crude`. The terminal does not act on
  Ctrl+Shift+C, Ctrl+Shift+N, Ctrl+Shift+T or Ctrl+Shift+W, because Crude owns
  those keys. The pinned build's Kitty keyboard mode is disabled: it encodes
  plain Delete as Ctrl+H. Crude requests xterm `modifyOtherKeys` instead, so
  Delete, Backspace, Shift+Enter, Ctrl+Enter and modified editing keys remain
  distinct. Every GUI update rewrites this Crude-owned configuration; the
  user's own terminal configuration is not changed.
- It adds JetBrainsMono Nerd Font only when the machine cannot draw that family
  already: `~/.local/share/fonts/JetBrainsMonoNerdFont` on Linux, per-user values
  under `HKCU` that point at `%LOCALAPPDATA%\Crude\window\fonts` on Windows. A
  font that is already installed is used as it is and is never copied over,
  re-registered or removed, so the window cannot change how the rest of the
  machine draws text.
- A window folder that holds a terminal, a configuration or font values the
  installer does not use loses them, together with a menu entry that opens them.
  Nothing outside Crude's own folder is removed.
- A download, checksum or write failure reports the cause and keeps the program
  install.

### Files the installers change

- Linux installs under `~/.local/bin` by default and retains the
  redistribution notices in `.crude-licenses` beside it. It adds one idempotent
  PATH line to `.profile` and the active Bash/Zsh rc file; Fish receives a
  `fish_add_path` line to run. `--no-path` leaves every shell file alone. The
  verified program is published by same-directory atomic rename. A lock left by
  a killed installer needs manual removal, and the message says so.
- Windows defaults to `LOCALAPPDATA/Crude/bin`, retains notices, holds a
  per-directory lock, replaces files one at a time with the program last, and
  rolls back a failed update. The raw user PATH value and its registry kind and
  the process PATH are preserved. Machine PATH is never changed. Interrupted
  rollback keeps transaction preimages and refuses another update until you
  inspect them. `LOCALAPPDATA/Crude/git` holds the Git shell for this user
  when the machine has none. It is outside the program transaction, so a shell
  failure never undoes a program install, and it deletes nothing else.
- Neither installer deletes application data, changes a core image, or stops
  anything without the answer described above.

`scripts/test_install.py` and `scripts/test_install.ps1` run the real installers
against real archives, checksums and file operations and replace only the
network client. They cover consent, an idempotent PATH, update in place, damaged
archives, path traversal, links, a busy program, a refused stop, the window
entry, a window checksum refusal, and a Git shell that is present, installed,
reused, or refused. Native release jobs also install and repeat
the actual platform package. Portable PowerShell fixtures do not establish
native Windows registry, shortcut, executable-lock or ConPTY acceptance.

## Release modes

Releases are deliberately two different products:

| Mode | What runs | When to use it |
|---|---|---|
| `verified` (default) | Source validation, the complete `verify.yml` chain (website lint/build, Linux lint/design/docs/performance/build/tests/race/fuzz, installer fixtures and native Windows tests), native builds, installer checks, exact inventory, draft creation | Any release you intend to publish as a normal build |
| `instant` | Source validation, native builds, installer checks, exact inventory, draft creation | A time-sensitive build when the full chain is impractical; it deliberately skips `verify.yml` |

`instant` is not a weaker verification label on the same evidence: it has no
full-chain evidence at all. It still validates the immutable tag/commit,
performs native Linux/Windows packaging smoke checks, verifies the release
inventory and checksums, and creates a **draft** with an explicit `Instant
release` warning in the body. The tag and release title stay the plain version
in both modes, so installers, links and the latest-release lookup treat an
instant build like any other release. Never publish an instant draft as a normal
release without inspecting it and recording what was skipped.

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
python scripts/test_go.py
golangci-lint run ./...
make design-audit docs-check bench-gate
python scripts/test_go.py --race
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
gh workflow run pages.yml -R opencrude/opencrude.github.io
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

The website deploys from the organization site repository
`opencrude/opencrude.github.io`, which serves the root URL
<https://opencrude.github.io/>. Its `pages.yml` is `workflow_dispatch` only: a
push never publishes the website. The job checks out `site/` from this
repository at the requested ref, installs from `site/package-lock.json`, runs
the mandatory `@shadcn/lint` gate, builds `site/dist`, validates the built
artifact, uploads only that directory and uses the official Pages actions
pinned by commit. See the [website guide](website.md).

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
