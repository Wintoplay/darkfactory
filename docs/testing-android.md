# Testing the Android client

> **Remote browser access is disabled.** The daemon now refuses `/`,
> `index.html`, and static assets to a non-loopback peer or a request with
> forwarding headers. The Firefox-on-Android procedure below is retained as
> historical evidence for the shared `web/` layout, not as a supported remote
> client. The native app in [`../android/README.md`](../android/README.md)
> bundles the same UI, owns the sealed token, and pins an app-layer E2EE host
> key. A Kotlin debug APK has been built and smoke-tested on `dfphone`, including
> QR deep-link pairing and encrypted streaming; see the
> native evidence.
> The native UI-24 chat run (create, prompt/reply, rotation,
> background/foreground, session switch, daemon restart) is recorded there; a
> real-phone Tailscale run is still open.

Desktop width reasoning does not see the phone: the layout, the safe areas, the
soft keyboard, the device pixel ratio, and the paint path are all different. The
Pixel 5 emulator described here found four defects that no desktop test caught
(fractional-DPR glyph clipping, a double-counted safe-area inset, a blank shell
attach, and a session title row that a live session never needs).

This is the procedure. The mechanical parts (boot, reverse, shot, tap, type) and
the two automated regression probes live in `test/android/`, but the loop itself
is still `screencap` → look → `tap`: the coordinates come from the screenshot just
taken, and nothing in a script can see a clipped glyph or a keyboard that covers
the pane. `test/android/README.md` covers the harness.

## One-time setup

```bash
export ANDROID_SDK_ROOT=$HOME/Android/Sdk ANDROID_HOME=$HOME/Android/Sdk
mkdir -p "$ANDROID_SDK_ROOT/cmdline-tools"
# commandlinetools-linux-<build>_latest.zip from dl.google.com/android/repository
unzip -q commandlinetools-linux-*_latest.zip -d "$ANDROID_SDK_ROOT/cmdline-tools"
mv "$ANDROID_SDK_ROOT/cmdline-tools/cmdline-tools" "$ANDROID_SDK_ROOT/cmdline-tools/latest"

yes | "$ANDROID_SDK_ROOT/cmdline-tools/latest/bin/sdkmanager" --licenses
"$ANDROID_SDK_ROOT/cmdline-tools/latest/bin/sdkmanager" --install \
  platform-tools emulator platforms\;android-34 system-images\;android-34\;google_apis\;x86_64
```

Roughly 5.4 GB under `~/Android/Sdk`, plus 1.8 GB per AVD under
`~/.android/avd`.

```bash
echo no | avdmanager create avd -n dfphone \
  -k 'system-images;android-34;google_apis;x86_64' -d pixel_5 --force
printf 'hw.keyboard=no\n' >> ~/.android/avd/dfphone.avd/config.ini
```

**`hw.keyboard=no` is the step people skip.** The Pixel 5 profile defaults to a
hardware keyboard, and Android suppresses the soft keyboard while one is
attached, so the compose row and the `visualViewport` shrink path appear broken
or untestable. Without it, every keyboard result is a lie. (The runtime
equivalent is `adb shell settings put secure show_ime_with_hard_keyboard 1`.)

## Start

```bash
export PATH=$PATH:$HOME/Android/Sdk/platform-tools:$HOME/Android/Sdk/emulator
emulator -avd dfphone -no-window -no-audio -no-snapshot -no-boot-anim \
  -gpu swiftshader_indirect -accel on -memory 3072 &

until [ "$(adb shell getprop sys.boot_completed 2>/dev/null | tr -d '\r')" = 1 ]; do sleep 5; done
adb reverse tcp:8787 tcp:8787     # loopback on the device -> the daemon on the host
```

`-no-window` is fine: `adb exec-out screencap -p > shot.png` is the display.
`adb reverse` is why this needs no `--allow-remote`, no TLS, and no pairing: the
page is served from `127.0.0.1:8787` from the device's point of view, exactly
like the desktop browser.

Firefox is the browser worth testing (Gecko's viewport and safe-area behaviour
differs from Chrome's). Official builds:

```
https://archive.mozilla.org/pub/fenix/releases/<ver>/android/fenix-<ver>-android-x86_64/fenix-<ver>.multi.android-x86_64.apk
```

```bash
adb install -r -g firefox.apk
adb shell am start -a android.intent.action.VIEW -d "http://127.0.0.1:8787/" -n org.mozilla.firefox/.App
```

## Non-device gates

Two checks run the client's real Java classes without an emulator, so a broken
v2 byte layout fails in seconds instead of on a phone:

```sh
make android-pq-check   # Keccak/X25519/ML-KEM vs Go vectors; bundle tamper; transcripts, keys, AADs, fingerprintV2
make android-pq-live    # disposable --e2ee-min-suite=2 daemon: pair, sealed token, encrypted /state, downgrade + replay refusal
```

They do not replace the smoke test below: no gate here proves the WebView, the
Keystore, the notification service, or a real radio path.

## Native app smoke test

Build with JDK 21, SDK platform 36 and Build-Tools 36.0.0:

```sh
export JAVA_HOME="$HOME/.local/opt/jdk-21.0.13+9"
export ANDROID_HOME="$HOME/Android/Sdk"
export ANDROID_SDK_ROOT="$ANDROID_HOME"
./android/gradlew -p android assembleDebug
adb reverse tcp:8787 tcp:8787
# Pair in the app with http://127.0.0.1:8787 and a fresh Pair phone code.
adb install -r android/app/build/outputs/apk/debug/app-debug.apk
adb shell am start -n dev.darkfactory.mobile/.MainActivity
```

The acceptance sequence is: pair once (by typed URL/code or by scanning the
desktop QR with the phone camera), compare the native host-key fingerprint and
the suite label (`hybrid X25519 + ML-KEM-768` or `P-256 (legacy)`) with the
desktop **Pair phone** dialog, load the fleet from the bundled UI,
create/attach a PTY, send input, stream output through `/api/v1/e2ee`, open an
OpenCode chat, rotate and background/foreground the app, verify the token is
absent from WebView storage and logs, revoke the device from the desktop, and
confirm the app returns to the native pair screen without discarding the saved
host. Then start a fresh daemon with a different host key while the app is
stopped and confirm the saved pin is refused before the WebView loads and the
host row survives; Forget is the only action that removes it. A `200` from
`/state` alone is not evidence. Record screenshots and the exact commands in a
dated artifact folder.

For a v2 device, also start the daemon with `--e2ee-min-suite=2` and confirm a
v1-only client is refused at pairing and at the envelope. That test belongs on a
disposable XDG root, not the operator's daemon. The 2026-11-09 emulator run did
this on `dfphone` and recorded the result in
`test/artifacts/e2ee-v2-android/`;
it paired over v2, loaded `/state`, and kept the pin after the host identity
changed, but did not exercise PTY streaming under v2.

The QR path can be exercised without a camera by sending the same URI:

```sh
code=ABCD-EFGH
uri="darkfactory://pair?base=http%3A%2F%2F127.0.0.1%3A8792&code=$code"
adb shell "am start -a android.intent.action.VIEW -d '$uri' dev.darkfactory.mobile"
```

Quote the whole remote command: `adb shell` re-parses it, and an unquoted `&`
truncates the intent at the base URL. The app prefills the form but must still
show the fingerprint dialog before saving the device.

The historical Firefox procedure follows. It is no longer a remote-client
acceptance path.

## Sign in without printing the token

```bash
adb shell input tap 540 1037          # the token field
TOKEN=$(cat "$HOME/.config/darkfactory/token")
adb shell input text "$TOKEN" >/dev/null 2>&1
adb shell input keyevent 4            # dismiss the keyboard
adb shell input tap 540 1277          # Connect
```

Keep the token in a variable read from the file. Never let it reach the command
line you show, and redirect `adb` output: `am start` echoes the intent URL, which
for `darkfactory open --no-browser` would include the one-time code.

## “This device is read-only”

This is the read-only scope, not a failed PTY. Workspace access is one
capability: a full-scope device gets the terminal and OpenCode writes together,
because an OpenCode turn can already run workspace actions. A read-only device
can see the fleet, state, runs and chat transcripts, but the daemon refuses
every PTY route and every OpenCode mutation. A session’s blocked panel carries
**Open chat**, so the worktree-scoped transcript stays reachable even though
the phone hides the session head and the terminal’s own Chat control.

If the panel says **read-only**, the host must pair the device again with
**Workspace (terminal + chats)**; the phone cannot grant it to itself:

1. On the host browser, open **Pair phone** and choose **Workspace**.
2. Let the dialog generate a new code and QR.
3. On the phone, tap **Disconnect** and open the new pairing link (or enter the
   new code). Reusing the old link/code keeps the old scope.
4. In **Devices**, revoke the old read-only row. A full-scope row now reports
   `terminal: true` for compatibility, but scope is the authority.

A full-scope device created before the workspace change is treated as
workspace on the next request; the stored `terminal` flag is no longer an
independent grant. A pasted master token is a different credential: it always
has workspace access and is intended for a local test, not as the phone’s
long-lived pairing.

## Driving it

```bash
adb exec-out screencap -p > /tmp/s.png     # 1080x2340 on this AVD
adb shell input tap X Y                    # device pixels, not display pixels
adb shell input text 'echo%shello'         # %s is a literal space
adb shell input keyevent 66                # Enter (sends the compose row)
adb shell input keyevent 4                 # Back: dismisses the keyboard
```

Two traps, both of which cost a real detour:

- **Coordinates.** The screenshot is 1080×2340, but image viewers render it
  smaller (around 920 wide), so a tap target read off the image is ~15% off on
  both axes. Scale it: `x × 1080/displayedWidth`, `y × 2340/displayedHeight`.
  That is survivable for a 270 px nav button and not for a 30 px `×`.
- **Spaces.** `adb shell input text` silently drops them, so `echo a b` arrives
  as `echo`. Use `%s`.

## Read the client's own log

There is no remote debugging console, so the page posts its diagnostics:

```bash
./bin/darkfactory serve --debug          # also mirrors to debug.log
```

Load `http://127.0.0.1:8787/?debug=1` and the `client-debug` lines land in the
server log: `viewport sync inner=… vv=… top=… dpr=…`, the renderer choice,
attach and redraw decisions, and the element under the last tap. That log is how
the fractional DPR and the exact viewport numbers were found, not guesswork.

`?debug=1&panel=0` keeps every log line and its POST but leaves the floating
panel hidden. The panel covers the lower half of the terminal, so a screenshot
cannot tell its ink from the pane's: hide it before comparing captures.

A black pane has two different causes, and the attach lines tell them apart:

```
attach <id> redraw=1          # the server suppressed the replay; a redraw is needed
first chunk 4 bytes           # the response body started (a 0-byte attach never resolves fetch)
screen 51x28 type=normal len=28 vp=0/0 rows=28 box=396x543
```

`rows=0` with a non-zero first chunk means the bytes never reached xterm's
buffer. `rows>0` with a black screenshot means the buffer has text the renderer
never painted. `stream open` with no `stream headers` means the response never
became readable at all — see `test/android/README.md` and substrate lesson 15.

## What to test

The cases below are the phone-specific ones: every entry is something the desktop
cannot exercise, or something that has already broken here. **M** means manual
only (read the screenshot and the log), **P** means a probe in `test/android/`
covers it: `P:pane` = `probe-pane.sh`, `P:oc` = `probe-opencode.sh`.

1. **Sign-in without zoom** (M). Tap the token field: no zoom, no clipped
   glyphs, Connect reachable with the keyboard up. Pass: `viewport sync` reports
   `dpr=2.727…`, `inner=396x719`, `top=0` with the keyboard down.
2. **Mobile default view** (M). A cold load lands on Fleet; no attach line is
   logged and no terminal bytes arrive until the terminal is opened. Pass: no
   `stream open` in the log while staying on Fleet.
3. **Session switching keeps the pane** (P:pane). Fleet → Session, and session
   tab → session tab. Pass: the pane still has ink after the switch.
4. **Terminal touch scroll** (M). Drag a finger vertically on the pane. Pass: a
   shell scrolls its scrollback, a mouse-aware TUI gets wheel reports
   (`tui wheel up`/`down` in the log), an alt-screen pager gets arrow keys. The
   pan must not raise the keyboard.
5. **Tap-to-focus** (M). A confirmed tap focuses the terminal and raises the
   keyboard; a drag that is a scroll must not (`df_keyboard_shown` stays false).
6. **Keyboard does not bury the pane** (M). With the keyboard up the compose row
   and the terminal stay above it, the bottom nav is reachable, and the page
   itself does not scroll — only the pane does.
7. **↑ / ↓ / ⇣** (M). On a TUI they send PageUp/PageDown/End rather than arrow
   keys; on a shell they page the local scrollback and ⇣ jumps to the bottom.
8. **Renderer choice** (M). On this fractional-DPR AVD the log says
   `webgl skipped: fractional dpr 2.727`. `webgl renderer` here is a regression;
   a lost context must log the fallback and keep painting.
9. **OpenCode panel loads** (P:oc). Opening the chat from a worktree, or loading
   `?chat=1` with a worktree present, shows a native chat, not an xterm: the
   page reports the picker and message-list boxes, the list is laid out
   (>200 px tall), and no `stream open` is logged. The chat is scoped to that
   worktree; changing worktree changes the list.
10. **Chat picker switch** (P:oc). Changing chat through the native select
    switches the panel and closes the popup; the next tap must land on the page
    and not be swallowed by an orphaned picker.
11. **Jump to Latest** (P:oc). Scroll up more than ~90 px: the button appears.
    Tap it: the list jumps to the bottom (`to` within 4 px of `max`) and the
    button hides again.
12. **Permission requests are answerable** (M). A pending OpenCode permission
    shows `once`/`always`/`reject`, and the chosen answer unblocks the agent. An
    invisible or unanswerable prompt stalls the agent with no other symptom.
13. **Debug panel stays hidden** (M). With `?debug=1&panel=0` the `client-debug`
    lines still reach the server log while the panel covers no part of the pane.
14. **Chat mode** (M). Tap Chat in the chat header: the fleet tree, topbar and
    bottom nav go away and the conversation keeps its safe-area padding; the
    picker still switches chats. Exit brings the nav back, and a reload returns
    the tab to the layout it chose. The terminal's Focus button is unaffected.
15. **Markdown and commands** (M). An assistant reply with a heading, list,
    table, fenced code and link renders each as an element, while literal
    `<img onerror=…>` in the reply stays text. Typing `/` opens the command list
    above the composer; ↑/↓ and Enter insert without sending; sending it runs the
    command, and a name OpenCode does not have is sent as a normal message.
16. **Fleet reorder** (P:gate). In Fleet, drag a repo header grip above another
    repo; the group moves and stays moved after a reload. Drag a one-workspace
    repo’s row grip: its whole group moves (the row is not a dead handle). In a
    repo with several workspaces, drag a row grip: only that row moves inside
    its repo. Focus a grip and press ↑/↓: it moves one place. `make ui-gate`
    asserts this at desktop, narrow and phone widths, with mouse and touch, and
    checks every grip is at least 16×16 px.
17. **Notifications** (M). After pairing, grant notification permission,
    background the app, and end a session (or create an OpenCode permission
    request). Within one poll interval the phone shows “Agent finished” /
    “Permission requested”; the persistent “Watching …” notification stays. Tap
    **Stop** in that notification and confirm the service stops; open the app
    again to restart it. Deny the notification permission and confirm the app
    still connects, with only the persistent service notification (if the OS
    shows it).

Cases 4, 5, 7, 12, 14, 15 and 17 have no probe: they need a real finger and a
real keyboard, and 12/17 need an agent or a state transition. Cases 4, 5 and 7 were
last executed by hand on 2026-11-01; the screenshots and log lines are in
`test/artifacts/client-foundation/terminal-phone/`.
Run 1–17 after any change to `web/app.js` layout, viewport, touch, reorder, the
OpenCode panel, or the Android notification service, and at minimum the two
probes before merging those changes.

## Regression probe

```bash
test/android/probe-pane.sh                 # Fleet -> Session round trip
test/android/probe-pane.sh -t 470,505      # switch to the session tab at that pixel
test/android/probe-opencode.sh             # chat picker + jump to latest
test/android/probe-opencode.sh picker      # only the picker
test/android/probe-opencode.sh latest      # only the Latest button
```

`probe-pane.sh` captures, performs the gesture, captures again, and fails (exit
3) if the pane lost its ink, or (exit 2) if the gesture attached no session at
all — a stale tap point that opens a dialog changes the screenshot without
exercising the pane.

`probe-opencode.sh` reads the device-pixel boxes the page logs (`oc picker box …`,
`oc messages box …`, `oc latest box …`) instead of hard-coding coordinates, so it
survives layout changes. It fails (exit 2) when the gesture never took effect and
(exit 3) when a control stayed stuck — a picker that never closes, a Latest
button that stays visible. Neither probe replaces looking at the image.

Three regressions run headlessly against a real daemon and Chromium, for the
client/HTTP boundary only: `test/browser/probe.sh attach` (a click still reaches
a TUI after attach, tab switch, and reload), `test/browser/probe.sh session`
(the session-tab `+` creates a live PTY that accepts input), and
`test/browser/probe.sh opencode` (many OpenCode chats switched by click and by
picker, each repainting its own transcript). See
[`../test/browser/README.md`](../test/browser/README.md). They do not replace
the phone cases below.

Three traps they handle, all learned the hard way: a focused terminal raises the
soft keyboard and moves the bottom nav up ~780 px, so `df_nav` hides the
keyboard first; the bottom nav can be tapped by fraction of the device screen
while session tabs cannot, because their widths depend on the label; and the
picker popup belongs to the browser UI, so the probe moves through it with
DPAD (`df_key 20`) and Enter rather than tapping row geometry it cannot see.

## Baseline to compare against

Pixel 5 AVD, 1080×2340 at 440 dpi, Firefox for Android:

```
inner = 396x719 CSS px      visualViewport = 396x719, offsetTop 0
dpr   = 2.727272727272727   (fractional; the DOM renderer, not WebGL)
layout viewport stops above the Android navigation bar
```

`innerHeight == visualViewport.height` on Android, which is why the phone uses
the layout viewport and iOS uses the visual one. A regression shows up as a
different `inner`, a `top` that is not 0 with the keyboard down, or `webgl
renderer` in the log on a fractional DPR.

## What it cannot test

- **iOS and Safari.** There is no iOS simulator on Linux. Safe-area insets,
  `-webkit-touch-callout` behaviour, and the keyboard animation that needs the
  settle poll have to be checked on the iPad itself.
- **Real system chrome.** The emulator's navigation bar follows the system
  theme, so a light bar under a dark app is an emulator artefact. A real
  gesture bar and a real notch behave differently.
- **The pairing path.** `adb reverse` short-circuits Tailscale, `publicURL`, and
  the HTTPS requirement on `/pair`. Testing a phone that is genuinely remote
  needs the tailnet.
- **GPU drivers.** `swiftshader_indirect` is software rendering. A glyph-clipping
  bug that only appears with a vendor driver will not reproduce here.

## Stop

```bash
adb emu kill
```

`adb reverse` survives until the server restarts; re-run it after `adb kill-server`.
