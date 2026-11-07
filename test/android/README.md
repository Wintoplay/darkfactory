# Android phone client tests

`docs/testing-android.md` is the procedure and the trap list. This directory is
the code for it: the parts of driving the emulator that are worth typing once.

The phone finds bugs the desktop cannot see (fractional DPR, safe areas, the
soft keyboard, the paint path after a view switch), so these checks run against
a real Android emulator with Firefox, not a resized desktop window.

## Files

- `lib.sh` — sourced helpers: `df_boot`, `df_reverse`, `df_shot`, `df_tap`,
  `df_swipe`, `df_type`, `df_key`, `df_open`, `df_nav`, `df_token`, plus
  `df_screen`, `df_serial`, `df_keyboard_shown` / `df_hide_keyboard` for the
  soft-keyboard cases, `df_page_offset` / `df_page_center` / `df_page_tap` for
  tapping a control at the box the page reported, and the `oc_lines` / `oc_rect`
  / `oc_wait` log readers. `df_nav` computes bottom nav targets from the device
  screen, so it does not inherit the scaled screenshot trap; `df_tap` and
  `df_swipe` take device pixels only.
- `page-offset.py` — measures the gap between the page's layout viewport and the
  screen (Firefox's toolbar, 312 px on the Pixel 5 AVD). The page reports boxes
  from `getBoundingClientRect()`, which starts below that toolbar; `adb input
  tap` does not. Stdlib only.
- `check-blank.py` — reports the share of ink in the terminal pane and exits 3
  when the pane is one flat colour. Stdlib only.
- `probe-pane.sh` — the terminal regression probe: capture, switch, capture,
  assert.
- `probe-opencode.sh` — the OpenCode panel probe: switch a chat through the
  mobile picker, and scroll away then jump to latest. It reads the device-pixel
  boxes the page logs (`oc picker box …`) instead of hard-coding coordinates.
- `probe-chat.sh` — the chat round trip: New chat, a prompt with a reply, a
  permission request answered with Allow once, and an interrupted turn. It needs
  a signed-in page and `DF_TOKEN`, and fails with exit 2 when the provider does
  not ask before the shell tool.

The numbered cases each one covers, and the manual-only cases beside them, are in
the "What to test" section of `docs/testing-android.md`.

## Run it

```bash
# daemon, emulator, reverse, Firefox on the page, signed in, a session open
test/android/probe-pane.sh                 # Fleet -> Session round trip
test/android/probe-pane.sh -t 343,515      # or: tap one point (a session tab)
test/android/probe-opencode.sh             # chat picker + jump to latest
test/android/probe-opencode.sh picker      # only the picker
test/android/probe-opencode.sh latest      # only the Latest button
DF_LOG=/path/to/serve.log DF_TOKEN=$(cat ~/.config/darkfactory/token) \
  test/android/probe-chat.sh               # New chat, reply, permission, interrupt
```

`-t` takes device pixels off a fresh screenshot, e.g. the centre of the second
session tab. That is the gesture that used to leave the pane black: switching
to another session, or away to Fleet and back.

One-time setup is in `docs/testing-android.md`; the short version:

```bash
source test/android/lib.sh
df_boot && df_reverse
df_open 'http://127.0.0.1:8787/?debug=1&panel=0'
```

`panel=0` keeps the client log and its POSTs but leaves the floating debug
panel hidden: the panel covers the lower half of the terminal, and a screenshot
check cannot tell its ink from the pane's.

The probe needs the pane already painted (it does not sign in or choose a
session), so a `1-before.png` verdict of `blank` means open a session first,
not that the probe failed.

`probe-chat.sh` is the same contract for the chat panel: it needs the panel open
at `?debug=1&panel=0&chat=1`, already signed in (it never types a token), and a
test OpenCode provider whose policy asks before the `shell` tool. It reads the
daemon log for navigation and talks to the authenticated API only to assert the
chat id, the assistant reply and the interrupted turn. An auto-allowing provider
is a setup failure (exit 2), not a pass.

Firefox's toolbar makes the page and `adb` disagree about y: the page's
`getBoundingClientRect()` starts at the layout viewport, below the toolbar, so
its y values land short by the toolbar height. `df_page_offset` measures that
row off a screenshot and `df_page_tap` adds it; never tap a page box directly.
The x axis needs no correction.

## Reading a failure

`screenshots/` is not kept; every run writes to a fresh directory and prints it.
If the after-shot is blank, the appended `client-debug` lines show whether the
attach was a TUI (no replay, redraw requested), the first chunk size, and the
xterm screen state:

```
attach <id> redraw=1
first chunk 6041 bytes
screen 44x30 type=normal len=30 vp=0/0 rows=17 box=396x719
```

`rows=0` with `first chunk` non-zero means the bytes never reached the buffer.
`rows>0` with a blank screenshot means the buffer has text the renderer never
painted. Both are client bugs; they are not the same bug.

## What it cannot test

iOS/Safari, real system chrome, a genuinely remote phone (adb reverse
short-circuits pairing and Tailscale), and vendor GPU drivers. See the
"What it cannot test" section of `docs/testing-android.md`.
