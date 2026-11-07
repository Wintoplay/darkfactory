# Third-party assets

Everything here is vendored. The binary does not download assets, and the
daemon build does not run npm and the shipped daemon requires no JavaScript
runtime. Development-only browser evaluation dependencies live separately in
`tools/clientbench/`.

## Orca terminal code

The pinned Orca terminal references, MIT license, source hashes, and adaptation
notes are in [orca/README.md](orca/README.md). The adapted mouse-click encoder
has executable tests. It is not yet loaded by the production UI.

## @xterm/xterm 6.0.0

- Upstream: <https://github.com/xtermjs/xterm.js>
- Package: <https://registry.npmjs.org/@xterm/xterm/-/xterm-6.0.0.tgz>
- npm integrity: `sha512-TQwDdQGtwwDt+2cgKDLn0IRaSxYu1tSUjgKarSDkUM0ZNiSRXFpjxEsvc/Zgc5kq5omJ+V0a8/kIM2WD3sMOYg==`
- License: MIT (`web/vendor/LICENSE.xterm`)

Vendored files:

| File | SHA-256 |
|---|---|
| `web/vendor/xterm.js` | `14903579ff54664cd72f8e8699e6961a6272c21863ec1c3b118cdc8af5d4a972` |
| `web/vendor/xterm.css` | `854a7c0fb70e8b1a083c16797ab827299fb18744f5ad34f227b48337e33293c6` |

Only the browser UMD build and its stylesheet are shipped. Source maps are not
vendored.

## @xterm/addon-fit 0.11.0

- Upstream: <https://github.com/xtermjs/xterm.js/tree/master/addons/addon-fit>
- Package: <https://registry.npmjs.org/@xterm/addon-fit/-/addon-fit-0.11.0.tgz>
- npm integrity: `sha512-jYcgT6xtVYhnhgxh3QgYDnnNMYTcf8ElbxxFzX0IZo+vabQqSPAjC3c1wJrKB5E19VwQei89QCiZZP86DCPF7g==`
- License: MIT (`web/vendor/LICENSE.addon-fit`)

Vendored files:

| File | SHA-256 |
|---|---|
| `web/vendor/addon-fit.js` | `ba3ea256ce0620a0992a197d6c9baea64823fc93d8da07a9e366ca9943c18527` |

## @xterm/addon-webgl 0.19.0

- Upstream: <https://github.com/xtermjs/xterm.js/tree/master/addons/addon-webgl>
- Package: <https://registry.npmjs.org/@xterm/addon-webgl/-/addon-webgl-0.19.0.tgz>
- npm integrity: `sha512-b3fMOsyLVuCeNJWxolACEUED0vm7qC0cy4wRvf3oURSzDTYVQiGPhTnhWZwIHdvC48Y+oLhvYXnY4XDXPoJo6A==`
- License: MIT (`web/vendor/LICENSE.addon-webgl`)

Vendored files:

| File | SHA-256 |
|---|---|
| `web/vendor/addon-webgl.js` | `b85f8d4b3e9756bebb757e3fe47134d70f03ea3d6b187624426d2e2b65dec06c` |

The addon is used on Chromium/Blink and non-Linux WebKit, loaded lazily after
the terminal is created, with the core DOM renderer as the fallback. WebKitGTK
keeps the DOM renderer because its masked GL path was not a win. It is skipped
at a fractional devicePixelRatio, where its cell box rounds and clips glyphs. It
has no runtime dependencies, network calls, `eval`, or dynamic code loading. It
is not loaded from a CDN.

## QRCode for JavaScript

- Upstream: <https://www.d-project.com/> (Kazuhiko Arase; the MIT-licensed
  generator shipped in the local `qrcode-terminal` vendor copy)
- License: MIT (`web/vendor/LICENSE.qrcode`)
- Vendored file: `web/vendor/qrcode.js`
- SHA-256: `4521347e3da91752ca97d47ca34d9acee8f275f99e7e3f670938944b3f3b65d2`

This is a browser-only bundle of the byte-mode generator (`QR8bitByte`,
`QRBitBuffer`, `QRMath`, `QRPolynomial`, `QRRSBlock`, `QRUtil`, `QRCode`). It
renders the pairing URL into a DOM grid; there is no canvas, network call, or
build step. The pairing dialog hides the QR if the script is unavailable.

## Safety notes

- xterm.js injects `<style>` elements. The daemon serves the page with a
  per-response nonce, and `web/app.js` stamps that nonce onto dynamically
  created `<style>` nodes. Do not add `unsafe-inline` to `style-src`.
- OSC 8 links are ignored by the configured link handler. Window manipulation
  options are left at their secure defaults (all disabled). The clipboard addon
  is not vendored.
- `web/app.js` drops output-driven palette and colour sets (OSC 4/10/11/12 and
  the 104/110/111/112 resets) before the bytes reach the emulator, because a
  process that repaints the theme can hide text or impersonate another agent.
  Colour queries are still forwarded. `make ui-gate` asserts this in
  `test/browser/probe.sh hostile`; keep the filter when upgrading xterm.js.
