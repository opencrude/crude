# Releases and installation

> Status: Current guide — release preparation is implemented; first native GitHub CI and public release acceptance are still required.

## Supported packages

| Package | Build host | End-user requirements |
|---|---|---|
| Linux amd64 | Ubuntu 22.04, native cgo + pinned libghostty-vt | x86-64, glibc 2.35+, Bash, curl, GNU tar and coreutils |
| Windows amd64 | Windows Server 2022, native cgo + Zig | Windows x64 with ConPTY, PowerShell 5.1+; Windows Terminal recommended |

No macOS, ARM64, 32-bit or Alpine/musl release artifacts are advertised. The
embedded terminal requires cgo; a stub-only build is not a release. See
[native dependency bootstrap](../../deps/README.md) and
[terminal contracts](../reference/in-app-terminals.md).

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

## GitHub workflow and publication gate

`.github/workflows/tui.yml` calls `.github/workflows/verify.yml` on PRs and pushes.
Linux runs lint, design/docs/performance gates, build, full tests and full race
tests. Windows builds/tests with native cgo and runs installer fixtures on both
Windows PowerShell 5.1 and PowerShell 7. Actions are pinned by SHA; Zig downloads
have pinned SHA-256 checksums. No pull_request_target or inherited secrets.

`.github/workflows/release.yml` accepts v-prefixed tags (or an existing tag through
workflow_dispatch with **that tag selected as the ref**). Tag and verification
commit must match, and be reachable from master. It runs the same verification,
builds/tests native archives, and creates a **draft only** with both packages,
checksums and the installer scripts. Only the final draft job has contents:write.
It never overwrites an existing release or silently promotes a prerelease.

Before any public launch:

1. All contributing agents must have finished, their exact final reports reviewed,
   and no outstanding process/capture/editor changes left to arrive.
2. Review the complete diff, run the [validation gates](validation.md), and verify
   both native platforms. Do not skip failures or raise test/performance limits.
3. Audit tracked content **and Git history** for secrets/private data. Review
   findings; never make a genuine credential public or rewrite history without
   deliberate approval. `.gitleaksignore` contains only five exact, reviewed
   historical false-positive fingerprints (Go field identifiers and a key chord).
4. Keep temporary downloads, private profiles, databases, caches, build outputs and
   old repair backups out of publication. Do not purge other agents' recovery
   artifacts to make the source tree look tidy. Source archives contain tracked
   files, not ignored build directories.
5. Commit and push every reviewed change. Check git status is empty and HEAD is
   exactly origin/master. The audited source may become public at this point to
   use free standard GitHub-hosted Linux/Windows runners; that is **not** a binary
   release or certification. Inspect successful GitHub Linux **and** Windows runs
   before proceeding to release publication.
6. Choose the version and release notes. Tag only that reviewed commit; inspect
   the resulting draft, archive notices, installer risk/setup output and native
   install/update results. Verify no newer local/remote changes appeared.
7. Publish the reviewed draft only after those gates are met. Source visibility
   and release publication are separate actions; never relax a gate to get a
   binary out. This preparation does not attest that native gates have passed.

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
# Before binary publication, all native and release gates must also pass:
gh release edit <tag> --draft=false
```

## README screenshots

The images in the root assets directory are real Linux product captures of
Crude's own source through `scripts/tuishot.sh`, using an isolated profile. The
conversation is explicitly marked as a local fixture. No private configuration,
real API key, customer project or fake benchmark is shown. Wide and constrained
captures were inspected; every dedicated capture window was closed. See
[visual validation](validation.md#visual-capture) for the canonical workflow.
