# Terminal engine evaluation

This is a research plan, not a decision to replace xterm.js. The
[terminal surface audit](reviews/terminal-surface-audit-2026-11-02.md) records
what the current client enables, the gaps that matter for hostile output, and
the assertion set every candidate has to pass. Keep the daemon and detached
session host unchanged. Compare the current xterm.js configuration
with pinned builds of ghostty-web and riotermjs in isolated clients. Record
their sources, licenses, transitive dependencies, build steps, checksums, and
known advisories before testing. Vendored production assets must not require
network access at runtime.

Use the same daemon revision, workloads, terminal dimensions, font, scrollback,
and device for each candidate. Randomize run order and record versions, GPU
backend, hardware acceleration or software fallback, device temperature, and
power state. Report missing data and failures alongside measurements.

Measure interactive typing to visible echo, first output, reconnect, resize,
heavy-output completion, memory, idle resource use, and phone battery impact.
`test/browser/probe.sh echo` already reports keydown-to-visible-echo segments
(see [client validation](client-validation.md)); extend it rather than writing a
second harness.
Exercise real agent TUIs and an actual Android device as well as Linux browser
and Wails clients. Synthetic parser throughput is not user-visible latency.

Test attach ordering, first byte, one resize owner, TUI redraw, alternate
screen, touch scrolling, mouse input, keyboard layouts, IME, selection, and
accessibility. Feed hostile escape sequences and bounded oversized output.
Check clipboard, external links, window operations, resource limits, and CSP.
Preserve or strengthen the existing controls and validate their behavior.

Select a candidate only after comparable evidence shows a material benefit,
acceptable security and maintenance cost, and no release-blocking regressions.
Rust or GPU usage alone is not a security or performance result.
