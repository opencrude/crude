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
        color=1
    fi
    say() { printf '%s\n' "$*" >&2; }
    # paint CODE TEXT colours one symbol in a terminal and prints it plain in a
    # log file, so both read the same.
    paint() {
        if [[ -n $color ]]; then printf '\033[%sm%s\033[0m' "$1" "$2"; else printf '%s' "$2"; fi
    }
    step() { printf '  %s %-9s %s\n' "$1" "$2" "$3" >&2; }
    ok() { step "$(paint 32 '✓')" "$1" "$2"; }
    note() { step '·' "$1" "$2"; }
    warn() { step "$(paint 33 '!')" "$1" "$2"; }
    die() { printf '\nCrude installer: %s\nNothing was changed.\n' "$*" >&2; exit 1; }
    download() {
        curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
            --tlsv1.2 --retry 3 --connect-timeout 15 --max-time 600 "$@"
    }

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

  --yes                 Use the defaults. No questions.
  --gui, --no-gui       Add or skip the GUI version.
  --stop, --no-stop     Stop or keep a running Crude while updating.
  --version vX.Y.Z      Install one exact release. Prereleases are allowed.
  --install-dir DIR     Install the program somewhere else (default ~/.local/bin).
  --no-path             Do not add the install folder to your PATH.
  --repository OWNER/REPO
  --help                Show this text.

The GUI version opens Crude in its own window, with its own terminal and font.
Run the same command again to update Crude and the window together.
Press Ctrl+C at any time to stop.
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
    # Only a person at a terminal is asked. An unattended install continues;
    # --yes only skips the question.
    if ((accept == 0)) && [[ -t 0 && -t 2 ]]; then
        if ! ask 'Install anyway?' n; then
            die 'you did not accept the warning'
        fi
    fi

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
    # The decision comes before the download so the user answers once, at the
    # start; the program is signalled only after the new one is verified.
    stop_pids=()
    stop_choice=go
    plan_stop() {
        mapfile -t stop_pids < <(running_pids "$install_dir/crude")
        ((${#stop_pids[@]})) || return 0
        if [[ "$stop" == 0 ]]; then
            stop_choice=keep
            return 0
        fi
        [[ "$stop" == 1 ]] && return 0
        ask "Crude is running (${#stop_pids[@]} processes). Stop it now? Open sessions end. Unsaved editor text stays in History" n ||
            die 'Crude is still running. Close it and run this installer again, or pass --stop'
    }
    do_stop() {
        if [[ "$stop_choice" == keep ]]; then
            note 'Running' "Crude stays open on ${version_old:-the old version}. It changes at the next start"
            return 0
        fi
        ((${#stop_pids[@]})) || return 0
        local pid alive
        kill -TERM "${stop_pids[@]}" 2>/dev/null || true
        for _ in 1 2 3 4 5 6 7 8 9 10; do
            sleep 0.5
            alive=0
            for pid in "${stop_pids[@]}"; do kill -0 "$pid" 2>/dev/null && alive=1; done
            ((alive)) || break
        done
        if ((alive)); then
            if [[ "$stop" == 1 ]] || ask 'Crude did not stop. Force it to stop? Unsaved work in open files can be lost' n; then
                for pid in "${stop_pids[@]}"; do kill -KILL "$pid" 2>/dev/null || true; done
                sleep 1
            else
                die 'Crude is still running. Nothing was installed'
            fi
        fi
        alive=0
        for pid in "${stop_pids[@]}"; do kill -0 "$pid" 2>/dev/null && alive=1; done
        ((alive == 0)) || die 'Crude is still running. Nothing was installed'
        ok 'Stop' 'Crude is closed'
    }

    # ---- Optional window ----------------------------------------------------
    # One pinned terminal with one configuration, for this user only. It is the
    # same Crude; the window only makes it open like an application.
    ghostty_version='1.4.0'
    ghostty_sha256='50ad4f8b54a01e7d41426e7d12ffae9c88aa1c516ac140755e2bca596e1d2ae6'
    font_version='v3.5.1'
    font_sha256='04d5e8f903693f9dd13e16f867e994834e681eb3c72c0d337a770dcda09010cf'
    user_data="${XDG_DATA_HOME:-$HOME/.local/share}"
    data="$user_data/crude"
    window_root="$data/window"
    window_prefix="$window_root/ghostty-$ghostty_version"
    window_config="$window_root/ghostty.conf"
    window_launcher="$install_dir/crude-window"
    window_id='io.github.opencrude.Crude'
    window_work=''
    fonts_dir="$user_data/fonts/JetBrainsMonoNerdFont"
    # Crude RTL draws Arabic, Persian and Hebrew in the window's cells. It
    # ships with each release, so every install refreshes it.
    rtl_fonts_dir="$user_data/fonts/CrudeRTL"

    # decide_window sets install, update or skip before anything is downloaded,
    # so the question never waits behind a long transfer.
    decide_window() {
        window_state=skip window_asked=0
        if [[ "$gui" == 0 ]]; then
            return 0
        fi
        if [[ "$gui" == 1 || -x "$window_launcher" || -f "$user_data/applications/$window_id.desktop" ]] ||
           [[ -f "$window_root/wezterm.lua" && -f "$HOME/.local/share/applications/crude.desktop" ]]; then
            window_state=install
            return 0
        fi
        # An unattended install adds the program only; a graphical session gets
        # one short question.
        ((accept)) && return 0
        [[ -n ${DISPLAY:-}${WAYLAND_DISPLAY:-} ]] || return 0
        window_asked=1
        if ask 'Do you want the GUI version as well?' y; then window_state=install; fi
    }

    install_window_terminal() {
        local image="Ghostty-$ghostty_version-x86_64.AppImage" archive sum
        if [[ -x "$window_prefix/bin/ghostty" ]] &&
           timeout 10 "$window_prefix/bin/ghostty" --version >/dev/null 2>&1; then return 0; fi
        archive="$window_work/$image"
        # The portable GTK build lets us use the same terminal on old and new
        # distributions. Extract once instead of mounting it with FUSE on launch.
        download --output "$archive" "https://github.com/pkgforge-dev/ghostty-appimage/releases/download/v$ghostty_version/$image" || {
            warn 'Window' 'cannot download the window. Check your network connection'
            return 1
        }
        sum=$(sha256sum < "$archive") || return 1
        if [[ "${sum%% *}" != "$ghostty_sha256" ]]; then
            warn 'Window' 'the download does not match its checksum'
            return 1
        fi
        if ! (cd -- "$window_work" && chmod 755 -- "$archive" &&
              timeout -k 5 120 "$archive" --appimage-extract >/dev/null 2>&1); then
            warn 'Window' 'cannot unpack the download'
            return 1
        fi
        # The runtime can expose its extraction directory via a relative link.
        local unpack staged_root
        staged_root=$(readlink -f -- "$window_work") || return 1
        unpack=$(readlink -f -- "$window_work/squashfs-root") || return 1
        [[ "$unpack" == "$staged_root/"* ]] || return 1
        if ! timeout 10 "$unpack/bin/ghostty" --version >/dev/null 2>&1; then
            warn 'Window' 'the window cannot start on this system'
            return 1
        fi
        if [[ -e "$window_prefix" || -L "$window_prefix" ]]; then
            mv -T -- "$window_prefix" "$window_work/previous" || return 1
        fi
        if ! mv -T -- "$unpack" "$window_prefix"; then
            [[ ! -e "$window_work/previous" ]] || mv -T -- "$window_work/previous" "$window_prefix"
            return 1
        fi
    }

    install_window_fonts() {
        local stamp="$fonts_dir/.crude-font"
        if [[ -f "$fonts_dir/JetBrainsMonoNerdFont-Regular.ttf" && -f "$stamp" ]] &&
           [[ "$(cat "$stamp")" == "$font_version" ]]; then
            return 0
        fi
        if command -v fc-list >/dev/null && fc-list -q ':family=JetBrainsMono NF:style=Regular'; then
            return 0
        fi
        local archive="$window_work/JetBrainsMono-$font_version.tar.xz" sum
        if ! download --output "$archive" "https://github.com/ryanoasis/nerd-fonts/releases/download/$font_version/JetBrainsMono.tar.xz"; then
            [[ -n $(compgen -G "$fonts_dir/*.ttf") ]] || warn 'Font' 'the font for the window is missing. Icons may look wrong'
            return 0
        fi
        sum=$(sha256sum < "$archive") || return 1
        if [[ "${sum%% *}" != "$font_sha256" ]]; then
            rm -f -- "$archive"
            warn 'Font' 'the downloaded font does not match its pinned checksum'
            return 0
        fi
        mkdir -p -- "$fonts_dir"
        tar -xJf "$archive" --no-same-owner --no-same-permissions -C "$fonts_dir" 2>/dev/null || true
        rm -f -- "$archive"
        # Keep one flat folder whatever the archive layout is.
        find "$fonts_dir" -mindepth 2 -name '*.ttf' -exec mv -f -- {} "$fonts_dir"/ \; 2>/dev/null || true
        find "$fonts_dir" -mindepth 1 -type d -empty -delete 2>/dev/null || true
        if compgen -G "$fonts_dir/*.ttf" >/dev/null; then
            printf '%s\n' "$font_version" > "$stamp"
            if command -v fc-cache >/dev/null; then fc-cache --force >/dev/null 2>&1 || true; fi
        else
            warn 'Font' 'the font for the window is missing. Icons may look wrong'
        fi
    }

    install_window_rtl_font() {
        compgen -G "$stage/fonts/CrudeRTL-*.ttf" >/dev/null || return 0
        mkdir -p -- "$rtl_fonts_dir"
        cp -f -- "$stage"/fonts/CrudeRTL-*.ttf "$stage/fonts/LICENSE-DejaVu" "$rtl_fonts_dir"/ 2>/dev/null ||
            { warn 'Font' 'right-to-left text may look wrong'; return 0; }
        if command -v fc-cache >/dev/null; then fc-cache --force >/dev/null 2>&1 || true; fi
    }

    write_window_files() {
        local config="$window_work/ghostty.conf"
        cat > "$config" <<'GHOSTTY' || return 1
# Written by the Crude installer. Use the desktop theme for the window frame.
window-theme = system
window-decoration = server
font-family = JetBrainsMono NF
font-family = Crude RTL
font-codepoint-map = U+0590-U+06FF,U+0750-U+077F,U+0870-U+08FF,U+FB1D-U+FDFF,U+FE70-U+FEFC=Crude RTL
# Crude sends separate, already joined cells. Do not combine Lam and Alef again.
font-feature = -rlig
font-size = 13
window-width = 120
window-height = 36
maximize = true
window-save-state = never
confirm-close-surface = false
resize-overlay = never
bell-features = no-audio, no-system, no-title, no-border
shell-integration = none
# Let Crude handle its keyboard and pointer commands, including Shift+click.
keybind = clear
mouse-shift-capture = always
mouse-scroll-multiplier = discrete:1,precision:1
# Otherwise the GTK tab bar takes horizontal gestures even for a single tab.
gtk-horizontal-tab-scroll = false
GHOSTTY
        if ! timeout 10 "$window_prefix/bin/ghostty" +validate-config --config-file="$config" >/dev/null 2>&1; then
            warn 'Window' 'cannot load the window configuration'
            return 1
        fi
        # Quote paths as literal Bash arguments; -e runs Crude directly.
        {
            printf '#!/usr/bin/env bash\n# Open Crude in its own window.\n'
            # Use the private resources even when launched from another Ghostty.
            printf 'export GHOSTTY_RESOURCES_DIR=%q\n' "$window_prefix/share/ghostty"
            # The bundled loader starts directly. AppRun also writes a shared
            # /tmp link, which two installs could point at different trees.
            printf 'exec %q --config-default-files=false --config-file=%q --gtk-single-instance=false --class=%q -e %q\n' \
                "$window_prefix/bin/ghostty" "$window_config" "$window_id" "$install_dir/crude"
        } > "$stage/crude-window" || return 1
        chmod 755 -- "$stage/crude-window" || return 1

        local icon="$data/icons/crude.png"
        if [[ -f "$stage/crude-icon.png" ]]; then
            mkdir -p -- "$data/icons" && cp -- "$stage/crude-icon.png" "$icon" || return 1
            [[ ! -f "$stage/crude-icon.svg" ]] || cp -- "$stage/crude-icon.svg" "$data/icons/crude.svg" || return 1
        fi
        local desktop="$user_data/applications" exec_path="$window_launcher"
        mkdir -p -- "$desktop" || return 1
        # Exec has two layers of escaping: desktop string values and command
        # quoting. Percent signs are field codes even inside a quoted argument.
        exec_path=${exec_path//\\/\\\\\\\\}
        exec_path=${exec_path//\"/'\\"'}
        exec_path=${exec_path//\$/'\\$'}
        exec_path=${exec_path//\`/'\\`'}
        exec_path=${exec_path//%/%%}
        local desktop_file="$window_work/$window_id.desktop"
        {
            printf '%s\n' '[Desktop Entry]' 'Type=Application' 'Name=Crude' \
                'Comment=Editor, terminals and AI agents in one window' \
                "Exec=\"$exec_path\""
            if [[ -f "$icon" ]]; then printf 'Icon=%s\n' "${icon//\\/\\\\}"; fi
            printf '%s\n' 'Terminal=false' 'Categories=Development;IDE;' "StartupWMClass=$window_id"
        } > "$desktop_file" || return 1
        mv -fT -- "$config" "$window_config" ||
            { warn 'Window' 'cannot write the window configuration'; return 1; }
        mv -fT -- "$stage/crude-window" "$window_launcher" &&
            mv -fT -- "$desktop_file" "$desktop/$window_id.desktop" || return 1
    }

    retire_wezterm() {
        local legacy_menu="$HOME/.local/share/applications/crude.desktop" old live=0
        # Only touch the entries and files written by the old Crude installer.
        if [[ -f "$legacy_menu" ]] && grep -Fqx 'Name=Crude' "$legacy_menu" &&
           grep -Fqx 'StartupWMClass=crude' "$legacy_menu"; then
            rm -f -- "$legacy_menu" || return 1
        fi
        for old in "$window_root"/wezterm-[0-9]*; do
            if [[ -d "$old" && ! -L "$old" ]]; then
                if [[ -n $(running_pids "$old/usr/bin/wezterm-gui") || -n $(running_pids "$old/usr/bin/wezterm") ]]; then
                    live=1
                    continue
                fi
                rm -rf -- "$old" || return 1
            elif [[ "$old" == *.Ubuntu22.04.tar.xz && -f "$old" ]]; then
                rm -f -- "$old" || return 1
            fi
        done
        if ((live == 0)); then rm -f -- "$window_root/wezterm.lua" "$window_root/wezterm.lua.new"; fi
    }

    install_window() {
        mkdir -p -- "$window_root" || return 1
        window_work=$(mktemp -d "$window_root/.install.XXXXXXXX") || return 1
        install_window_terminal || return 1
        install_window_fonts
        install_window_rtl_font
        write_window_files || return 1
        retire_wezterm || warn 'Window' 'cannot remove the old window files. Run this installer again'
        if command -v update-desktop-database >/dev/null; then
            update-desktop-database "$user_data/applications" >/dev/null 2>&1 || true
        fi
        ok 'Window' 'Crude is in your application menu'
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

    version_old=''
    if [[ -x "$install_dir/crude" ]]; then
        version_old=$(timeout 5 "$install_dir/crude" --version 2>/dev/null) || version_old=''
        version_old=${version_old#crude }
        version_old=${version_old%% (*}
    fi
    plan_stop
    decide_window

    # ---- Release ------------------------------------------------------------
    local work='' stage='' locked=0 lock=''
    # Invoked by the EXIT trap inside this installation subshell.
    # shellcheck disable=SC2317
    cleanup() {
        [[ -z "$window_work" ]] || rm -rf -- "$window_work"
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
            LICENSE-MIT|LICENSE-APACHE|README.md|crude-icon.png|crude-icon.svg|crude-icon.ico|licenses|licenses/|licenses/*|fonts|fonts/|fonts/*) ;;
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

    # ---- Install -----------------------------------------------------------
    do_stop
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

    # ---- The window --------------------------------------------------------
    if [[ "$window_state" == install ]]; then
        # A command inside install_window that fails must not end the install,
        # because the program is already in place.
        if ! install_window; then
            warn 'Window' 'the window is not up to date. Run this installer again'
        fi
    elif [[ -x "$window_launcher" ]]; then
        note 'Window' 'not updated. Run this installer with --gui to update it'
    elif ((window_asked)); then
        note 'Window' 'not added'
    else
        note 'Window' 'not added. Run this installer with --gui to add it'
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
