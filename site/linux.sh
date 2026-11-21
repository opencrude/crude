#!/bin/sh
# Install or update Crude on Linux x86-64:
#
#   curl -fsSL https://opencrude.github.io/linux.sh | sh
#
# This runs the installer of the latest published release,
# https://github.com/opencrude/crude/releases/latest/download/install.sh
# Put installer options after "sh -s --", for example:
#
#   curl -fsSL https://opencrude.github.io/linux.sh | sh -s -- --yes --gui
#
# The installer is read completely before it starts, so a broken download
# installs nothing. Its questions go to your terminal.
main() {
    url='https://github.com/opencrude/crude/releases/latest/download/install.sh'
    if ! command -v bash >/dev/null 2>&1; then
        printf '\nCrude installer: bash is missing. Install bash, then try again.\nNothing was changed.\n' >&2
        return 1
    fi
    if ! installer=$(curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
        --tlsv1.2 --retry 3 --connect-timeout 15 --max-time 120 "$url"); then
        printf '\nCrude installer: cannot download %s. Check your network connection, then try again.\nNothing was changed.\n' "$url" >&2
        return 1
    fi
    # A piped script reads this file as its input. The installer reads the
    # terminal instead, so it can ask; without a terminal it runs unattended.
    if [ -t 0 ]; then
        bash -c "$installer" install.sh "$@"
    elif (exec </dev/tty) 2>/dev/null; then
        bash -c "$installer" install.sh "$@" </dev/tty
    else
        bash -c "$installer" install.sh "$@" </dev/null
    fi
}
main "$@"
