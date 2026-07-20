# EVAL — How we measure whether the graph actually helps (budget-shaped)

**Date:** 2026-07-21
**Principle:** Retrieval correctness (did `find_references` return the right lines) is
**necessary but not sufficient**. The real question is whether the model **ships better
features, fixes, and refactors** — cheaper — *with* the graph than without. The eval rig
exists **from day one**; it is the only way to know if we help.

**Budget reality:** No budget for large benchmark sweeps. The real currency is **Claude Max
quota** — headless `claude -p` eval runs consume the same weekly limits as real work, so a
big suite would block the user's actual coding. The eval is therefore **quota-boxed**: the
free/cheap tiers carry the day-to-day signal, and the expensive model-in-the-loop tier is a
**tiny, curated, milestone-only** run. Default eval model = **Sonnet** (cheaper/faster);
**Opus** only for occasional final validation.

---

## Three tiers, cheapest-first

### Tier A — Retrieval correctness (no LLM)  · FREE · CI gate, every commit
- Gold `definition`/`references`/`type`/`members` locations on pinned TS repos.
- Ground truth bootstrapped from the language provider on a frozen commit, spot-checked,
  frozen.
- Metrics: precision / recall / exactness of returned locations.
- **Stale-correctness (critical):** scripted edit sequences assert correct **post-edit**
  locations — the regression gate for the staleness barrier.
- **Role:** deterministic, no model calls, runs on every commit. This carries the load.

### Tier B — Navigation efficiency (LLM, short episodes)  · CHEAP · per-milestone
The affordable thesis signal. Instead of solving whole GitHub issues, ask **fixed questions
with known-correct answers** and measure **cost-to-correct-answer**.
- Question set (~20–40): "where is X defined", "what implements interface Y", "list callers
  of Z", "what's the type of expr at file:line", "what breaks if I change W" — each with a
  pre-verified answer.
- Two arms (graph-on vs off), same model, on 2–3 mid-size TS repos.
- **Metrics:** correct? (binary) · **tokens-to-answer** · tool calls · turns.
- Short episodes = cheap → can run every milestone on Sonnet. This directly measures the
  token-reduction thesis without the cost of full task resolution.

### Tier C — Task capability (LLM, long episodes)  · EXPENSIVE · milestone-only, tiny
The gold standard: does the model resolve real production tasks better/cheaper?
- **Tiny curated set (~10–20 TS tasks)**, sourced from real merged PRs with test oracles,
  stratified by navigation spread (see below). Not hundreds.
- Sources: **Multi-SWE-bench / SWE-bench Multilingual (TS/JS subset)** for free oracles +
  a small hand-curated supplement of large-repo, multi-file tasks (to stress graph strength
  and control spread). *(Verify exact TS instance counts at Phase 2.)*
- Oracle: `FAIL_TO_PASS` + `PASS_TO_PASS` test execution.
- **N = 1–2 runs/task/arm** (accept noise; pairing reduces variance), paired by task.
- **Metrics:** resolved (pass@1) · tokens · tool calls · turns.
- Run **manually at major milestones**, Sonnet default, Opus spot-check on a handful.
  Quota budget: a few dozen episodes per milestone, scheduled when not doing real work.

---

## Two methodological commitments that keep it honest

1. **Within-task delta cancels confounders.** Both arms run the same harness on the same
   inputs, so contamination and scaffolding effects (which swing SWE-bench 10–20 pts alone)
   largely cancel. Absolute numbers are **not** leaderboard-comparable — only our **deltas**
   are, and the delta isolates the graph. This is also what lets small N stay meaningful.

2. **Stratify by "navigation spread."** Bin tasks/questions by how cross-file / multi-hop
   the answer is (files touched, distinct symbols, cross-file refs). Hypothesis (from
   `INITIAL_RESEARCH.md`): the graph's advantage **grows with spread** and is ~zero on
   single-file localized cases (the OkHttp-13% case). Report the delta **as a curve over
   spread**, not one aggregate — with a tiny N this is what turns noise into signal, and
   pre-commits us to an honest read.

**Pre-registered success bar (no goalpost-moving):** primary success = **no worse
resolution at materially fewer tokens/tool-calls, with the advantage concentrated on
high-spread tasks.** A null aggregate with a positive high-spread slope is a valid outcome.
So is a negative result — we ship the truth.

---

## The eval runs the real system
Tiers B and C execute the **full daemon + hooks + language provider + staleness barrier**,
freshly indexed at each base commit, edits live. Not a mocked graph → Tier C doubles as an
integration test for the barrier under realistic edit sequences.

## Statistics
Paired per-item deltas; bootstrap CIs where N allows; report **per spread-bin** and
aggregate. With small N, lean on Tier A (free, large) for confidence in the machinery and
Tier B (cheap, repeatable) for the efficiency trend; treat Tier C as directional.

## Open items (Phase 2)
- Spread-bin thresholds (compute from gold patches; calibrate on a pilot).
- Final Tier B question set + Tier C task list (TS sources vs curated).
- Per-milestone quota budget (episodes/week we can spend without blocking real work).
