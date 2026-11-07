#!/bin/sh
# Install DarkFactory from GitHub releases on Linux.
#
# Options:
#   --system              install to /usr/local (run it with sudo)
#   --prefix DIR          install root (default: ~/.local)
#   --bin-dir DIR         where the binary goes (default: PREFIX/bin)
#   --version TAG         release tag, e.g. v0.1.0 (default: the latest release)
#   --desktop             also install the user unit and the app-menu entry
#   --uninstall           remove what this script installs, then exit
#   --dry-run             print what would happen, download nothing
#   --help
#
# Environment overrides: DARKFACTORY_REPO (owner/name), DARKFACTORY_BASE_URL
# (a release-download base, for mirrors), PREFIX, BINDIR.
#
# The archive is checked against the release's SHA256SUMS before anything is
# written, so a truncated download or a swapped artifact stops here.

set -eu

REPO=${DARKFACTORY_REPO:-Wintoplay/darkfactory}
BASE_OVERRIDE=${DARKFACTORY_BASE_URL:-}
PREFIX=${PREFIX:-$HOME/.local}
BINDIR=${BINDIR:-}
VERSION=${DARKFACTORY_VERSION:-}
DESKTOP=0
UNINSTALL=0
DRY_RUN=0

usage() {
	cat <<'EOF'
Install DarkFactory on Linux from a GitHub release.

  install.sh [options]

  --system          install to /usr/local (run it with sudo)
  --prefix DIR      install root (default: ~/.local)
  --bin-dir DIR     where the binary goes (default: PREFIX/bin)
  --version TAG     release tag, e.g. v0.1.0 (default: the latest release)
  --desktop         also install the systemd user unit and the app-menu entry
  --uninstall       remove what this script installs, then exit
  --dry-run         print what would happen, download nothing
  --help

The binary is downloaded from https://github.com/Wintoplay/darkfactory/releases and
verified against the release's SHA256SUMS before it is installed.

What stays behind on --uninstall: the token and policy in ~/.config/darkfactory
and the state (state.db, PTY host) in ~/.local/state/darkfactory. Delete those
directories yourself if you mean to forget the token as well.
EOF
}

die() {
	printf 'darkfactory install: %s\n' "$1" >&2
	exit 1
}

say() {
	printf '%s\n' "$1"
}

run() {
	if [ "$DRY_RUN" = 1 ]; then
		printf '  would run: %s\n' "$*"
		return 0
	fi
	"$@"
}

while [ $# -gt 0 ]; do
	case "$1" in
	--system)
		PREFIX=/usr/local
		shift
		;;
	--prefix)
		[ $# -ge 2 ] || die "--prefix needs a directory"
		PREFIX=$2
		shift 2
		;;
	--bin-dir)
		[ $# -ge 2 ] || die "--bin-dir needs a directory"
		BINDIR=$2
		shift 2
		;;
	--version)
		[ $# -ge 2 ] || die "--version needs a tag"
		VERSION=$2
		shift 2
		;;
	--desktop)
		DESKTOP=1
		shift
		;;
	--uninstall)
		UNINSTALL=1
		shift
		;;
	--dry-run)
		DRY_RUN=1
		shift
		;;
	--help | -h)
		usage
		exit 0
		;;
	*)
		die "unknown option: $1 (try --help)"
		;;
	esac
done

[ -n "$BINDIR" ] || BINDIR=$PREFIX/bin
DATA_DIR=${XDG_DATA_HOME:-$HOME/.local/share}
CONFIG_DIR=${XDG_CONFIG_HOME:-$HOME/.config}
UNIT_DIR=$CONFIG_DIR/systemd/user
UNIT_FILE=$UNIT_DIR/darkfactory.service
DESKTOP_FILE=$DATA_DIR/applications/darkfactory.desktop
ICON_FILE=$DATA_DIR/icons/hicolor/scalable/apps/darkfactory.svg

if [ "$UNINSTALL" = 1 ]; then
	say "Removing DarkFactory from $BINDIR"
	# The desktop entry, icon and user unit live under $HOME/XDG, not under
	# $PREFIX. Removing them unconditionally used to delete the operator's real
	# launcher when uninstalling a disposable --prefix install. Only remove
	# them when they belong to this binary: the default user prefix, or a unit
	# whose ExecStart names $BINDIR/darkfactory.
	remove_desktop=0
	if [ "$BINDIR" = "$HOME/.local/bin" ]; then
		remove_desktop=1
	elif [ -f "$UNIT_FILE" ] && grep -qxF "ExecStart=$BINDIR/darkfactory serve" "$UNIT_FILE"; then
		remove_desktop=1
	fi
	if [ "$remove_desktop" = 1 ] && command -v systemctl >/dev/null 2>&1 && [ -f "$UNIT_FILE" ]; then
		run systemctl --user disable --now darkfactory.service || true
	fi
	run rm -f "$BINDIR/darkfactory"
	if [ "$remove_desktop" = 1 ]; then
		run rm -f "$UNIT_FILE" "$DESKTOP_FILE" "$ICON_FILE"
		if command -v systemctl >/dev/null 2>&1; then
			run systemctl --user daemon-reload || true
		fi
	else
		say "Kept $DESKTOP_FILE, $ICON_FILE and $UNIT_FILE (they belong to another install)."
	fi
	say "Kept: $CONFIG_DIR/darkfactory (token, policy) and ${XDG_STATE_HOME:-$HOME/.local/state}/darkfactory (state.db)."
	say "Run 'darkfactory session-host --stop' before removing a running install."
	exit 0
fi

[ "$(uname -s)" = "Linux" ] || die "this build targets Linux; macOS and Windows are not packaged yet"

case "$(uname -m)" in
x86_64 | amd64) ARCH=amd64 ;;
aarch64 | arm64) ARCH=arm64 ;;
*) die "unsupported architecture: $(uname -m)" ;;
esac

ARTIFACT=darkfactory_linux_$ARCH.tar.gz
if [ -n "$BASE_OVERRIDE" ]; then
	RELEASE_BASE=$BASE_OVERRIDE
elif [ -n "$VERSION" ]; then
	RELEASE_BASE=https://github.com/$REPO/releases/download/$VERSION
else
	RELEASE_BASE=https://github.com/$REPO/releases/latest/download
fi

command -v sha256sum >/dev/null 2>&1 || die "sha256sum is required"
if command -v curl >/dev/null 2>&1; then
	FETCH=curl
elif command -v wget >/dev/null 2>&1; then
	FETCH=wget
else
	die "curl or wget is required"
fi

fetch() {
	if [ "$FETCH" = curl ]; then
		curl -fsSL "$1"
	else
		wget -qO- "$1"
	fi
}

say "DarkFactory installer"
say "  platform   linux/$ARCH"
say "  release    ${VERSION:-latest} ($RELEASE_BASE)"
say "  binary     $BINDIR/darkfactory"
if [ "$DESKTOP" = 1 ]; then
	say "  unit       $UNIT_FILE"
	say "  app menu   $DESKTOP_FILE"
fi

if [ "$DRY_RUN" = 1 ]; then
	run install -d "$BINDIR"
	run install -m 0755 "<extracted>/darkfactory" "$BINDIR/darkfactory"
	exit 0
fi

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT HUP INT TERM

say "Downloading"
fetch "$RELEASE_BASE/$ARTIFACT" >"$TMP/$ARTIFACT" || die "could not download $RELEASE_BASE/$ARTIFACT"
fetch "$RELEASE_BASE/SHA256SUMS" >"$TMP/SHA256SUMS" || die "could not download SHA256SUMS"

say "Verifying"
grep " $ARTIFACT\$" "$TMP/SHA256SUMS" >"$TMP/want" || die "$ARTIFACT is not listed in SHA256SUMS"
(cd "$TMP" && sha256sum -c want) >/dev/null || die "checksum mismatch for $ARTIFACT"

tar -xzf "$TMP/$ARTIFACT" -C "$TMP"
STAGE=$TMP/darkfactory_linux_$ARCH
[ -x "$STAGE/darkfactory" ] || die "the archive did not contain the expected binary"

install -d "$BINDIR"
install -m 0755 "$STAGE/darkfactory" "$BINDIR/darkfactory"

if [ "$DESKTOP" = 1 ]; then
	install -d "$UNIT_DIR" "$DATA_DIR/applications" "$DATA_DIR/icons/hicolor/scalable/apps"
	# A user install lives outside /usr/bin, so the unit names the real path.
	sed "s|^ExecStart=.*|ExecStart=$BINDIR/darkfactory serve|" \
		"$STAGE/share/systemd/user/darkfactory.service" >"$UNIT_FILE"
	install -m 0644 "$STAGE/share/applications/darkfactory.desktop" "$DESKTOP_FILE"
	install -m 0644 "$STAGE/share/icons/hicolor/scalable/apps/darkfactory.svg" "$ICON_FILE"
	if command -v systemctl >/dev/null 2>&1; then
		systemctl --user daemon-reload || true
	fi
	if command -v update-desktop-database >/dev/null 2>&1; then
		update-desktop-database "$DATA_DIR/applications" 2>/dev/null || true
	fi
	if command -v gtk-update-icon-cache >/dev/null 2>&1; then
		gtk-update-icon-cache -q -t "$DATA_DIR/icons/hicolor" 2>/dev/null || true
	fi
fi

say ""
say "Installed $("$BINDIR/darkfactory" version 2>/dev/null || echo "to $BINDIR")"
case ":$PATH:" in
*":$BINDIR:"*) ;;
*)
	say ""
	say "Add $BINDIR to PATH, then open a new shell:"
	say "  echo 'export PATH=\"$BINDIR:\$PATH\"' >> ~/.profile"
	;;
esac
say ""
say "Next:"
if [ "$DESKTOP" = 1 ]; then
	say "  systemctl --user enable --now darkfactory   # start at login"
	say "  darkfactory open                            # or click the app icon"
else
	say "  darkfactory serve --debug                   # or: darkfactory launch"
	say "  darkfactory open"
fi
