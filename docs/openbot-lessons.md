# Lessons from OpenBot (CopilotKit/OpenBot)

**Why this teacher.** Orca taught us reliability and how *not* to build authority.
OpenBot (MIT, ~7.7k stars, TypeScript, v0.2.0) exists to answer the other half
of our question: how an agent gets tools without being trusted with them. Its
one-line thesis is the whole design:

> "Anything a Bot does to a computer, a file, an MCP server or a component goes
> through one gateway that decides and records it. That is the difference
> between an agent that can use your tools and an agent you can let near them."

Companion to [orca-lessons.md](orca-lessons.md) (reliability) and
[agent-authority.md](agent-authority.md) (our threat model and requirements).
This document is the feature review and what we take from it.

- Studied at `72a0b1f`, shallow clone 2026-10-29. No build, no install.
- 1,931 TS/TSX files, 703 test files, releases `v0.0.10` (2026-09-13) →
  `v0.2.0` (2026-10-12). Fast, test-heavy, honest docs.

---

## 1. The shape of the thing

| Piece | What it is |
|---|---|
| `app` | React UI: channels, chat, **live screen**, activity, admin, audit |
| `server` | API, auth, roles, policy, **audit**, credentials, grants, connectors |
| `agent-computer` | Chromium + `/workspace` + file/shell tools, one container per Bot |
| `supervisor` | Creates/stops/resets computers; the only holder of the Docker socket |
| `agent-*` | AG-UI bots (LangGraph, Mastra, CrewAI, Pydantic, ADK, …) |
| PostgreSQL+pgvector | product data, **audit rows**, policy, grants, credentials |

A "coworker" is a durable Bot profile: runtime agent + profile + standing role.
The role is a system message sent with *every* run, plus a deployment-wide
provenance block that "cannot be forgotten from the next coworker somebody adds".
Agents are reachable over [AG-UI], so governance "rides the protocol rather than
the framework" — the same trick that lets us govern any CLI agent.

Numbers that matter for us: 40 example coworkers, ten in the shipped fintech
catalogue; 703 test files against 1,931 sources (~0.36).

---

## 2. The gateway: decide, record, act — in that order

Every side effect a Bot attempts (browser, file, shell, MCP, component) returns
to the **server**, not to the computer. The sequence is fixed:

1. resolve the target from server-held state (never from model arguments);
2. evaluate the current policy;
3. **write an audit row for the decision**;
4. call the computer only if the decision forwards;
5. write a second audit row if a forwarded action fails.

This is the single most important thing to copy. It means:

- the agent never holds a credential that can act on its own;
- the record exists *before* the effect, so a crash cannot produce an
  unrecorded action;
- refusal is a first-class outcome with a named rule, not a silent no-op.

Two refinements that only show up in the code:

- **The gate classifies before it asks.** `activate`, `type`, `write_file`,
  `run_command` are `"write"`; everything else is `"read"`. Read-only paths
  (screenshot, snapshot, read) bypass the gateway entirely — a deliberate
  volume decision, not an oversight, since the trail would otherwise be mostly
  reads.
- **There is a second door, and it is governed separately.** MCP tool calls go
  through `callTool`: grant → resolve server → classify effect → policy →
  rejection row → content inspection → approval → vendor → outcome row. Unlike
  the computer path, the *success* row is written **after** the attempt. That
  is the fix for a real bug: the row used to say "succeeded" before the network
  call had happened. (Their component path is not CEL-gated at all — see
  caveats.)

Our current `POST /sessions/{id}/input` writes bytes straight to a PTY. That is
the equivalent of calling the computer directly. The gateway is the missing
layer.

Note the honest limit of the ordering: the decision insert and the effect are
**not atomic**. A crash between them leaves an "allowed" row with no outcome
row. The ordering is still the right call — acting fails when the insert
fails — but we should say so rather than imply a transaction we do not have.

---

## 3. Policy is data, and it fails closed

Rules are CEL expressions over a field set the *deployment* defines:

```text
tool.name, intent, bot.id, actor.id,
page.url, page.host, element.{ref,role,name,type},
key, command, file.{path,name,extension},
mcp.{server,tool,effect}, initiator.{kind,id}
```

The evaluation order and failure behaviour, verbatim from the gateway header:

> "Three jobs, in this order: 1. Resolve the ref… 2. Ask the policy. Deny beats
> allow, an absent policy denies, and a broken rule denies. 3. Write the row,
> whichever way the decision went, and only then act."

The properties that matter, and whether they are actually tested:

| Property | Tested? |
|---|---|
| absent policy denies | yes |
| empty allow denies | yes |
| deny beats allow (even `allow:["true"]`) | yes |
| malformed deny denies (rule throws) | yes |
| non-boolean deny (string/number/ternary) denies | yes |
| malformed allow does not permit | yes |
| non-boolean allow does not permit | yes |
| deny answering `false` permits | yes |
| malformed *configured* policy stops startup | **no test found** |

That last row is the interesting one: the behaviour exists (`config.ts` throws at
boot) but nothing proves it, so the first refactor can quietly turn it into a
default-allow. When we build `policy.Evaluate`, the gap to close is not the
engine — it is the boot-time test.

Two implementation details worth copying:

- `contains()` is case-insensitive and `matches()` compiles a regex whose invalid
  pattern **throws**, which the deny path treats as deny. "Anything other than a
  boolean is a broken rule."
- Policy lives in one row (`id='current'`) and is re-read on NOTIFY and on
  reconnect. There is **no policy version and no history**, so an audit row
  cannot say which rules were in force at the time. We should add
  `policy_version` to every ledger row — see S6.

Also note what policy is *not*: it is not the model's prompt, and it is not the
agent's config. An agent cannot widen its own policy, and a prompt cannot talk
its way past a rule.

---

## 4. What gets recorded, and the actor/initiator split

Every action records **whose authority** it ran on (`actor`) and **what caused
it** (`initiator`):

| `initiator.kind` | Meaning |
|---|---|
| `person` | somebody was in the room (the default) |
| `deployment` | start-up, or refusing an unidentifiable caller |
| `routine` | a schedule fired, nobody there |
| `responsibility` | a standing instruction fired |
| `memory` | a background import |
| `handoff` | another Bot passed the work on |

The Audit screen's **"Nobody watching"** filter is exactly `routine + handoff +
responsibility + memory`. That is the query our overseer needs: *what ran on my
authority while I was away*. `deployment` is deliberately excluded, because "a
boundary held at start-up is not work done on anybody's behalf" — a lovely
distinction that keeps the column from overclaiming.

Redaction is explicit: credential plaintext, command output, file bodies, page
text, and secret values never land in the trail (a secret records a character
count). Exports are themselves audited (`audit.exported`), capped at 5,000 rows
with a truncation header, and CSV cells starting with `= + - @` are prefixed so a
chosen name cannot execute in a spreadsheet.

Our R6 ledger is right in spirit but thinner: no initiator dimension, no
redaction policy, no export discipline.

---

## 5. The computer: a jail with a vocabulary, not a root shell

Per Bot, the supervisor creates one container and two labelled volumes
(`profile`, `workspace`) from `POST /computers/:botId/{ensure,stop,reset}`:

```ts
SecurityOpt: ["no-new-privileges:true"]
CapDrop: ["ALL"]
PidsLimit: 512
```

Three design choices worth stealing:

1. **The caller never supplies a name.** Names are derived from the Bot id;
   there is "no passthrough". A compromised server cannot ask for
   `--privileged` or mount `/`.
2. **Refuse, don't adopt.** If a container name is taken but unowned, the
   supervisor returns 409 rather than adopting it.
3. **Reset deletes the profile, not the workspace.** Credentials are disposable;
   the user's files are not.

And the honesty, which I value most:

> "The shared secret is not the boundary; **the vocabulary is**."
> "This process is not a security boundary." (agent-computer sessions)
> "It is not a containment boundary against a Bot's own shell." (chart)

They keep the surface small (four verbs, no passthrough) precisely because the
credential is weak. That is the correct posture for us too: our capability token
is not the security boundary — the small verb set is.

---

## 6. Egress: deny until told, and say what it cannot stop

Four modes per Bot: `allow_all`, `defaults_plus_allowlist`, `allowlist_only`,
`deny_all` (what a Bot gets when its owner's network access is off).

> "No network policy has reached this computer yet, so nothing may leave it."

Until a policy is pushed, everything is refused. Cloud metadata
(`169.254/16`, `100.100.100.200`, `fd00:ec2::254`, NAT64 and IPv4-mapped forms)
is refused in **every** mode. The policy is re-pushed on wake and every 30 s.

Then the limit, stated plainly:

> "WHAT THIS CANNOT STOP: a process that ignores proxy variables and opens a raw
> socket. That is the Kubernetes NetworkPolicy's job."

For us the analogue is a systemd `IPAddressDeny=any` + `IPAddressAllow=` list,
which *does* stop raw sockets — cheaper than a proxy, harder to bypass. Their
honesty is still the right model: every control ships with a sentence saying
what it does not cover.

---

## 7. Handover: the human brake, as a state machine

```text
bot --requestHelp--> waiting --take--> taken(holder=human) --release--> completed
                       |--cancel--> cancelled      |--TTL 10min--> expired
                       +--interrupt--> interrupted (keepPage on restart)
```

- While a person holds control, **Bot actions are refused (409), not queued**,
  and a barrier drains in-flight work (30 s) before the human takes over.
- Runs with no browser attached **pause** instead (`HeadlessToolSuspension`),
  then resume with a continuation.
- A help request survives a restart: `.control/<botId>.json`, fsync + rename,
  `0600`, and on reload an active request becomes `interrupted` with the page it
  was on, so the human is never handed a blank browser.
- An audit row is written for each transition, including the page.

This is the approval mechanism our `docs/agent-authority.md` needs, and it is
better than what I proposed: refusing rather than queueing means the agent can
never "win the race" against the human.

One detail to copy verbatim: **a help request records the page it was made on**,
so control resumes where the problem is.

---

## 8. Delegation: typed envelopes, depth from the assertion

`message_bot` carries `{bot, task, constraints?, expecting?}` — typed, because
"free text is the commonest way a handoff goes quietly wrong".

Four things the deployment decides, never the model:

- **who is addressed** — resolved against the asking person's roster; "the same
  answer whether it does not exist or is not theirs to see" (no enumeration);
- **where the answer lands** — from the signed assertion, so a Bot cannot drop a
  turn into a conversation it was never part of;
- **who is asking** — stamped from the row the deployment wrote;
- **how deep** — from the signed assertion; `BOT_HANDOFF_MAX_DEPTH=1`,
  `BOT_HANDOFF_MAX_PER_RUN=3`. That is what stops A→B→C→A forever.

The last one is the direct answer to "a rogue agent cannot control everything":
delegation depth and fan-out are properties of a signed envelope, not something
the agent can increment about itself.

---

## 9. Capability comes from grants, not from prose

A coworker's tool set is resolved from grants (`plugin_grants`, per-component
grants, skills). The config file says it best:

> "The tools decide what is connected, not this prompt."

The comment behind it is the lesson: a prompt that said "when nothing is
connected, say so" became wrong the moment a Drive connector was granted — the
Bot called the tool, got a listing, and then denied having access, "because a
flat instruction to deny outranks evidence the prompt never mentions".

The Recorder shows the same discipline in token form: its bearer "cannot run a
Bot or read a conversation", a capture pass is issued per connected browser, and
each person gets their own Learning space. Narrow capability, bound to a
person, useless if stolen.

For us: a worker's capability is a *grant set*, and the prompt never gets to
claim a tool it was not granted.

---

## 10. Copy / adapt / refuse

| OpenBot | Verdict for DarkFactory |
|---|---|
| One gateway; decide → **record** → act | **Copy** — this is the missing layer in our API |
| Policy as data, deny-before-allow, four fail-closed properties | **Copy** — with tests per property |
| Actor vs initiator, "Nobody watching" filter, audited exports | **Copy** — cheaper than it looks, high value |
| Small verb vocabulary as the real boundary; names derived, never supplied | **Copy** |
| Honest "not a security boundary" labels next to each control | **Copy** |
| Handover state machine; refuse-not-queue while a human holds control | **Copy** — better than my approval-queue design |
| Typed delegation envelope, depth/fan-out from a signed assertion | **Copy** |
| Grants decide tools; the prompt never grants | **Copy** |
| Container per agent with Chromium | **Adapt** — T1 systemd jail; containers only for untrusted repos or browser automation |
| Egress via proxy with four modes | **Adapt** — systemd `IPAddressDeny` (also stops raw sockets) |
| `OPENBOT_SINGLE_USER` | **Refuse** — our default is single-user *by binding*, not by a flag that can be flipped |
| Next.js-style multi-service stack (server, app, worker, PG, supervisor, Intelligence) | **Refuse** — one Go binary is our advantage |

The last row is the real difference. OpenBot is a fleet platform: six services,
a database, a browser runtime, an external memory plane. We are one process that
owns PTYs. We should take the *governance semantics* and leave the topology.

### The copy budget

Concretely, what "copy" means in lines of Go. If we ever exceed this, we are
porting OpenBot rather than learning from it.

| Take | Where | ~LOC |
|---|---|---|
| Gateway order: resolve → decide → record → act, as one `guard()` in front of every mutating handler | `internal/httpapi` | 100 |
| `Event{Actor, Initiator, Verb, Target, Decision, Rule, PrevHash}` — the two-column shape, even in a JSONL file | `internal/audit` | 60 |
| Named refusals `{code, rule, target}` instead of prose errors | `internal/policy` | 30 |
| Fail-closed rule table with four tests (no CEL — named predicates are enough until users author rules) | `internal/policy` | 150 |
| Handover state machine (`waiting/taken/expired/interrupted`) | `internal/session` | 120 |
| HMAC envelope carrying `depth`, `fanout`, `initiator` | `internal/auth` | 80 |

Total ≈ 540 lines, ~30% of the current tree — and only ~190 of it is needed
before a second principal exists.

**Never:** the container runtime, Chromium, Postgres schema, the six-service
split, AG-UI, components, the Recorder, multi-tenant roles, Intelligence.

---

## 11. What this changes in our requirements

`docs/agent-authority.md` is updated with these. The deltas:

1. A new **S0**: every side effect goes through one gateway in the daemon;
   decide → record → act. This is a prerequisite for S4–S9, not an alternative.
2. **S6 (ledger)** gains `initiator` alongside `actor`, a redaction policy, and
   "export is itself an event".
3. **S8 (approval)** becomes a *handover state machine* with `waiting / taken /
   expired / interrupted`, refusing rather than queueing while a human holds it.
4. **S11 (auto-allow)** must be expressed as policy data, with four fail-closed
   tests, not as `if` statements in a handler.
5. **S12 (budgets)** gains delegation depth and fan-out from a signed envelope.

Cost stays where the previous note put it: the gateway and policy engine are
code, not infrastructure — a few hundred lines of Go and a table. The container
tier is the only expensive part, and we are not taking it.

---

## 12. Caveats

- OpenBot is **beta** (its own word) and 22 days from `v0.0.10` to `v0.2.0`;
  APIs move. We are taking patterns, not code.
- Its governance covers what flows through the gateway. A Bot that opens a raw
  socket from its shell is out of scope by their own admission — same limit we
  will have until the jail denies egress at the kernel level.
- Their policy field set is browser-shaped (`page.*`, `element.*`). Ours is
  terminal-shaped: `session.*`, `worktree.*`, `command.*`, `repo.*`.
