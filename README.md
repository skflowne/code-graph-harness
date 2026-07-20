# code-graph-harness

Exploring how to introduce **code graphs** (AST + symbol/reference graph) into a coding
agent harness to reduce token usage and improve the model's understanding and correctness
of a codebase — letting the model answer relational questions ("where is this type, what
are its properties, where is it used") by graph lookup instead of expensive text search.

## Status

Research phase. See [`INITIAL_RESEARCH.md`](./INITIAL_RESEARCH.md) for the findings,
evidence, and recommended build path.

## Direction (from research)

- Deterministic **AST/LSP-derived typed symbol graph** — best evidence for *both* fewer
  tokens and higher correctness.
- Start with **LSP-as-tools**, add **tree-sitter + PageRank repo-map**, persist a
  SQLite/Kùzu graph only when repo-wide queries outgrow LSP.
- Vector/semantic search stays at the **fuzzy discovery edge**, never the correctness core.
