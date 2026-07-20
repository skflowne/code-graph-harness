# EVAL — How we measure whether the graph actually helps

**Date:** 2026-07-21
**Principle:** Retrieval correctness (did `find_references` return the right lines) is
**necessary but not sufficient**. The real question is whether the model **ships better
features, fixes, and refactors** — cheaper — *with* the graph than without. Everything else
is a proxy. The eval rig exists **from day one**; it is the only way to know if we help.

---

## Two tiers

### Tier A — Retrieval correctness (component)  · CI gate, every commit
- **What:** gold `definition` / `references` / `type` / `members` locations on pinned repos.
- **Ground truth:** bootstrapped from the LSP on a frozen commit, spot-checked, then frozen.
- **Metrics:** precision / recall / exactness of returned locations.
- **Stale-correctness (critical):** scripted edit sequences assert the graph returns correct
  **post-edit** locations — this is the regression gate for the staleness barrier (§barrier).
- **Role:** guards the machinery. Fast, deterministic, blocks merges. Not the scoreboard.

### Tier B — Task capability (the thesis test)  · periodic (nightly / pre-release)
Does the model resolve real production tasks better/cheaper with the graph?

**Task sources — reuse OSS-harvested benchmarks with execution oracles (no hand-authoring):**

| Category | Source | Oracle | Notes |
|---|---|---|---|
| Bugs | **SWE-bench Verified** (500, Python) | `FAIL_TO_PASS` + `PASS_TO_PASS` tests | Aligns with pyright-first |
| Features | **FeatureBench** (ICLR 2026) | execution-based, test-driven | Beyond bug fixing |
| Refactors | **SmellBench** / refactor-labeled PRs | existing tests still pass (+structural) | Multi-file → graph should shine |
| Contamination check | **SWE-bench Live** (rolling, post-cutoff) | test execution | Confirms delta holds on fresh tasks |
| Multilingual (later) | **SWE-bench Multilingual / Multi-SWE-bench** | test execution | TS/Rust/Go as LSPs are added |

**Two-arm design — everything identical except the graph:**
- **Baseline:** vanilla Claude Code headless (`claude -p`, grep/read, `--strict-mcp-config`
  → no graph).
- **Treatment:** same + our MCP server + hooks + search-strategy CLAUDE.md.
- Identical: model, base commit, prompt, turn/token caps.
- **N ≥ 3–4 runs / task / arm** (nondeterminism), report median / rate, **paired by task**.

**Metrics per task:**
- *Capability (primary):* resolved — pass@1, pass@k.
- *Efficiency (primary):* total tokens, cost, tool calls, turns.
- *Diagnostic:* graph-tool adoption count, stale-rate encountered, wall-clock.

---

## Two methodological commitments that keep it honest

1. **Within-task delta cancels confounders.** Both arms run the same harness on the same
   inputs, so contamination and scaffolding effects (which swing SWE-bench 10–20 pts on
   their own) largely cancel. Our absolute numbers are **not** leaderboard-comparable — only
   our **deltas** are, and the delta is exactly what isolates the graph's contribution.

2. **Stratify by "navigation spread."** Bin tasks by how cross-file / multi-hop the gold
   solution is (files changed in gold patch, distinct symbols referenced, cross-file ref
   count). Hypothesis (from `INITIAL_RESEARCH.md`): the graph's advantage **grows with
   spread** and is ~zero on single-file localized tasks (the OkHttp-13% case). Report the
   delta **as a curve over spread**, not one aggregate — this turns a likely-null average
   into the real signal, and pre-commits us to accepting an honest result.

**Pre-registered success bar (no goalpost-moving):** primary success = **no worse
resolution at materially fewer tokens/tool-calls, with resolution improving on high-spread
tasks.** Higher overall resolution is a bonus, not the bar. A null aggregate with a positive
high-spread slope is a *valid, publishable* outcome. So is a negative result — we ship the
truth.

---

## The eval runs the real system

Tier B executes the **full daemon + hooks + LSP + staleness barrier**, freshly indexed at
each task's base commit, with edits live as the model works. Not a mocked graph. So Tier B
doubles as an **integration test for the staleness barrier under realistic edit sequences**.

---

## Statistics
- Paired per-task deltas; bootstrap CIs on resolution-rate delta and token delta.
- Report per-stratum (spread bins) **and** aggregate.
- Willing to report/accept null or negative results.

## Open items to define during Phase 2
- Exact spread-binning thresholds (compute from gold patches; calibrate on a pilot set).
- Task subset sizes per source (statistical power vs cost of N×2×tasks headless runs).
- How much curated large-repo / multi-file supplement (to stress graph strength) vs pure
  harvested sets.
