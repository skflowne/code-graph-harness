# code-graph-harness

Exploring how to introduce **code graphs** (AST + symbol/reference graph) into a coding
agent harness to reduce token usage and improve the model's understanding and correctness
of a codebase — letting the model answer relational questions ("where is this type, what
are its properties, where is it used") by graph lookup instead of expensive text search.

## Status

Planning complete; Phase 0 next. Docs:
- [`INITIAL_RESEARCH.md`](./INITIAL_RESEARCH.md) — evidence: tokens vs correctness, prior art.
- [`INTEGRATION_CONSTRAINTS.md`](./INTEGRATION_CONSTRAINTS.md) — building into Claude Code; decisions + problem map.
- [`EVAL.md`](./EVAL.md) — how we measure whether it actually helps (from day one).
- [`PLAN.md`](./PLAN.md) — architecture + phased build sequence.

## Direction (settled)

- **LSP-backed** deterministic typed symbol graph — correctness first (users install the LSP).
- Ships as a **portable MCP daemon** (works in any MCP harness) + a thin **Claude Code
  adapter** (hooks + CLAUDE.md). Claude Code first.
- **Staleness is the hard problem:** a blocking `PostToolUse` hook is a deterministic sync
  barrier; freshness metadata + model instruction back it up.
- **Never deny grep** — a search-strategy doc (always in context) teaches graph-vs-grep.
- **TypeScript first:** daemon in TS/Node, first target language TS via the TS Language
  Service API in-process (polyglot via LSP later).
- **Budget-shaped eval from day one:** free retrieval-correctness CI gate + cheap
  navigation-efficiency signal + a tiny milestone-only task-capability set (Max quota is the
  real budget). Two-arm delta, stratified by nav spread.
- Windows ↔ WSL path handling as a first-class differentiator.
