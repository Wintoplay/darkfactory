# Feature leverage: what to launch, what to speak, what to copy

The question: how do we keep adding features at the lowest marginal cost —
depend on Hermes, launch Hermes, follow OpenBot, or build our own?

The short answer: **launch products, speak protocols, copy designs — and never
depend on any of them.** Everything else follows from one boundary rule.

---

## 1. The cost model

Five ways to get a feature, ordered by what they cost *after* the first release.

| Mode | First cost | Marginal cost | Breaks when | Example |
|---|---|---|---|---|
| **Depend** (library, service) | low | **negative** | upstream changes an API | importing Hermes as a Python lib; running OpenBot's Postgres schema |
| **Fork** | medium | **you own it** | never — you are the upstream | vendoring Hermes |
| **Launch** (process) | very low | **≈ 0** | upstream changes argv (rare) | `hermes --tui` in a PTY |
| **Speak** (protocol) | medium | **≈ 0 per provider** | protocol version bump (rare, negotiated) | OSC 7501 status, MCP tools, ACP |
| **Copy** (design) | medium | 0 | never | OpenBot's gateway order |

Orca is the empirical proof of the ordering: agent *launch* is ~200 lines, agent
*status detection* is 3,139 PRs and still going. The moment you parse someone
else's TUI instead of launching it, your maintenance becomes theirs.

Depend is the only mode with *negative* leverage: you get a feature today and a
breaking change later, plus a compatibility matrix forever.

---

## 2. The boundary rule

Before asking "build or adopt?", ask **where the feature lives**:

> **Inside one session, the agent owns it. Across sessions, repos, or time, we
> own it.**

| Feature | Lives | Owner |
|---|---|---|
| Memory, skills, subagents, context compression | inside a session | the agent (Hermes, Claude, …) |
| Channels (Telegram, Slack, email) | inside a session | the agent |
| Provider/model choice, auth, retries | inside a session | the agent |
| Worktrees, PTYs, scrollback, restore | across sessions | us |
| Fleet view, approvals, audit, policy | across sessions | us |
| Cross-repo search, release notes, scheduling | across repos/time | us |

This is why "just use Hermes" is not a strategy and "reimplement Hermes" is not
one either: 60% of Hermes is *inside a session*, and we should launch it; the
other 40% is what we exist to do.

---

## 3. Tier A — launch it

Anything that is a CLI or TUI is a tenant. Our contract is four fields:

```go
type Preset struct {
    Command string
    Args    []string
    Env     map[string]string
    Dir     string
}
```

That is already `config.AgentPreset`. Keep it that dumb. Every version-specific
flag we add is a future bug; every output we parse is a future outage.

What this buys, for ~200 lines:

- Hermes's 70+ tools, memory, skills, cron, subagents, 25 messaging platforms.
- Claude/Codex/OpenCode/Gemini's entire feature sets, as they ship them.
- Every future agent, on day one, because the contract is "it runs in a
  terminal".

The ceiling is equally clear: **anything that needs to know what the agent is
doing is outside this tier.** That is what tier B is for.

---

## 4. Tier B — speak its protocol

One adapter, many providers. This is the highest-leverage code we can write,
because it converts an N-vendor integration problem into a 1-protocol problem.

| Protocol | What it replaces | Effort | Status |
|---|---|---|---|
| **OSC 7501** (program status) | per-vendor status scraping | ~100 lines to parse; agents that emit it work for free | Orca's fallback channel |
| **MCP** | per-tool integrations | client is ~300 lines; every MCP server becomes a tool | ubiquity |
| **ACP** | editor/agent integration | client is a JSON-RPC loop over stdio | Hermes ships an ACP server; Zed/VS Code/JetBrains use it |
| **AG-UI** | chat/transcript parsing | event stream | OpenBot's whole bet |

Note where the leverage is: we do **not** need to parse `claude`'s screen to
learn it is waiting, if the agent writes status into its own output (OSC 7501).
We do **not** need a Telegram integration, if Hermes has one and runs in our PTY.

Rule: adopt a protocol only when **two or more** upstreams speak it. One vendor
is a dependency with extra steps.

---

## 5. Tier C — copy the design

Governance is the one area where copying beats depending, and the reason is
structural: OpenBot is TypeScript + Postgres + six services. We are one Go
binary. There is no version of "depend on OpenBot" that costs less than
reimplementing the 400 lines we actually need.

Copy these once, then own them:

- gateway order: resolve → decide → **record** → act;
- four fail-closed policy properties, each with a test;
- `actor` + `initiator` in the ledger;
- handover instead of an approval queue;
- delegation depth from a signed envelope.

What copying does **not** give us: their future fixes. That is the trade. To
compensate, run the ritual in §7.

Orca code may be adapted when it solves a concrete client problem. Preserve
license and copyright notices, record source revision and file paths, and port
the behavior tests alongside the code. Its React Native client depends on its
own backend protocol, so copying the mobile directory does not produce a
DarkFactory client. Evaluate individual components and adapters first.

The cost estimates above are heuristics, not measurements. Launching a CLI
does not make its touch interactions, reconnect behavior, or session controls
work automatically. Those need the phone acceptance evidence in UI-24.

---

## 6. "Should we just use Hermes?"

Tempting, and worth answering precisely, because it is two different questions.

**As a dependency or a fork: no.**

- It is 1.37 GB of repo, Python 3.14 + Node + FFmpeg, ~25k tests, shipped daily.
- Its unit is a session/profile; it has no concept of a worktree fleet.
- Its security model is per-command approval inside the agent, not a boundary
  around it.
- Its 51.7k open issues become our support queue the day we fork or embed it.

**As a tenant: yes, immediately.**

`hermes --tui` inside a PTY gives our users memory, skills, tools, cron,
subagents and channels for the cost of a config entry. Orca already does exactly
this. If we later want structure instead of a terminal, Hermes ships an ACP
server and an API server — that is tier B, not a fork.

The distinction is the whole strategy: **we are not competing with the agent; we
are the place agents run.**

---

## 7. The ritual that replaces upstream updates

If we do not depend on them, we do not get their fixes. So we buy the fixes back
cheaply:

1. **Release-note mining, monthly.** Pull Orca's and Hermes's release notes and
   contributor commits; grep for `fix(terminal|pty|worktree|restore|resize|
   reconnect|env|path)`. Each hit is a candidate test in our suite. Orca's 751
   restore PRs are a free regression list we could never afford to write.
2. **Protocol watch, quarterly.** OSC 7501, ACP, AG-UI, MCP: has adoption crossed
   two upstreams? Then write the adapter.
3. **Dependency budget: 2.** Today: `creack/pty`, `bbolt`, plus `x/sys` indirect.
   Anything that pushes past that needs a written reason, because it is a
   compatibility matrix we have to carry forever.
4. **Preset drift check.** For each tenant we ship, record the version we tested
   and print it in `status`. Do not gate features on it — just make "which
   version is this" answerable in a bug report.

That is roughly one afternoon a month, and it replaces the maintenance we would
otherwise inherit.

---

## 8. Anti-patterns (each one is a real Orca bug class)

- **Parsing a TUI to learn state.** Use a protocol or say `unknown`.
- **Vendoring someone's DB schema.** Copy the columns, not the migrations.
- **Chasing vendor flags.** `--dangerously-skip-permissions` today, renamed
  tomorrow.
- **Reimplementing memory/skills/channels.** That is the agent's job; we would
  do it worse.
- **A per-vendor adapter before a protocol exists.** One vendor is a dependency.
- **Forking a fast-moving upstream** to change 5% of it.

---

## 9. What this means concretely

| Decision | Verdict |
|---|---|
| Agent presets (Hermes, Claude, Codex, …) | **Launch** — keep the 4-field contract |
| Agent status | **Speak** OSC 7501 when present; otherwise `unknown` |
| Memory, skills, subagents, channels | **Never build** — tenant features |
| Worktrees, PTY, restore, resize | **Build** — our layer, our invariants |
| Policy, audit, approvals | **Copy** OpenBot's design once, own it |
| Web UI | **Build**, keep it thin; no renderer project |
| OpenBot as a dependency | **No** — TS/Postgres, beta |
| Hermes as a dependency/fork | **No** — launch it instead |
| Orca/OpenBot/Hermes release notes | **Mine monthly** as a test list |

The one-sentence version: **spend our code on the layer nobody else occupies —
the workspace across sessions — and rent every layer that already has a
maintainer.**
