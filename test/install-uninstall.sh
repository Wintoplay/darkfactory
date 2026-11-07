#!/usr/bin/env bash
# The uninstall branch derives the desktop entry, icon and user unit from
# $HOME/XDG, not from --prefix. A disposable --prefix uninstall must leave the
# operator's real launcher alone; an uninstall of the default prefix owns it
# and removes all three files.
#
# This is a regression test for a real incident: a probe ran
# `install.sh --uninstall --prefix /tmp/...` and deleted the live
# ~/.local/share/applications/darkfactory.desktop, its icon and the user unit.
set -euo pipefail

repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT

home=$root/home
bindir=$home/.local/bin
desktop=$home/.local/share/applications/darkfactory.desktop
icon=$home/.local/share/icons/hicolor/scalable/apps/darkfactory.svg
unit=$home/.config/systemd/user/darkfactory.service

mkdir -p "$home/.local/share/applications" \
	"$home/.local/share/icons/hicolor/scalable/apps" \
	"$home/.config/systemd/user" \
	"$bindir" \
	"$root/stub"
printf 'desktop\n' >"$desktop"
printf 'icon\n' >"$icon"
printf '[Service]\nExecStart=%s serve\n' "$bindir/darkfactory" >"$unit"
touch "$bindir/darkfactory"

# Never touch the real user manager from a test.
printf '#!/bin/sh\nexit 0\n' >"$root/stub/systemctl"
chmod 0755 "$root/stub/systemctl"

run_install() {
	HOME=$home XDG_DATA_HOME= XDG_CONFIG_HOME= PATH=$root/stub:$PATH \
		"$repo/install.sh" "$@"
}

# A foreign prefix must not remove launchers it did not install.
run_install --uninstall --prefix "$root/other" >/dev/null
for f in "$desktop" "$icon" "$unit"; do
	[ -f "$f" ] || {
		echo "FAIL: foreign-prefix uninstall removed $f" >&2
		exit 1
	}
done

# The default user prefix owns them, so uninstall removes all three.
run_install --uninstall >/dev/null
for f in "$desktop" "$icon" "$unit"; do
	[ ! -e "$f" ] || {
		echo "FAIL: default-prefix uninstall kept $f" >&2
		exit 1
	}
done

echo "PASS install-uninstall: foreign prefix kept the launcher; default prefix removed it"
