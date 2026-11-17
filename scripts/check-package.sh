#!/bin/sh
#
#   Checks that an addon package holds everything the system needs to install and run it: the
#   installer, the manifest, the daemon, the rc.d script, the proxy rule (etc/lighttpd.conf - a
#   package without it serves no page), the three pages, the icons, the 503 page, the version file;
#   that no settings came along; and that every name in it is one the system's installer and its
#   backup can handle.
#
#     scripts/check-package.sh dist/shell-*-0.1.0.tar.gz
#
set -e
[ $# -gt 0 ] || { echo "usage: $0 <package.tar.gz>..." >&2; exit 2; }
status=0
for pkg in "$@"; do
    list=$(tar tzf "$pkg" | sed 's#^\./##')
    for want in update_script openccu-lite.json shell/bin/lite-shell shell/rc.d shell/etc/lighttpd.conf \
        shell/app/index.html shell/app/terminal.html shell/app/files.html shell/www/icon.svg shell/www/icon-dark.svg shell/www/lighttpd-error-503.html shell/versions; do
        if ! printf '%s\n' "$list" | grep -qx "$want"; then
            echo "$pkg: missing $want" >&2
            status=1
        fi
    done
    if printf '%s\n' "$list" | grep -q '^shell/var/\|settings\.json$\|state\.json$'; then
        echo "$pkg: must not contain settings, state or shell/var/" >&2
        status=1
    fi
    if printf '%s\n' "$list" | grep -v '^[A-Za-z0-9_./@-]*$' | grep -q .; then
        echo "$pkg: a file name with a character outside A-Za-z0-9_./@-" >&2
        status=1
    fi
    [ $status -eq 0 ] && echo "$pkg: ok"
done
exit $status
