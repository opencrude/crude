#!/usr/bin/env bash
# Install or update a published Linux x86-64 release. No Go, Zig, or sudo.
# The whole operation is a function so a truncated piped download cannot install.
main() (
    set -euo pipefail
    umask 022

    fail() { printf 'crude installer: %s\n' "$*" >&2; exit 1; }
    usage() {
        cat <<'EOF'
Usage: bash install.sh --accept-beta-risk [--version vX.Y.Z] [--install-dir DIR] [--no-path]
                      [--repository OWNER/REPO]
Defaults: latest stable release, ~/.local/bin, uoosef403/crude.
Run the same command again to update. Explicit versions also allow prereleases.
The installer never stops Crude or modifies its configuration/session data.
EOF
    }

    local version='' repository='uoosef403/crude' install_dir="${CRUDE_INSTALL_DIR:-$HOME/.local/bin}" add_path=1 accept_beta_risk=0
    while (($#)); do
        case "$1" in
            --version|--install-dir|--repository)
                (($# >= 2)) || fail "$1 requires a value"
                case "$1" in
                    --version) version=$2 ;;
                    --install-dir) install_dir=$2 ;;
                    --repository) repository=$2 ;;
                esac
                shift 2 ;;
            --no-path) add_path=0; shift ;;
            --accept-beta-risk) accept_beta_risk=1; shift ;;
            --help|-h) usage; exit 0 ;;
            *) fail "unknown argument '$1' (see --help)" ;;
        esac
    done
    [[ "$repository" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$ ]] || fail 'repository must be OWNER/REPO'
    valid_version() {
        [[ "$1" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$ ]] || return 1
        local part
        local -a identifiers
        if [[ "$1" == *-* ]]; then
            IFS=. read -ra identifiers <<< "${1#*-}"
            for part in "${identifiers[@]}"; do [[ ! "$part" =~ ^0[0-9]+$ ]] || return 1; done
        fi
        return 0
    }
    [[ -z "$version" ]] || valid_version "$version" || fail 'version must be vMAJOR.MINOR.PATCH (optionally -prerelease)'
    [[ "$(uname -s)" == Linux ]] || fail 'this script supports Linux only; on Windows use install.ps1'
    [[ "$(uname -m)" == x86_64 ]] || fail 'only Linux x86-64 releases are currently available'
    for command in curl tar sha256sum mktemp chmod mkdir mv cp grep awk timeout; do
        command -v "$command" >/dev/null || fail "required command not found: $command"
    done
    [[ "$install_dir" == /* && "$install_dir" != *$'\n'* && "$install_dir" != *$'\r'* ]] || fail 'install directory must be an absolute, single-line path'

    cat >&2 <<'EOF'
WARNING: Crude is in active development. You are beta-testing software that can
edit files and run commands. Bugs or agent mistakes may damage or lose work.
Commit important changes, keep independent backups, and review agent actions.
Installing or updating is not a guarantee that your projects or sessions are safe.
EOF
    if ((accept_beta_risk == 0)); then
        local answer=''
        if [[ -t 0 && -t 1 ]]; then
            read -r -p 'Accept beta-testing risks and continue? Type yes: ' answer || true
        fi
        [[ "$answer" == yes ]] || fail 'not installed; rerun with --accept-beta-risk only if you accept these risks'
    fi

    # Font installation is observable; the font actually selected by a terminal
    # is not. Never claim that an installed font proves this terminal is ready.
    if ! command -v fc-list >/dev/null || ! fc-list : family 2>/dev/null | grep -iE 'JetBrainsMono (Nerd Font|NF)(,|$)' >/dev/null; then
        printf '%s\n' 'SETUP WARNING: JetBrainsMono Nerd Font (NF) is missing or cannot be detected.' >&2
    fi
    case "${TERM_PROGRAM:-}:${TERM:-}" in
        *kitty*|*alacritty*) ;;
        *) printf '%s\n' 'SETUP WARNING: preferred terminal not detected; use current Kitty or Alacritty for the best experience.' >&2 ;;
    esac
    if [[ "${COLORTERM:-}" != truecolor && "${COLORTERM:-}" != 24bit ]]; then
        printf '%s\n' 'SETUP WARNING: truecolor is not advertised; colors may differ.' >&2
    fi
    cat >&2 <<'EOF'
Crude's recommended setup: current Kitty/Alacritty, JetBrainsMono Nerd Font NF
(not NFM), truecolor, and a comfortable wide terminal. Install the font from
https://www.nerdfonts.com/font-downloads and select it in your terminal settings.
The active font cannot be verified automatically. In Crude, enable Nerd font and
Show file icons under Tools > Settings > IDE > Interface once the font is ready.
You can ask Crude itself: "Help me configure the ideal Crude terminal and font
setup; explain each change and ask before installing or modifying settings."
No fonts, terminal settings, system packages or privileges are changed here.
EOF

    download() {
        curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
            --tlsv1.2 --retry 3 --connect-timeout 15 --max-time 180 "$@"
    }
    if [[ -z "$version" ]]; then
        local resolved
        resolved=$(download --head --output /dev/null --write-out '%{url_effective}' "https://github.com/$repository/releases/latest") ||
            fail 'cannot resolve latest release; check the network and that this repository has a public, published release'
        local prefix="https://github.com/$repository/releases/tag/"
        [[ "$resolved" == "$prefix"* ]] || fail 'GitHub did not resolve a published release'
        version=${resolved#"$prefix"}
        valid_version "$version" || fail "unsupported release tag: $version"
    fi

    local asset="crude_${version}_linux_amd64.tar.gz" base="https://github.com/$repository/releases/download/$version"
    local work='' stage='' locked=0 lock=''
    # Invoked by the EXIT trap inside this installation subshell.
    # shellcheck disable=SC2329
    cleanup() {
        [[ -z "$stage" ]] || rm -rf -- "$stage"
        [[ -z "$work" ]] || rm -rf -- "$work"
        ((locked == 0)) || rmdir -- "$lock"
        return 0
    }
    trap cleanup EXIT
    trap 'exit 130' INT
    trap 'exit 143' TERM
    work=$(mktemp -d "${TMPDIR:-/tmp}/crude-install.XXXXXXXX")
    printf 'Downloading Crude %s (linux/amd64)...\n' "$version"
    download --output "$work/$asset" "$base/$asset" || fail "cannot download $asset; the release may not support this platform"
    download --output "$work/SHA256SUMS" "$base/SHA256SUMS" || fail 'cannot download release checksums'
    local expected actual
    expected=$(awk -v name="$asset" '$2 == name { print $1; count++ } END { if (count != 1) exit 1 }' "$work/SHA256SUMS") ||
        fail 'checksum manifest must contain exactly one entry for this archive'
    [[ "$expected" =~ ^[0-9a-fA-F]{64}$ ]] || fail 'invalid SHA-256 checksum'
    actual=$(sha256sum "$work/$asset")
    [[ "${actual%% *}" == "${expected,,}" ]] || fail 'SHA-256 mismatch; existing installation was not changed'

    # Refuse links, unexpected members and traversal before extracting anything.
    tar -tzf "$work/$asset" > "$work/members" || fail 'invalid release archive'
    local member binary_count=0
    while IFS= read -r member; do
        [[ "$member" != /* && "/$member/" != *'/../'* && "$member" != *$'\r'* ]] || fail 'unsafe archive member'
        case "$member" in
            crude) binary_count=$((binary_count + 1)) ;;
            LICENSE-MIT|LICENSE-APACHE|README.md|licenses|licenses/|licenses/*) ;;
            *) fail "unexpected archive member: $member" ;;
        esac
    done < "$work/members"
    ((binary_count == 1)) || fail 'archive must contain exactly one root crude executable'
    tar -tvzf "$work/$asset" > "$work/details" || fail 'cannot inspect release archive'
    if grep -qEv '^[-d]' "$work/details"; then fail 'archive must contain only regular files and directories'; fi

    mkdir -p -- "$install_dir"
    lock="$install_dir/.crude-install.lock"
    mkdir -- "$lock" 2>/dev/null || fail "another installer is running (or stale lock $lock needs manual removal)"
    locked=1
    [[ ! -d "$install_dir/crude" ]] || fail 'destination crude is a directory'
    stage=$(mktemp -d "$install_dir/.crude-install.XXXXXXXX")
    tar -xzf "$work/$asset" --no-same-owner --no-same-permissions -C "$stage"
    [[ -f "$stage/crude" && -s "$stage/crude" && -f "$stage/LICENSE-MIT" && -f "$stage/LICENSE-APACHE" && -f "$stage/README.md" && -d "$stage/licenses" ]] || fail 'incomplete release package'
    chmod 755 "$stage/crude"
    local reported
    reported=$(timeout 10 "$stage/crude" --version) || fail 'downloaded executable cannot run or timed out; Linux releases require glibc 2.35 or newer (not Alpine/musl)'
    [[ "$reported" == "crude $version ("*'; linux/amd64)' && "$reported" =~ ^crude\ [^[:space:]]+\ \([0-9a-fA-F]{40}\;\ [0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z\;\ linux/amd64\)$ ]] || fail "downloaded executable reported the wrong version/platform: $reported"

    # Retain the redistribution notices next to the installed binary, not in the
    # application profile. All fallible preparation precedes the atomic rename.
    local notices="$install_dir/.crude-licenses/${version}-${actual:0:12}"
    # Module-cache license files can be read-only. Publish their content-addressed
    # directory once; a repeat must not try to overwrite those immutable files.
    if [[ ! -d "$notices" ]]; then
        mkdir -- "$stage/notices"
        cp -R -- "$stage/licenses/." "$stage/notices/"
        cp -- "$stage/LICENSE-MIT" "$stage/LICENSE-APACHE" "$stage/README.md" "$stage/notices/"
        mkdir -p -- "$install_dir/.crude-licenses"
        mv -T -- "$stage/notices" "$notices"
    fi
    if ((add_path)); then
        local path_line rc
        # Persist a literal expansion for the user's next shell, not this one.
        # shellcheck disable=SC2016
        printf -v path_line 'export PATH=%q:"$PATH"' "$install_dir"
        local -a profiles=("$HOME/.profile")
        case "${SHELL:-/bin/bash}" in
            */bash) profiles+=("$HOME/.bashrc") ;;
            */zsh) profiles+=("${ZDOTDIR:-$HOME}/.zshrc") ;;
            */fish) printf 'Fish: run fish_add_path %q to add Crude to PATH.\n' "$install_dir" ;;
        esac
        for rc in "${profiles[@]}"; do
            if [[ ! -f "$rc" ]] || ! grep -Fqx -- "$path_line" "$rc"; then
                printf '\n# Crude installer: user-local executable directory\n%s\n' "$path_line" >> "$rc"
            fi
        done
    fi
    mv -fT -- "$stage/crude" "$install_dir/crude"
    printf 'Installed %s\nExecutable: %s/crude\nNotices: %s\n' "$reported" "$install_dir" "$notices"
    if [[ ":$PATH:" != *":$install_dir:"* ]]; then
        # shellcheck disable=SC2016
        printf 'Open a new terminal, or run: export PATH=%q:"$PATH"\n' "$install_dir"
    fi
    printf 'To update, run this installer again. Existing sessions and configuration were not changed.\n'
)
main "$@"
