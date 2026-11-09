BIN ?= bin/darkfactory
VERSION ?= 0.1.0-dev
# Version never changes between development builds, so the commit is the only
# identity a running daemon or the UI footer can show.
COMMIT ?= $(shell git describe --always --dirty 2>/dev/null || echo unknown)
TSC ?= tsc
GOVULNCHECK ?= govulncheck

# Release artifacts. Linux first: linux/amd64 and linux/arm64 are built, packed
# and checksummed; macOS compiles in CI but is not packaged yet.
DIST ?= dist
DIST_ARCHES ?= amd64 arm64
MAINTAINER ?= Wintoplay <winroom@gmail.com>
# One timestamp and no owner for every archive member, so two runs from the same
# commit produce the same bytes (NF-REL-6). Commit time, not build time.
EPOCH ?= $(shell git log -1 --format=%ct 2>/dev/null || date +%s)

.PHONY: all build install install-status install-restart upgrade desktop-upgrade update-source test race fuzz security-check protocol-verify vet vuln fmt vendor run clean ui-check ui-gate install-script-check check dist dist-clean desktop-preflight desktop-build desktop-run desktop-check android-build android-release android-install android-run android-pq-check android-pq-live clientbench-build clientbench-check

all: build

build:
	mkdir -p $(dir $(BIN))
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)" -o $(BIN) ./cmd/darkfactory

# ---- development install --------------------------------------------------
# The daemon embeds web/, so a running daemon keeps serving the UI it was
# built with until it is replaced *and* restarted. `make install` swaps the
# binary with one atomic rename and leaves no backup copy behind; this tree and
# the release archives are the source of truth. `make install-status` says
# whether the daemon is still on the previous build (after a swap its
# /proc/<pid>/exe reads "... (deleted)"), and `make install-restart` replaces a
# running daemon too. The detached session host is not touched, so hosted PTYs
# survive (SS-7).
BINDIR ?= $(HOME)/.local/bin
DARKFACTORY ?= $(BINDIR)/darkfactory
DARKFACTORY_STATE ?= $(if $(XDG_STATE_HOME),$(XDG_STATE_HOME),$(HOME)/.local/state)/darkfactory

install: build
	@mkdir -p "$(BINDIR)"
	@set -eu; tmp=$$(mktemp "$(DARKFACTORY).new.XXXXXX"); \
	trap 'rm -f "$$tmp"' EXIT HUP INT TERM; \
	install -m 0755 "$(BIN)" "$$tmp"; \
	mv -f "$$tmp" "$(DARKFACTORY)"
	@echo "installed $(DARKFACTORY) ($(VERSION) $(COMMIT))"
	@$(MAKE) --no-print-directory install-status

install-status:
	@df_alive() { \
		kill -0 "$$1" 2>/dev/null || return 1; \
		s=$$(cat "/proc/$$1/stat" 2>/dev/null) || return 0; \
		s=$${s##*) }; \
		[ "$${s%% *}" != Z ]; \
	}; \
	want="$(VERSION) ($(COMMIT))"; \
	got="$$($(DARKFACTORY) version 2>/dev/null || echo 'not installed')"; \
	if [ "$$got" != "$$want" ]; then \
		echo "installed binary is '$$got'; this tree builds '$$want'"; \
		echo "run: make install-restart"; \
	fi; \
	pid=$$(sed -n '1p' "$(DARKFACTORY_STATE)/serve.pid" 2>/dev/null || true); \
	if [ -n "$$pid" ] && df_alive "$$pid"; then \
		exe=$$(readlink "/proc/$$pid/exe" 2>/dev/null || true); \
		case "$$exe" in \
			"$(DARKFACTORY)") echo "daemon pid $$pid is running the installed build ($$got)";; \
			*) echo "daemon pid $$pid is still serving the previous build ($$exe)"; \
			   if command -v systemctl >/dev/null 2>&1 && \
			      systemctl --user is-active --quiet darkfactory 2>/dev/null; then \
				echo "restart it with: systemctl --user restart darkfactory"; \
			   else \
				echo "restart it with: make install-restart"; \
			   fi;; \
		esac; \
	else \
		echo "no running daemon; start one with: $(DARKFACTORY) serve"; \
	fi

update-source: install-restart

# One command for "I pulled new code": swap the daemon, the desktop shell and the
# open window. Depend on install-restart so the window reopens against the new
# daemon; the session host is never restarted, so hosted PTYs survive.
upgrade: install-restart desktop-upgrade
	@echo "upgrade complete: $(VERSION) ($(COMMIT))"

desktop-upgrade:
	@if $(MAKE) --no-print-directory desktop-preflight >/dev/null 2>&1; then \
		$(MAKE) --no-print-directory desktop-build; \
		mkdir -p "$(BINDIR)"; \
		install -m 0755 "$(BIN)-desktop" "$(BINDIR)/darkfactory-desktop"; \
		echo "installed $(BINDIR)/darkfactory-desktop"; \
		if [ -z "$${DF_UPGRADE_NO_WINDOW:-}" ] && \
		   [ -n "$${WAYLAND_DISPLAY:-}$${DISPLAY:-}" ] && \
		   pgrep -f "^$(BINDIR)/darkfactory-desktop " >/dev/null 2>&1; then \
			pkill -f "^$(BINDIR)/darkfactory-desktop " || true; \
			for i in $$(seq 1 40); do \
				pgrep -f "^$(BINDIR)/darkfactory-desktop " >/dev/null 2>&1 || break; \
				sleep 0.25; \
			done; \
			mkdir -p "$(DARKFACTORY_STATE)"; \
			setsid nohup "$(DARKFACTORY)" launch --desktop \
				>> "$(DARKFACTORY_STATE)/launch.log" 2>&1 < /dev/null & \
			echo "reopened the desktop window"; \
		fi; \
	else \
		echo "upgrade: native desktop SDK missing; skipped the desktop shell"; \
	fi

install-restart:
	@if command -v systemctl >/dev/null 2>&1 && \
	    systemctl --user is-active --quiet darkfactory 2>/dev/null; then \
		echo "darkfactory is managed by systemd; run make install, then systemctl --user restart darkfactory" >&2; \
		exit 1; \
	fi
	@$(MAKE) --no-print-directory install
	@set -eu; \
	df_alive() { \
		kill -0 "$$1" 2>/dev/null || return 1; \
		s=$$(cat "/proc/$$1/stat" 2>/dev/null) || return 0; \
		s=$${s##*) }; \
		[ "$${s%% *}" != Z ]; \
	}; \
	pid=$$(sed -n '1p' "$(DARKFACTORY_STATE)/serve.pid" 2>/dev/null || true); \
	if [ -n "$$pid" ] && df_alive "$$pid"; then \
		case "$$(readlink "/proc/$$pid/exe" 2>/dev/null || true)" in \
			"$(DARKFACTORY)"|"$(DARKFACTORY) (deleted)") kill "$$pid" || exit 1;; \
			*) echo "pid file points at a different program; not stopping it" >&2; exit 1;; \
		esac; \
		for i in $$(seq 1 40); do df_alive "$$pid" || break; sleep 0.25; done; \
		if df_alive "$$pid"; then \
			echo "daemon pid $$pid did not exit; refusing to start another daemon" >&2; exit 1; \
		fi; \
	fi; \
	mkdir -p "$(DARKFACTORY_STATE)"; \
	setsid nohup "$(DARKFACTORY)" serve >> "$(DARKFACTORY_STATE)/serve.log" 2>&1 < /dev/null & \
	for i in $$(seq 1 40); do \
		new=$$(sed -n '1p' "$(DARKFACTORY_STATE)/serve.pid" 2>/dev/null || true); \
		if [ -n "$$new" ] && [ "$$new" != "$$pid" ] && df_alive "$$new"; then \
			echo "daemon process started (pid $$new); check the app and $(DARKFACTORY_STATE)/serve.log for readiness"; exit 0; \
		fi; \
		sleep 0.25; \
	done; \
	echo "the new daemon did not report a pid; see $(DARKFACTORY_STATE)/serve.log" >&2; \
	exit 1

test:
	go test ./...

# The race detector needs cgo for its runtime, and only in the test binary:
# the shipped binary stays CGO_ENABLED=0 (NF-PORT-2).
race:
	CGO_ENABLED=1 go test -race ./...

# The corpus runs as ordinary tests above; this explores past it.
fuzz:
	sh test/security/fuzz.sh

security-check: android-pq-check android-pq-live fuzz protocol-verify

protocol-verify:
	sh tools/protocolverify/check.sh

vet:
	go vet ./...

# Reachability scan against the Go vulnerability database: it reports the
# advisories this code can actually reach, not the ones its dependency
# versions merely match. Dev-only and outside `make check` because the first
# run fetches the database over the network; the shipped binary never makes an
# outbound call (NF-S-1).
vuln:
	@bin=$(GOVULNCHECK); \
	command -v $$bin >/dev/null 2>&1 || bin=$$(go env GOPATH)/bin/govulncheck; \
	[ -x $$bin ] || { echo "vuln: govulncheck not found; go install golang.org/x/vuln/cmd/govulncheck@latest"; exit 1; }; \
	$$bin ./...

# The UI ships as plain JS; this is a syntax check plus a type check.
# Bun 1.4+ has a built-in checker; otherwise use an installed tsc.
ui-check:
	node --check web/app.js
	@if grep -nE 'innerHTML|insertAdjacentHTML|outerHTML|document\.write|new Function|[^a-zA-Z_.]eval\(' web/app.js; then \
		echo "ui-check: web/app.js must not inject HTML or evaluate code at runtime" >&2; \
		exit 1; \
	fi
	@if [ -n "$(TSC)" ] && command -v $(firstword $(TSC)) >/dev/null 2>&1; then \
		$(TSC) -p jsconfig.json; \
	elif command -v bun >/dev/null 2>&1; then \
		bun check; \
	else \
		echo "ui-check: neither tsc nor bun found; skipped type check"; \
	fi

check: vet test race ui-check clientbench-check install-script-check desktop-check

# The UI gate drives the real page at desktop and phone widths. It needs the
# browser/PTY harness (Playwright + Chromium), so it stays out of `make check`;
# `make ui-check` and the Go UI test cover the static half on every run.
ui-gate: build
	@test -n "$$(command -v node)" || { echo "ui-gate: node is required" >&2; exit 1; }
	test/browser/probe.sh gate
	test/browser/probe.sh hostile

# Optional clients and evaluation tools never enter the daemon dependency graph.
# Wails v3 on Linux needs GTK4 + WebKitGTK 6.0. Report the missing pkg-config
# modules and the package names instead of letting cgo fail mid-build.
WAILS_PKGCONFIG := gtk4 webkitgtk-6.0 libsoup-3.0 glib-2.0 gio-unix-2.0 vte-2.91-gtk4
desktop-preflight:
	@command -v pkg-config >/dev/null 2>&1 || { \
		echo "desktop-preflight: pkg-config is required" >&2; exit 1; }; \
	missing=""; \
	for pkg in $(WAILS_PKGCONFIG); do \
		pkg-config --exists "$$pkg" || missing="$$missing $$pkg"; \
	done; \
	if [ -n "$$missing" ]; then \
		echo "desktop-preflight: missing pkg-config modules:$$missing" >&2; \
		if [ -r /etc/os-release ]; then \
			. /etc/os-release; \
			case "$$ID $$ID_LIKE" in \
				*debian*|*ubuntu*) \
					echo "desktop-preflight: on Debian/Ubuntu install (needs approval):" >&2; \
					echo "  sudo apt install libgtk-4-dev libwebkitgtk-6.0-dev libsoup-3.0-dev libvte-2.91-gtk4-dev" >&2;; \
			esac; \
		fi; \
		exit 1; \
	fi; \
	echo "desktop-preflight: native SDK present"

desktop-build: desktop-preflight
	cd desktop && CGO_ENABLED=1 go build -trimpath -o ../bin/darkfactory-desktop ./cmd/darkfactory-desktop

desktop-run: desktop-build
	./bin/darkfactory-desktop --url "$${DF_URL:-http://127.0.0.1:8787/}"

desktop-check:
	cd desktop && go vet ./internal/...
	cd desktop && go test ./internal/...

# Optional native Android client. It is not part of `make check`: it needs a
# JDK with javac, Android SDK Build-Tools 36.0.0, and a connected device or
# emulator. The APK copies web/ at build time; it is not a second UI copy.
ANDROID_HOME ?= $(HOME)/Android/Sdk
ANDROID_ADB ?= $(ANDROID_HOME)/platform-tools/adb
ANDROID_APK ?= android/app/build/outputs/apk/debug/app-debug.apk
# The APK name is the nearest tag, the same identity a release archive carries.
# The versionCode is a checked-in high-water mark, not a commit count: Android
# refuses a downgrade and a rewritten history once shrank the count, which
# broke every update.
ANDROID_VERSION_NAME ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//')
ANDROID_VERSION_CODE ?= $(shell sed -n 's/^versionCode=//p' android/version.properties)

android-build:
	./android/gradlew -p android assembleDebug -PdfVersionName="$(ANDROID_VERSION_NAME)" -PdfVersionCode="$(ANDROID_VERSION_CODE)"

# The client's v2 crypto gets its own gate: a JVM
# compiles the app's real classes, checks them against Go vectors, and (with
# android-pq-live) against a disposable daemon. Neither target needs a device.
android-pq-check:
	sh test/android/check-pq.sh

android-pq-live:
	sh test/android/check-e2ee-live.sh

android-release:
	./android/gradlew -p android assembleRelease -PdfVersionName="$(ANDROID_VERSION_NAME)" -PdfVersionCode="$(ANDROID_VERSION_CODE)"

android-install: android-build
	"$(ANDROID_ADB)" install -r "$(ANDROID_APK)"

android-run: android-install
	"$(ANDROID_ADB)" shell am start -n dev.darkfactory.mobile/.MainActivity

clientbench-build:
	go build -trimpath -o bin/clientbench ./tools/clientbench

clientbench-check:
	node --check tools/clientbench/fixture.js
	node --check tools/clientbench/browser.cjs
	python3 -c 'import ast, pathlib; ast.parse(pathlib.Path("tools/clientbench/run.py").read_text())'
	go test ./tools/clientbench
	python3 tools/vendor-orca.py
	node --test clients/terminal/mouse-click.test.mjs

install-script-check:
	./test/install-uninstall.sh

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

vendor:
	go mod vendor

run:
	go run ./cmd/darkfactory serve

clean:
	rm -rf bin $(DIST)

dist-clean:
	rm -rf $(DIST)

# dist is what a release ships: one tarball and one .deb per architecture, plus
# SHA256SUMS over all of them. CI runs this target rather than repeating the
# commands, so a release is the same build a developer can reproduce locally.
dist: dist-clean
	@set -eu; \
	for arch in $(DIST_ARCHES); do \
		stage=$(DIST)/darkfactory_linux_$$arch; \
		mkdir -p "$$stage/share/systemd/user" "$$stage/share/applications" \
			"$$stage/share/icons/hicolor/scalable/apps"; \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -buildvcs=false \
			-ldflags "-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)" \
			-o "$$stage/darkfactory" ./cmd/darkfactory; \
		cp README.md "$$stage/"; \
		install -m 0755 install.sh "$$stage/"; \
		cp packaging/systemd/darkfactory.service "$$stage/share/systemd/user/"; \
		cp packaging/darkfactory.desktop "$$stage/share/applications/"; \
		cp packaging/icons/darkfactory.svg \
			"$$stage/share/icons/hicolor/scalable/apps/"; \
		tar --sort=name --mtime=@$(EPOCH) --owner=0 --group=0 --numeric-owner \
			--mode='u+rwX,go+rX,go-w' \
			-C $(DIST) -czf "$(DIST)/darkfactory_linux_$$arch.tar.gz" "darkfactory_linux_$$arch"; \
	done
	@set -eu; \
	for arch in $(DIST_ARCHES); do \
		root=$(DIST)/deb/$$arch; \
		mkdir -p "$$root/DEBIAN" "$$root/usr/bin" "$$root/usr/lib/systemd/user" \
			"$$root/usr/share/applications" "$$root/usr/share/icons/hicolor/scalable/apps" \
			"$$root/usr/share/doc/darkfactory"; \
		install -m 0755 "$(DIST)/darkfactory_linux_$$arch/darkfactory" "$$root/usr/bin/darkfactory"; \
		install -m 0644 packaging/systemd/darkfactory.service \
			"$$root/usr/lib/systemd/user/darkfactory.service"; \
		install -m 0644 packaging/darkfactory.desktop \
			"$$root/usr/share/applications/darkfactory.desktop"; \
		install -m 0644 packaging/icons/darkfactory.svg \
			"$$root/usr/share/icons/hicolor/scalable/apps/darkfactory.svg"; \
		install -m 0644 README.md "$$root/usr/share/doc/darkfactory/README.md"; \
		size=$$(du -sk "$$root" | cut -f1); \
		sed -e "s/@VERSION@/$(VERSION)/g" -e "s/@ARCH@/$$arch/g" \
			-e "s|@MAINTAINER@|$(MAINTAINER)|g" -e "s/@INSTALLED@/$$size/g" \
			packaging/debian/control.in > "$$root/DEBIAN/control"; \
		find "$$root" -type d -exec chmod 0755 {} +; \
		chmod 0644 "$$root/DEBIAN/control"; \
		SOURCE_DATE_EPOCH=$(EPOCH) dpkg-deb --root-owner-group --build "$$root" \
			"$(DIST)/darkfactory_$(VERSION)_$$arch.deb" >/dev/null; \
	done
	@cd $(DIST) && sha256sum *.tar.gz *.deb > SHA256SUMS && cat SHA256SUMS
