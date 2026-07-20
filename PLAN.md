# PLAN — Build sequence

**Date:** 2026-07-21
**Reads with:** `INITIAL_RESEARCH.md` (evidence), `INTEGRATION_CONSTRAINTS.md` (decisions),
`EVAL.md` (measurement). This is the how and the order.

---

## Stack decisions
- **Daemon implementation:** TypeScript / Node.
- **First target language:** TypeScript — via the **TS Language Service API in-process**
  (max precision, no separate LSP process), behind a `LanguageProvider` interface. Other
  languages implement the same interface via generic LSP clients later.
- **Eval default model:** Sonnet (cheap iteration); Opus for occasional validation only.

## Architecture

One long-lived **daemon** = the portable core, with **two client faces on one process**.
This shape is forced by the staleness decision: the hook and the model must talk to the
same live LSP/graph state.

```
┌──────────────── Claude Code (harness adapter lives here) ───────────────┐
│  Model loop → built-in Grep/Read/Edit  +  MCP graph tools               │
│  CLAUDE.md search-strategy (always in context)                          │
│  Hooks:  SessionStart → inject PageRank repo-map + graph status         │
│          PostToolUse(Edit|Write) → BLOCKING sync barrier                │
└───────────┬───────────────────────────────────────┬────────────────────┘
   (1) MCP over stdio                    (2) control socket (project-keyed)
      graph tools                           "file X changed: sync + wait"
            │                                          │
            ▼                                          ▼
┌────────────────────── Graph Daemon (portable core) ─────────────────────┐
│  LSP client pool ....... pyright, tsserver, gopls, rust-analyzer …      │
│  Query / graph layer ... definition, refs, type, members, callers…      │
│  Freshness tracker ..... per-file dirty state + monotonic generation    │
│  FS watcher ............ catches out-of-band edits (git, external editor)│
│  Path normalizer ....... WSL ↔ Windows                                  │
│  Telemetry ............. JSONL + OTEL, session + graph_mode tagged       │
└──────────────────────────────────────────────────────────────────────────┘
```

**Cross-cutting principles:** signatures-not-bodies default · symbol-name-path addressing
(not line numbers — offsets shift under edits we didn't observe) · cap/paginate every tool
· never deny grep · bounded waits everywhere · accept honest null results.

---

## The staleness barrier (the hard core — Phase 1)

Three-layer defense (deepest first):
1. **Deterministic barrier (primary):** blocking `PostToolUse` → tiny hook CLI → daemon
   control socket → LSP `didChange`/`didSave` → **wait for settle** → return. The model's
   turn cannot continue until the graph is current.
2. **Freshness metadata (safety net):** every result carries `generation` + `stale`; the
   daemon always knows which files are dirty (LSP processes requests in-order after
   `didChange`, results tied to document version).
3. **Model instruction (last resort):** search-strategy doc — how to react to `stale: true`.

**"Settle" detection research spike** (no universal LSP signal): in-order-request probe on
the edited file + `$/progress` (rust-analyzer) + diagnostics quiescence + **bounded wait
(≤~1–2s) with generation tag**. Never hang the model. Prototype on pyright first,
rust-analyzer last (hardest, most explicit indexing).

---

## Phases

### Phase 0 — Walking skeleton + telemetry spine + Tier A scaffold
- Daemon (TypeScript/Node): MCP (stdio) + control socket, project-keyed path.
- **First language: TypeScript via the TS Language Service API in-process**, behind a
  `LanguageProvider` interface (so polyglot-via-LSP is a later drop-in).
- Tools v0: `find_definition`, `find_references`, `get_outline` (signatures, capped,
  carry `generation` + `stale` fields even if trivially fresh).
- **Path normalizer (WSL ↔ Windows) from the start.**
- **Telemetry spine (full stack, per decision #7):** JSONL event stream + OTEL exporter +
  session/`graph_mode` tagging.
- **Tier A eval scaffold:** retrieval-correctness harness on a pinned TS repo.
- *Exit:* MCP round-trip works; every call is logged; Tier A green on a pinned repo.

### Phase 1 — Staleness barrier + freshness + Tier A live
- Freshness tracker (per-file dirty, monotonic generation).
- Blocking `PostToolUse` hook → control socket → LSP sync → settle detection → return.
- FS watcher for out-of-band edits; debounce/coalesce rapid edits.
- `graph_status` tool; `stale`/`generation` on every result.
- **Tier A stale-correctness tests:** scripted edit sequences assert post-edit correctness
  (the barrier's regression gate).
- *Exit:* no stale reads under scripted edit races; barrier latency within budget.

### Phase 2 — Adoption layer + Tier B v1 (thesis test online) + full observability
- CLAUDE.md **search-strategy** (when graph vs grep, stale-flag protocol) + strong tool
  descriptions.
- `SessionStart` hook: inject PageRank repo-map (≤10k chars) + graph status.
- **Tier B (navigation efficiency):** cheap fixed-question set, two-arm, Sonnet, stratified
  by spread → first affordable graph-vs-baseline signal (tokens-to-answer). See `EVAL.md`.
- **Full observability:** OTEL token join by session_id + live dashboard.
- *Exit:* reproducible token-to-answer delta, sliced by spread.

### Phase 3 — Breadth (tools + languages), eval expands
- Tools: `get_type`, `get_members`, `get_callers`/`get_callees`, `who_imports`/`imports_of`,
  `impact`, `get_source`. Prioritize typed edges (`extends`/`implements`/`type-of`) +
  interface→consumer expansion; bidirectional traversal (per `INITIAL_RESEARCH.md` §4c).
- Languages via LSP registry: pyright, gopls, rust-analyzer (install hints, graceful
  degradation on missing servers).
- **Tier C (task capability):** tiny curated TS task set (~10–20) from Multi-SWE-bench /
  SWE-bench Multilingual TS subset + hand-curated multi-file tasks. Milestone-only, Sonnet
  default, Opus spot-check. See `EVAL.md`.

### Phase 4 — Hardening & portability
- Portable-core audit: zero Claude-Code assumptions in the daemon; **second harness adapter
  (Cursor)** as proof of portability.
- Perf: LSP warmup, big-repo indexing, coalescing, caching.
- Dashboard polish; continuous eval in CI.

---

## Immediate next step
Phase 0 skeleton (TypeScript/Node): daemon (MCP stdio + control socket) + TS Language
Service provider + 3 tools + path normalizer + JSONL telemetry + Tier A scaffold on a
pinned TS repo.
