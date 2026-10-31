#!/bin/sh
set -eu

# Split package names on whitespace without evaluating shell syntax or globs.
# Startup installs are opt-in; failure must stop startup instead of hiding a
# missing dependency until a scheduled job runs.
if [ -n "${LACRON_APK_PACKAGES:-}" ]; then
    if [ "$(id -u)" != "0" ]; then
        echo 'LACRON_APK_PACKAGES requires container root (UID 0).' >&2
        exit 1
    fi
    set -f
    for package in $LACRON_APK_PACKAGES; do
        case "$package" in
            -*) echo "Invalid Alpine package name: $package" >&2; exit 1 ;;
        esac
    done
    # apk receives package names as arguments; do not use eval here.
    apk add --no-cache -- $LACRON_APK_PACKAGES
fi

exec /usr/local/bin/lacron "$@"
