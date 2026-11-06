#!/usr/bin/env bash
# Install or update a published Linux x86-64 release of Crude.
# You do not need Go, Zig, sudo, or a source checkout.
# The whole operation is a function so a truncated piped download cannot install.
main() (
    set -euo pipefail
    umask 022

    # ---- Output -------------------------------------------------------------
    # Every sentence states the state and the next step. Colour is used only
    # for a terminal; the same text reads correctly in a log file.
    color=''
    if [[ -t 2 && -z ${NO_COLOR:-} && ${TERM:-dumb} != dumb ]]; then
        color=$'\033['
    fi
    say() { printf '%s\n' "$*" >&2; }
    step() { printf '  %s%s%s %-9s %s\n' "$color" "${1}" "$([[ -n $color ]] && printf '\033[0m')" "${2}" "${3}" >&2; }
    ok() { step '✓' "$1" "$2"; }
    note() { step '·' "$1" "$2"; }
    warn() { step '!' "$1" "$2"; }
    die() { printf '\nCrude installer: %s\nNothing was changed.\n' "$*" >&2; exit 1; }
    ask() {
        # ask PROMPT DEFAULT: prints the prompt, reads one answer.
        local prompt=$1 default=${2:-n} answer=''
        if [[ ! -t 0 || ! -t 2 ]]; then return 1; fi
        printf '  ? %s [%s] ' "$prompt" "$([[ $default == y ]] && printf 'Y/n' || printf 'y/N')" >&2
        read -r answer || true
        [[ -n $answer ]] || answer=$default
        [[ $answer == [yY] || $answer == [yY][eE][sS] ]]
    }
    usage() {
        cat <<'EOF'
Usage: bash install.sh [options]

  --yes                 Accept the warning and use the defaults. No prompt.
  --gui, --no-gui       Install or skip the Crude desktop window.
  --stop, --no-stop     Stop or keep a running Crude while updating.
  --version vX.Y.Z      Install one exact release. Prereleases are allowed.
  --install-dir DIR     Install the program somewhere else (default ~/.local/bin).
  --no-path             Do not add the install folder to your PATH.
  --repository OWNER/REPO
  --help                Show this text.

The desktop window is a pinned terminal that starts Crude and nothing else.
Run the same command again to update. Press Ctrl+C at any time to stop.
EOF
    }

    version='' repository='opencrude/crude' install_dir="${CRUDE_INSTALL_DIR:-$HOME/.local/bin}"
    add_path=1 accept=0 gui='' stop=''
    while (($#)); do
        case "$1" in
            --version|--install-dir|--repository)
                (($# >= 2)) || die "$1 needs a value. Run --help to see the options."
                case "$1" in
                    --version) version=$2 ;;
                    --install-dir) install_dir=$2 ;;
                    --repository) repository=$2 ;;
                esac
                shift 2 ;;
            --yes|-y) accept=1; shift ;;
            --gui) gui=1; shift ;;
            --no-gui) gui=0; shift ;;
            --stop) stop=1; shift ;;
            --no-stop) stop=0; shift ;;
            --no-path) add_path=0; shift ;;
            --help|-h) usage; exit 0 ;;
            *) die "unknown option '$1'. Run --help to see the options." ;;
        esac
    done

    [[ "$repository" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$ ]] || die 'the repository must look like OWNER/REPO'
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
    [[ -z "$version" ]] || valid_version "$version" || die 'the version must look like vMAJOR.MINOR.PATCH, with an optional -prerelease'
    [[ "$(uname -s)" == Linux ]] || die 'this installer runs on Linux only. On Windows use install.ps1. Crude has no macOS release yet.'
    [[ "$(uname -m)" == x86_64 ]] || die 'Crude has no release for this processor. Only Linux x86-64 is available.'
    # Name every missing tool at once so one install line fixes them all.
    local -a missing=()
    for command in curl tar xz sha256sum mktemp chmod mkdir mv cp grep awk timeout; do
        command -v "$command" >/dev/null || missing+=("$command")
    done
    if ((${#missing[@]})); then
        die "this installer needs these commands: ${missing[*]}. Install them, then try again. For Debian or Ubuntu: sudo apt install curl xz-utils coreutils tar grep gawk"
    fi
    [[ "$install_dir" == /* && "$install_dir" != *$'\n'* && "$install_dir" != *$'\r'* ]] || die 'the install folder must be one absolute path'

    say ''
    say 'Crude installer'
    say ''

    # ---- Consent ------------------------------------------------------------
    say 'Crude is in active development. It changes files and runs commands.'
    say 'Bugs and agent mistakes can lose work. Commit and back up important work.'
    say ''
    if ((accept == 0)); then
        if ! ask 'Install anyway?' n; then
            die 'you did not accept the warning. Add --yes to accept it, or run this command in a terminal window'
        fi
    fi

    # ---- Release ------------------------------------------------------------
    download() {
        curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
            --tlsv1.2 --retry 3 --connect-timeout 15 --max-time 600 "$@"
    }
    if [[ -z "$version" ]]; then
        local resolved prefix
        resolved=$(download --head --output /dev/null --write-out '%{url_effective}' "https://github.com/$repository/releases/latest") ||
            die 'cannot read the latest release. Check your network connection.'
        prefix="https://github.com/$repository/releases/tag/"
        [[ "$resolved" == "$prefix"* ]] || die 'this repository has no published release yet'
        version=${resolved#"$prefix"}
        valid_version "$version" || die "'$version' is not a release this installer understands"
    fi
    local asset="crude_${version}_linux_amd64.tar.gz" base="https://github.com/$repository/releases/download/$version"
    say "Version $version · linux/amd64"

    local work='' stage='' locked=0 lock=''
    # Invoked by the EXIT trap inside this installation subshell.
    # shellcheck disable=SC2317
    cleanup() {
        [[ -z "$stage" ]] || rm -rf -- "$stage"
        [[ -z "$work" ]] || rm -rf -- "$work"
        ((locked == 0)) || rm -rf -- "$lock"
        return 0
    }
    trap cleanup EXIT
    trap 'exit 130' INT
    trap 'exit 143' TERM
    work=$(mktemp -d "${TMPDIR:-/tmp}/crude-install.XXXXXXXX")

    download --output "$work/$asset" "$base/$asset" ||
        die "cannot download $asset. This release may not support this platform"
    download --output "$work/SHA256SUMS" "$base/SHA256SUMS" || die 'cannot download the release checksums'
    local expected actual
    expected=$(awk -v name="$asset" '$2 == name { print $1; count++ } END { if (count != 1) exit 1 }' "$work/SHA256SUMS") ||
        die 'the checksum file does not list this archive exactly once'
    [[ "$expected" =~ ^[0-9a-fA-F]{64}$ ]] || die 'the checksum for this archive is not valid'
    actual=$(sha256sum "$work/$asset")
    [[ "${actual%% *}" == "${expected,,}" ]] || die 'the archive does not match its checksum. The installed copy was not changed'
    ok 'Download' "$(du -h --apparent-size "$work/$asset" | cut -f1) · SHA-256 matches"

    # Refuse links, unexpected members and traversal before extracting anything.
    tar -tzf "$work/$asset" > "$work/members" || die 'the release archive is damaged'
    local member binary_count=0
    while IFS= read -r member; do
        [[ "$member" != /* && "/$member/" != *'/../'* && "$member" != *$'\r'* ]] || die 'the release archive contains an unsafe path'
        case "$member" in
            crude) binary_count=$((binary_count + 1)) ;;
            LICENSE-MIT|LICENSE-APACHE|README.md|crude-icon.png|crude-icon.svg|crude-icon.ico|licenses|licenses/|licenses/*) ;;
            *) die "the release archive holds an unexpected file: $member" ;;
        esac
    done < "$work/members"
    ((binary_count == 1)) || die 'the release archive must hold exactly one crude program'
    tar -tvzf "$work/$asset" > "$work/details" || die 'cannot read the release archive'
    if grep -qEv '^[-d]' "$work/details"; then die 'the release archive must hold only files and folders'; fi

    mkdir -p -- "$install_dir"
    lock="$install_dir/.crude-install.lock"
    take_lock() {
        mkdir -- "$lock" 2>/dev/null || return 1
        printf '%s\n' "$$" > "$lock/owner"
        return 0
    }
    if ! take_lock; then
        owner=$(cat "$lock/owner" 2>/dev/null || true)
        if [[ -n "$owner" ]] && kill -0 "$owner" 2>/dev/null; then
            die "another installer (process $owner) is running. Wait for it to finish, or stop it, then try again"
        fi
        # An installer that stopped without cleanup leaves only its own lock.
        [[ -f "$lock/owner" ]] && rm -rf -- "$lock"
        take_lock || die "cannot lock the install folder. Check that it is writable"
    fi
    locked=1
    [[ ! -d "$install_dir/crude" ]] || die 'the install folder holds a folder named crude'
    stage=$(mktemp -d "$install_dir/.crude-install.XXXXXXXX")
    tar -xzf "$work/$asset" --no-same-owner --no-same-permissions -C "$stage"
    [[ -f "$stage/crude" && -s "$stage/crude" && -f "$stage/LICENSE-MIT" && -f "$stage/LICENSE-APACHE" && -f "$stage/README.md" && -d "$stage/licenses" ]] ||
        die 'the release archive is incomplete'
    chmod 755 "$stage/crude"
    local reported
    reported=$(timeout 10 "$stage/crude" --version) ||
        die 'the downloaded program does not start. Linux releases need glibc 2.35 or newer (not Alpine)'
    [[ "$reported" == "crude $version ("*'; linux/amd64)' ]] ||
        die "the downloaded program reports '$reported', not $version. Nothing was installed"

    # ---- Stop a running Crude ----------------------------------------------
    # Crude keeps working in a detached core after the window closes. The
    # update replaces that program, so the user decides whether it stops.
    running_pids() {
        local target resolved path pid
        target=$1
        resolved=$(readlink -f -- "$target" 2>/dev/null) || resolved=$target
        for path in /proc/[0-9]*/exe; do
            pid=${path#/proc/}
            pid=${pid%/exe}
            [[ "$pid" != "$$" && "$pid" != "$BASHPID" ]] || continue
            target=$(readlink -- "$path" 2>/dev/null) || continue
            target=${target% (deleted)}
            [[ "$target" == "$resolved" ]] && printf '%s\n' "$pid"
        done
        return 0
    }
    stop_running_crude() {
        local -a pids=()
        mapfile -t pids < <(running_pids "$install_dir/crude")
        ((${#pids[@]})) || return 0
        local count=${#pids[@]} pid alive
        if [[ "$stop" == 0 ]]; then
            note 'Running' "Crude stays open on ${version_old:-the old version}. It changes at the next start"
            return 0
        fi
        if [[ "$stop" != 1 ]]; then
            if ! ask "Crude is running ($count processes). Stop it now? Open sessions end. Unsaved editor text stays in History" n; then
                die 'Crude is still running. Close it and run this installer again, or pass --stop'
            fi
        fi
        kill -TERM "${pids[@]}" 2>/dev/null || true
        for _ in 1 2 3 4 5 6 7 8 9 10; do
            sleep 0.5
            alive=0
            for pid in "${pids[@]}"; do kill -0 "$pid" 2>/dev/null && alive=1; done
            ((alive)) || break
        done
        if ((alive)); then
            if [[ "$stop" == 1 ]] || ask 'Crude did not stop. Force it to stop? Unsaved work in open files can be lost' n; then
                for pid in "${pids[@]}"; do kill -KILL "$pid" 2>/dev/null || true; done
                sleep 1
            else
                die 'Crude is still running. Nothing was installed'
            fi
        fi
        alive=0
        for pid in "${pids[@]}"; do kill -0 "$pid" 2>/dev/null && alive=1; done
        ((alive == 0)) || die 'Crude is still running. Nothing was installed'
        ok 'Stop' "Crude is closed"
    }
    version_old=''
    if [[ -x "$install_dir/crude" ]]; then
        version_old=$(timeout 5 "$install_dir/crude" --version 2>/dev/null) || version_old=''
        version_old=${version_old#crude }
        version_old=${version_old%% (*}
    fi
    stop_running_crude

    # ---- Install -----------------------------------------------------------
    # Retain the redistribution notices next to the program, not in the profile.
    local notices="$install_dir/.crude-licenses/${version}-${actual:0:12}"
    if [[ ! -d "$notices" ]]; then
        mkdir -- "$stage/notices"
        cp -R -- "$stage/licenses/." "$stage/notices/"
        cp -- "$stage/LICENSE-MIT" "$stage/LICENSE-APACHE" "$stage/README.md" "$stage/notices/"
        mkdir -p -- "$install_dir/.crude-licenses"
        mv -T -- "$stage/notices" "$notices"
    fi
    mv -fT -- "$stage/crude" "$install_dir/crude"
    ok 'Program' "$install_dir/crude"
    if ((add_path)); then
        local path_line rc
        # Persist a literal expansion for the user's next shell, not this one.
        # shellcheck disable=SC2016
        printf -v path_line 'export PATH=%q:"$PATH"' "$install_dir"
        local -a profiles=("$HOME/.profile")
        case "${SHELL:-/bin/bash}" in
            */bash) profiles+=("$HOME/.bashrc") ;;
            */zsh) profiles+=("${ZDOTDIR:-$HOME}/.zshrc") ;;
            */fish) profiles=("$HOME/.profile"); note 'PATH' "for fish run: fish_add_path $install_dir" ;;
        esac
        for rc in "${profiles[@]}"; do
            if [[ ! -f "$rc" ]] || ! grep -Fqx -- "$path_line" "$rc"; then
                printf '\n# Crude installer: user-local program folder\n%s\n' "$path_line" >> "$rc"
            fi
        done
    fi

    # ---- Optional desktop window -------------------------------------------
    # One pinned terminal, configured to start Crude and nothing else. It is
    # installed for this user only; no system package and no elevation.
    wezterm_version='20240203-110809-5046fc22'
    wezterm_sha256='142cb46de648200b852cc4b1d932e6b8f2a9d3c55f6dc5af44b6d4eeb9385f5c'
    font_version='v3.5.1'
    font_sha256='04d5e8f903693f9dd13e16f867e994834e681eb3c72c0d337a770dcda09010cf'
    data="${XDG_DATA_HOME:-$HOME/.local/share}/crude"
    install_window() {
        local root="$data/window"
        local prefix="$root/wezterm-$wezterm_version"
        local archive="$root/wezterm-$wezterm_version.Ubuntu22.04.tar.xz"
        local font_archive="$root/JetBrainsMono-$font_version.tar.xz"
        mkdir -p -- "$root"
        if [[ ! -x "$prefix/usr/bin/wezterm" ]]; then
            download --output "$archive" "https://github.com/wezterm/wezterm/releases/download/$wezterm_version/wezterm-$wezterm_version.Ubuntu22.04.tar.xz" || {
                warn 'Window' 'cannot download the desktop window. Check your network connection'
                return 1
            }
            local sum
            sum=$(sha256sum "$archive")
            if [[ "${sum%% *}" != "$wezterm_sha256" ]]; then
                warn 'Window' 'the desktop window does not match its pinned checksum'
                return 1
            fi
            tar -tvJf "$archive" > "$root/details" || { warn 'Window' 'the desktop window archive is damaged'; return 1; }
            if grep -qEv '^[-d]' "$root/details"; then
                warn 'Window' 'the desktop window archive holds links or special files'
                return 1
            fi
            while IFS= read -r member; do
                if [[ "$member" == /* || "/$member/" == *'/../'* ]]; then
                    warn 'Window' 'the desktop window archive holds an unsafe path'
                    return 1
                fi
            done < <(tar -tJf "$archive")
            local unpack
            unpack=$(mktemp -d "$root/.unpack.XXXXXXXX")
            tar -xJf "$archive" --no-same-owner --no-same-permissions -C "$unpack"
            rm -rf -- "$prefix"
            # The archive holds one program tree in a 'wezterm' folder.
            if [[ -d "$unpack/wezterm" ]]; then mv -T -- "$unpack/wezterm" "$prefix"; rm -rf -- "$unpack"; else mv -T -- "$unpack" "$prefix"; fi
            rm -f -- "$archive" "$root/details"
        fi
        if ! "$prefix/usr/bin/wezterm" --version >/dev/null 2>&1; then
            warn 'Window' 'the desktop window cannot start on this system'
            say '  Install these packages, then run this installer again with --gui:'
            say '    libxkbcommon0 libfontconfig1 libwayland-client0 libx11-6'
            return 1
        fi

        # The font makes the file icons and box drawing in Crude align.
        local fonts="${XDG_DATA_HOME:-$HOME/.local/share}/fonts/JetBrainsMonoNerdFont" found=1
        if [[ ! -d "$fonts" ]] || ! compgen -G "$fonts/*.ttf" >/dev/null; then
            found=0
            if download --output "$font_archive" "https://github.com/ryanoasis/nerd-fonts/releases/download/$font_version/JetBrainsMono.tar.xz"; then
                local sum
                sum=$(sha256sum "$font_archive")
                if [[ "${sum%% *}" == "$font_sha256" ]]; then
                    mkdir -p -- "$fonts"
                    tar -xJf "$font_archive" --no-same-owner --no-same-permissions -C "$fonts" 2>/dev/null || true
                    rm -f -- "$font_archive"
                    # Keep one flat folder of fonts whatever the archive layout is.
                    find "$fonts" -mindepth 2 -name '*.ttf' -exec mv -f -- {} "$fonts"/ \; 2>/dev/null || true
                    find "$fonts" -mindepth 1 -type d -empty -delete 2>/dev/null || true
                    if command -v fc-cache >/dev/null; then fc-cache --force >/dev/null 2>&1 || true; fi
                    compgen -G "$fonts/*.ttf" >/dev/null && found=1
                fi
            fi
        fi
        [[ "$found" == 1 ]] || warn 'Font' 'JetBrainsMono Nerd Font is missing. File icons may look wrong'

        local crude_bin="$install_dir/crude" lua="$root/wezterm.lua"
        cat > "$lua" <<LUA
-- Written by the Crude installer. This window runs Crude only.
local wezterm = require 'wezterm'
return {
    default_prog = { '$crude_bin' },
    font = wezterm.font_with_fallback { 'JetBrainsMono Nerd Font', 'JetBrainsMono NF', 'monospace' },
    font_size = 13.0,
    window_decorations = 'TITLE | RESIZE',
    hide_tab_bar_if_only_one_tab = true,
    window_close_confirmation = 'NeverPrompt',
    check_for_updates = false,
    audible_bell = 'Disabled',
    keys = {
        { key = 'n', mods = 'CTRL|SHIFT', action = wezterm.action.DisableDefaultAssignment },
        { key = 't', mods = 'CTRL|SHIFT', action = wezterm.action.DisableDefaultAssignment },
    },
}
LUA
        cat > "$install_dir/crude-window" <<LAUNCH
#!/bin/sh
# Open Crude in its own window. Nothing else runs here.
exec "$prefix/usr/bin/wezterm" --config-file "$lua" start --always-new-process --class crude "\$@"
LAUNCH
        chmod 755 "$install_dir/crude-window"

        local icon="$data/icons/crude.png"
        if [[ -f "$stage/crude-icon.png" ]]; then
            mkdir -p -- "$data/icons"
            cp -- "$stage/crude-icon.png" "$icon"
            [[ ! -f "$stage/crude-icon.svg" ]] || cp -- "$stage/crude-icon.svg" "$data/icons/crude.svg"
        fi
        if [[ -f "$icon" ]]; then
            local themed="$HOME/.local/share/icons/hicolor/512x512/apps"
            mkdir -p -- "$themed"
            cp -- "$icon" "$themed/crude.png"
        fi
        local desktop="$HOME/.local/share/applications" icon_line=''
        mkdir -p -- "$desktop"
        # An Icon line must name a file that exists, or the menu shows a warning.
        [[ ! -f "$icon" ]] || icon_line="Icon=$icon"
        {
            printf '%s\n' \
                '[Desktop Entry]' \
                'Type=Application' \
                'Name=Crude' \
                'Comment=Editor, terminals and AI agents in one window' \
                "Exec=\"$install_dir/crude-window\""
            [[ -z "$icon_line" ]] || printf '%s\n' "$icon_line"
            printf '%s\n' 'Terminal=false' 'Categories=Development;IDE;' 'StartupWMClass=crude'
        } > "$desktop/crude.desktop"
        chmod 644 "$desktop/crude.desktop"
        if command -v update-desktop-database >/dev/null; then update-desktop-database "$desktop" >/dev/null 2>&1 || true; fi
        ok 'Window' 'Crude is in your application menu'
        return 0
    }
    if [[ -z "$gui" ]]; then
        if ((accept)); then gui=0; elif ask 'Install the Crude window? It runs Crude in its own window and nothing else' y; then gui=1; else gui=0; fi
    fi
    if ((gui)); then
        install_window || note 'Window' 'the window was not installed. Run this installer again with --gui'
    else
        note 'Window' 'not installed. Run this installer with --gui to add it'
    fi

    # ---- Summary -----------------------------------------------------------
    say ''
    say "Crude $version is ready."
    if [[ ":$PATH:" == *":$install_dir:"* ]]; then
        say "Next: run: crude"
    elif ((add_path)); then
        say "Next: open a new terminal, then run: crude"
        # shellcheck disable=SC2016
        say "For this terminal only: export PATH=$(printf '%q' "$install_dir"):\"\$PATH\""
    else
        say "Next: run: $install_dir/crude"
        say "PATH was not changed. Add $install_dir to PATH to run crude by name."
    fi
    say 'Run this installer again to update. Your sessions and settings were not changed.'
)
main "$@"
