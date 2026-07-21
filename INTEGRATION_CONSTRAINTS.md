# INTEGRATION_CONSTRAINTS — Building for existing harnesses (Claude Code first)

**Date:** 2026-07-21
**Context:** We are NOT (only) building our own harness. The graph must integrate into
existing coding-agent harnesses, **starting with Claude Code**, primarily via an **MCP
server**. This doc records the reframe, the decisions taken, and how each integration
problem is resolved. See `INITIAL_RESEARCH.md` for the underlying evidence and `PLAN.md`
for the build sequence.

---

## The central reframe

`INITIAL_RESEARCH.md` quietly assumed we **own the agent loop** (wire tools into the loop,
inject a repo-map, force-route queries, measure everything). Integrating into Claude Code
trades **control** for **influence + hooks**. Most problems below are downstream of that.

**Architecture that falls out of it — two layers, kept strictly separate:**

- **Portable core** — graph engine + MCP server + LSP pool + freshness + telemetry. Works
  in any MCP harness (Claude Code, Cursor, Cline, Windsurf). Correctness lives here.
- **Harness adapter** — Claude-Code-specific glue: hooks, CLAUDE.md snippet, agent defs.
  Other harnesses get their own thin adapter later.

Serena (MCP-wrapped LSP, works in Claude Code + others) is the existence proof.

---

## Decisions (settled)

| # | Topic | Decision | Rationale |
|---|---|---|---|
| 1 | **Staleness / races** | **Top priority.** Solve with a deterministic sync barrier, not just model instruction. | Stale graph is worse than grep (grep is always fresh). |
| 2 | **LSP vs tree-sitter** | **LSP.** Users install the language server. | Correctness first. Precision > zero-install convenience. |
| 3 | **Adoption** | Solve with an always-in-context **search-strategy doc** (when graph vs grep) + strong tool descriptions. Not a hard problem. | Model needs the tradeoff explained, not forced. |
| 4 | **Deny grep/glob** | **Never.** No aggressive mode. | Grep is sometimes the right tool. Keep it available. |
| 5 | **Trust** | Not a concern. Accuracy is the whole point of the system. | Precision + freshness by design. |
| 6 | **Distribution** | Proper **Windows ↔ WSL path handling** from the start. | Differentiator — most tools get this wrong. |
| 7 | **Observability** | **Full stack early**, and a **correctness/eval rig from day one.** | Only way to know if we're actually helping. See `EVAL.md`. |
| 8 | **Daemon language** | **Go.** | Concurrency fit for the staleness barrier + AI-reliable + prod MCP SDK + same lang as tsgo. See `PLAN.md` §Why Go. |
| 9 | **TS analysis** | Out-of-process via **`tsgo --lsp`** (TS 7 native). | In-process TS Language Service is gone in TS 7 → staleness barrier needed for TS from day one (but tsgo is ~10× faster → cheap barrier). |
| 10 | **Eval volume** | Carried by a **local model** (free); Claude arms quota-boxed. | Max quota is the budget; local runs give statistical power + a model-interaction study. See `EVAL.md`. |

---

## Problem map & resolution

**1. Loss of loop control (central).** Recovered *partially* by hooks (see below). Residual:
hooks are event-driven, not true loop control. Accepted.

**2. Adoption / competition with built-in Grep/Read.** Cannot force preference without
deny-listing built-ins (rejected, decision #4). Levers: search-strategy doc always in
context (SessionStart / CLAUDE.md), excellent tool descriptions, subagent tool restriction
where we define agents. Treated as a documentation/positioning problem, not a blocker.

**3. Subagent bypass.** Built-in Explore/Task agents lean on grep/read; MCP tools *are*
inherited by subagents by default, but preference isn't guaranteed. Partial mitigation via
tool restriction on agents we define. Accepted residual risk.

**4. Staleness vs the edit loop → the deterministic barrier.** THE priority (decision #1).
Claude Code's **`PostToolUse` hook blocks the turn until it returns** — a hard sync point
right after each edit, before the next tool call. Three-layer defense:
  1. *Barrier (primary):* blocking `PostToolUse` → daemon control socket → LSP
     `didChange`/`didSave` → wait for settle (timeout) → return. Race closed at source.
  2. *Freshness metadata (safety net):* every result carries `generation` + `stale`; the
     graph never silently returns stale data.
  3. *Model instruction (last resort):* search-strategy doc tells the model how to react to
     `stale: true` (wait / retry / call `graph_status`).
  - Open research spike: **"settle" detection** (no universal LSP "reindex done" signal) —
    in-order-request probe + `$/progress` + diagnostics quiescence + bounded wait.
  - Also need an **FS watcher**: out-of-band edits (git checkout, user's editor) produce
    staleness with no hook to catch.

**5. Cold start / indexing lifecycle.** `SessionStart` hook kicks off background indexing;
tools must degrade gracefully ("index building, use grep for now") — one hang poisons
adoption. Bounded waits everywhere.

**6. Proactive repo-map injection.** `SessionStart` / `UserPromptSubmit` hooks inject text
as a system-reminder (**~10k-char cap**). Good enough for a PageRank top-symbols map; not
the per-task-adaptive context owning the loop would give. Accepted.

**7. Coverage on unknown machines.** With LSP (decision #2) we require the right server
installed per language; degrade gracefully + emit install hints when a server is missing.

**8. Result-size discipline.** Cap/paginate every tool; signatures-not-bodies default.
Non-negotiable — chatty tools re-introduce the bloat we're removing.

**9. Distribution friction.** MCP server process + LSP install + `.mcp.json` (shared) vs
personal config + **WSL/Windows path normalization** (decision #6).

**10. Observability thinner than owning the loop.** `/usage`, `/context`, OTEL export give
token + tool-call counts (enough to A/B tokens). Correctness A/B needs a **separate offline
eval rig** (decision #7, `EVAL.md`).

**11. Moving target.** We're a guest in Claude Code's process; native code-intel may
overlap us; hook/MCP APIs drift. The portable-core/adapter split is the insurance.

---

## Confirmed Claude Code mechanics (surface we build on)

- **Hooks can inject context** via stdout/`additionalContext` (wrapped as system-reminder,
  ~10k-char cap). Relevant events: `SessionStart`, `UserPromptSubmit`, `PreToolUse`,
  **`PostToolUse` (blocking — our barrier)**, `Stop`, `SubagentStop`.
- **MCP tool schemas are deferred by default** (tool-search) — only tool *names* enter
  context until first use. Lean tool surface still preferred, but no heavy per-schema tax.
- **Subagents inherit MCP tools by default**; restrictable via agent-def `tools` /
  `disallowedTools` for agents we define.
- **Cannot force tool preference** without deny-listing built-ins (which we won't do).
- **Observability:** `/usage`, `/context`, OTEL export, per-session token counts.
