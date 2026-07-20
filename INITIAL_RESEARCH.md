# INITIAL_RESEARCH — Code Graphs in a Coding Agent Harness

**Date:** 2026-07-21
**Question:** Can introducing code graphs (AST + symbol/reference graph) into a coding
agent harness reduce token usage and improve the model's understanding/correctness of a
codebase — by answering relational questions ("where is this type, what are its
properties, where is it used") without expensive text-search exploration?

**Verdict (TL;DR):** Yes, but conditionally. A *deterministic AST/LSP-derived typed
symbol graph* is the one approach with evidence for **both** fewer tokens **and** higher
correctness. Vector/semantic search saves tokens on fuzzy discovery but is the *weakest*
method for correctness. Token savings are real but highly variable (10–90%) depending on
repo size and how much cross-file tracing a task needs. The biggest gains come from
giving the model **less context, precisely** (noise reduction), not from surfacing more.

---

## 1. Why graphs save tokens (the core mechanic)

Text search answers *"what lines contain this string."* The agent must then **read**
surrounding code to turn matches into meaning — disambiguate symbols, follow imports,
resolve which `get()` is the right one. That read-to-understand loop is where tokens go;
benchmarks show the majority of tokens in a "trace this feature" task are spent on
*exploration*, not reasoning.

A graph flips it: relational questions resolve in **one hop** and return a *compact
answer* (symbol + `file:line` + signature) instead of candidate lines the agent must
read. The graph becomes a table-of-contents the agent jumps around in, rather than a
document it scans.

---

## 2. Which questions a graph wins — and which it loses

| Graph wins (relational, exact answer) | Graph loses (fuzzy, conceptual) |
|---|---|
| Where is `X` defined? (go-to-def) | "Where do we handle auth?" |
| Where is `X` used? (find-refs) | "How does the retry logic work?" |
| What type is this expr / symbol? | "Is there anything like a rate limiter?" |
| What properties/methods does this type have? | Anything in comments, strings, config |
| What does it extend / implement? | "Why was it built this way?" |
| Who calls `fn` / what does `fn` call? (call graph) | Cross-cutting business logic |
| What implements this interface? | |
| What imports this module / what does it import? | |
| Blast radius of changing `X` (transitive refs) | |

The three target questions — *where does this type live, what properties, where is it
used* — are the sweet spot (LSP: `documentSymbol` / `typeDefinition` / `references`).

**The trap — the "navigation paradox":** naive graph traversal can cost *more* tokens
than grep. Following call chains blindly pulls in code that's architecturally connected
but semantically irrelevant, and multi-hop traversal burns nodes. Conclusion: **hybrid** —
keep grep/semantic search as the discovery primitive, use the graph for precise
navigation and for *ranking/validating* search results, not as the only lens.

---

## 3. Three layers to build (in value order)

1. **Syntactic (AST) — tree-sitter.** Per-file, no cross-file resolution. Cheap,
   language-broad. Gives: file outlines/symbol trees, precise byte ranges for surgical
   extraction, signature extraction. The "map a file without reading it" and "return just
   this function body" layer.
2. **Semantic symbol graph — the real prize.** Cross-file name resolution: go-to-def,
   find-refs, type-of, members, inheritance. Do **not** hand-roll resolution per language.
   Get it via LSP or a SCIP index (see §5).
3. **Derived graphs** — call graph, import/dependency graph, impact/blast-radius — all
   computed from layer 2.

---

## 4. Evidence — token savings AND correctness

Headline percentages are cherry-picked metrics against favorable baselines. The
denominators matter.

### 4a. grepai's "97%" — real mechanism, misleading number
Vector/semantic search (Ollama `nomic-embed-text`, 100% local), NOT a graph. On
Excalidraw, 5 questions, baseline = grep + 5 subagents:

| Metric | Baseline | grepai |
|---|---|---|
| Fresh input tokens | 51,147 | 1,326 → **the "97%"** |
| Cache read | 5,973,161 | 7,775,888 (**up 30%**) |
| Cache creation | 563,883 | 162,289 |
| **Total cost** | **$6.78** | **$4.92 → real 27.5%** |

Fresh input tokens were only 3.8% of cost. Real savings came from **not launching 5
subagents** (cache-creation −71%). **Correctness was not measured.** Takeaway: legit
token-saver on *discovery*, but 97% is a headline, not a cost or correctness number.

### 4b. CodeGraph — credible method, variance is the lesson
Tree-sitter → SQLite (symbols=nodes; calls/refs/imports=edges) → 3 MCP tools. Method:
Opus 4.7 headless, `--strict-mcp-config`, 4 runs/arm, median, raw numbers published.

| Repo | Files | Token reduction |
|---|---|---|
| Excalidraw | ~640 | ~90% |
| Tokio | ~790 | 86% |
| VS Code | ~10k | 78% |
| Alamofire | ~110 | 64% |
| Django | ~3k | 36% |
| Gin | ~110 | 34% |
| OkHttp | ~645 | **13%** (grep already efficient on a localized query) |

**Gains scale with repo size + cross-file tracing need.** ROI ~1k files, dramatic >5k,
near-zero on small localized queries. **Failure mode (harness-relevant):** the graph only
helps *if the acting agent queries it directly* — delegate exploration to a file-reading
subagent and the graph is bypassed.

### 4c. Correctness — deterministic AST graphs win (arXiv 2601.08773)
AST-derived graph vs LLM-extracted knowledge graph vs vector-only, 45 questions / 3 repos:

| Approach | Correct | Partial | Incorrect |
|---|---|---|---|
| **AST-derived graph (deterministic)** | **43/45** | 2 | 0 |
| LLM-extracted knowledge graph | 38/45 | 5 | 2 |
| Vector-only | 31/45 | 9 | 5 |

- Vector-only collapsed to 6/15 on architectural queries, highest hallucination risk.
- LLM-extracted graph silently **skipped 377 files** (probabilistic indexing gaps).
- AST graph built **~70–300× faster** (2.8s vs 200–880s), ~9× cheaper end-to-end.
- Most valuable edges were **`extends` / `implements` / `injects` (DI)** + **interface→
  consumer expansion** (implement an interface → also pull the interface's upstream
  consumers). This multi-hop architectural tracing is what grep genuinely can't do.
- Recommend **bidirectional traversal** (dependents *and* dependencies).

### 4d. SWE-bench — retrieval sophistication ≠ correctness
- Vector retrieval alone: ~2%.
- Agent-as-retriever (capable LLM + grep/navigation): 50–80%+.
- Dedicated Code Graph Model: ~40% on SWE-bench Lite — *comparable to*, not beating, SOTA.
- Blunt finding: *"advanced agents integrating embedding-based search, graph navigation,
  or specialized file tools do not consistently outperform the basic mini-SWE-agent
  baseline."* A good agent with grep is a strong correctness baseline.

### 4e. Where correctness gains actually come from
**Noise reduction, not "finding more."** CodeGraph's own +1.6 code-review quality claim
was *"not despite reduced context, but because of it — less noise means the model focuses
on what matters."* Fewer, more-precise tokens → better focus.

---

## 5. Build vs. buy

- **LSP-as-tools (Serena's approach).** Wrap real language servers (tsserver/pyright,
  rust-analyzer, gopls, clangd…), expose `definition`, `references`, `documentSymbol`,
  `typeDefinition`, `callHierarchy`, `typeHierarchy`. Highest leverage, lowest effort,
  always fresh, 40+ languages free. Weakness: stateful, slow to warm, one process per
  language, repo-wide queries can be slow. Serena (MIT, ~25k stars) is essentially this.
- **Persisted graph index (CodeGraph / SCIP approach).** tree-sitter → embedded SQLite
  (or KùzuDB for Cypher). Fast repo-wide queries, cross-session cache, great for
  blast-radius. Weakness: goes stale, needs (ideally incremental) re-index. For *precise*
  resolution emit **SCIP** (LSIF successor; `scip-typescript`, `scip-python`,
  `rust-analyzer` emit it).
- **Aider's repo-map trick — steal regardless.** tree-sitter def/ref tags → symbol
  reference graph → **PageRank** → **token-budgeted** ranked outline of the most central
  symbols, injected proactively at session start. Best "minimize what's passed" idea;
  antidote to the navigation paradox because the graph does the *ranking*.

---

## 6. Recommended path (fast-shipper optimized)

1. **Start with LSP-as-tools.** Directly answers the three target questions, always fresh,
   no indexing pipeline. Ship: `find_definition`, `find_references`, `get_type`,
   `get_members`, `get_outline(file)`, `get_source(location)` first.
2. **Add tree-sitter** for cheap outline/signature extraction and the PageRank repo-map
   (Aider-style, token-budgeted) injected at session start.
3. **Only add a persisted SQLite/Kùzu graph** when repo-wide queries get too slow for LSP
   (whole-repo impact analysis, big-monorepo caching). Not day one.

**Prioritize typed edges** `extends` / `implements` / `type-of` / `injects` and
**interface→consumer expansion**, not just the call graph. Traverse **bidirectionally**.

---

## 7. Tool interface (value is realized in return shape)

Rules:
- Return **signatures, not bodies**, by default.
- Every result carries a **stable symbol ID + `file:line` range**; drill in via `get_source(id)`.
- **One-line context** per reference, not the surrounding block.
- Bodies only on explicit request. Agent navigates by jumping; fetches source for the few
  it needs.

```
get_outline(file)            → symbol tree (map a file, ~0 body tokens)
find_definition(symbol)      → {id, file:line, signature}
find_references(symbol)      → [{file:line, 1-line context}]
get_type(expr|symbol)        → {type, definition_id}
get_members(type)            → [{name, kind, signature}]
get_callers(fn)/get_callees  → [ids]
who_imports(mod)/imports_of  → [modules]
impact(symbol)               → transitive referent ids (blast radius)
get_source(id|range)         → exact snippet, on demand
```

---

## 8. Risks & how to validate

- **Integration risk (yours to own):** the graph only helps if the *acting* agent queries
  it. Wire graph tools into the main loop, **not** behind a file-reading subagent.
- **Variance:** expect 10–90% token savings by repo/query, not a flat number. Small
  localized queries in small repos → grep already wins.
- **Don't lean on vector search for correctness** — weakest method, hallucinates on
  architectural questions. Use it only as the fuzzy discovery edge, then hand off to the
  graph.
- **Benchmark on your own workloads with correctness HELD OUT.** Every token-savings
  benchmark cited here failed to measure correctness. Run 4+ trials, hold repo/model
  constant, score answer accuracy — not just token count — or you optimize for cheap wrong
  answers.

---

## Sources

- grepai vs grep benchmark — https://yoanbernabeu.github.io/grepai/blog/benchmark-grepai-vs-grep-claude-code/
- grepai repo — https://github.com/yoanbernabeu/grepai
- CodeGraph review + per-repo numbers — https://andrew.ooo/posts/codegraph-review-pre-indexed-knowledge-graph-claude-code/
- CodeGraph overview — https://toknow.ai/posts/codegraph-knowledge-graph-ai-coding-agents-fewer-tokens/
- Reliable Graph-RAG: AST-derived vs LLM-extracted graphs — https://arxiv.org/html/2601.08773v1
- Code Graph Model, SWE-bench Lite — https://arxiv.org/pdf/2505.16901
- Navigation paradox (CodeCompass) — https://arxiv.org/pdf/2602.20048
- Agentic search stack replacing RAG in 2026 — https://buzzgrewal.medium.com/ai-agents-dont-need-vector-search-anymore-inside-the-agentic-search-stack-replacing-rag-in-2026-58efcabe4f6f
- Serena MCP (LSP-as-tools) — https://andrew.ooo/posts/serena-mcp-coding-agent-ide-review/
- Aider repo-map (tree-sitter + PageRank) — https://aider.chat/2023/10/22/repomap.html
- SCIP vs LSIF — https://sourcegraph.com/blog/announcing-scip
- Code intelligence tools comparison — https://rywalker.com/research/code-intelligence-tools
