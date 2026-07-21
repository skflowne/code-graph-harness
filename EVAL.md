# EVAL — How we measure whether the graph actually helps (budget-shaped)

**Date:** 2026-07-21
**Principle:** Retrieval correctness (did `find_references` return the right lines) is
**necessary but not sufficient**. The real question is whether the model **ships better
features, fixes, and refactors** — cheaper — *with* the graph than without. The eval rig
exists **from day one**; it is the only way to know if we help.

**Budget reality:** No budget for large benchmark sweeps. The real currency is **Claude Max
quota** — headless `claude -p` eval runs consume the same weekly limits as real work. So the
volume is carried by a **local model** (free — runs on our own compute), and Claude arms are
**quota-boxed** validation only.

---

## Model axis + the eval runner (how we get free volume)

Claude Code only runs Claude, so the science runs through a **model-agnostic eval runner we
own**: an MCP client with a **pluggable backend** — a local OpenAI-compatible endpoint
(Ollama / vLLM: Qwen3-Coder, GLM, Devstral, …) *or* the Claude API — same scaffolding, same
prompt, same hooks; only the **model** and the **graph condition** vary.

**Design = 2×2 (at least):** `{local, Claude} × {graph, no-graph}`.
- **Local arms carry the volume** (free, continuous, no rate limits) → the statistical power.
- **Claude arms are sparse, quota-boxed** validation.
- **Ecological-validity check:** occasionally run the `Claude + graph` arm in *real Claude
  Code* to confirm the neutral-runner result transfers to the shipping product.

**Hypotheses this tests:**
- **H1 (main effect):** graph improves each model (local+graph > local−graph; Claude+graph >
  Claude−graph).
- **H2 (interaction):** the graph helps the weaker/local model **more** (bigger delta) — our
  research predicts strong models grep-navigate well and need it less.
- **H3 (the value story):** does **local+graph approach Claude−graph**? If a free local model
  *with* the graph rivals ungraphed Claude, that reframes who the tool is for.

**Caveat:** a small local model that underuses the tools tells us about **discoverability**,
not the graph's ceiling. Log **per-model tool adoption** and read deltas in that light. Also
pick a local model with competent tool-calling; tool-use ability varies wildly by model.

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
- `{local, Claude} × {graph, no-graph}` on 2–3 mid-size TS repos.
- **Metrics:** correct? (binary) · **tokens-to-answer** · tool calls · turns · tool adoption.
- Short episodes + free local runs = cheap → run large-N on the local model every milestone,
  Claude arms sparsely. Directly measures the token-reduction thesis + the H2/H3 model
  interaction without full task-resolution cost.

### Tier C — Task capability (LLM, long episodes)  · EXPENSIVE · milestone-only, tiny
The gold standard: does the model resolve real production tasks better/cheaper?
- **Tiny curated set (~10–20 TS tasks)**, sourced from real merged PRs with test oracles,
  stratified by navigation spread (see below). Not hundreds.
- Sources: **Multi-SWE-bench / SWE-bench Multilingual (TS/JS subset)** for free oracles +
  a small hand-curated supplement of large-repo, multi-file tasks (to stress graph strength
  and control spread). *(Verify exact TS instance counts at Phase 2.)*
- Oracle: `FAIL_TO_PASS` + `PASS_TO_PASS` test execution.
- `{local, Claude} × {graph, no-graph}`, paired by task. **Local: N=3–4** (free); **Claude:
  N=1–2** (quota-boxed), plus a `Claude+graph` spot-check in real Claude Code.
- **Metrics:** resolved (pass@1) · tokens · tool calls · turns · tool adoption.
- Run **manually at major milestones**. Local arms carry volume; Claude arms scheduled when
  not doing real work. This is where H3 (local+graph vs Claude−graph) gets its strongest test.

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
Paired per-item deltas; bootstrap CIs where N allows; report **per spread-bin × model**.
Free local volume gives real power on H1/H2/H3; Claude arms are sparse — treat Claude-side
CIs as directional and lean on the ecological-validity spot-check. Tier A (free, large)
underwrites confidence in the machinery.

## Open items (Phase 2)
- **Local model choice** — a coding model with competent tool-calling that fits our hardware
  (Qwen3-Coder / GLM / Devstral / …); pin it for comparability.
- **Eval runner** — MCP client + pluggable backend (local OpenAI-compatible / Claude API),
  approximating Claude Code's loop closely enough for the spot-check to transfer.
- Spread-bin thresholds (compute from gold patches; calibrate on a pilot).
- Final Tier B question set + Tier C task list (TS sources vs curated).
- Per-milestone Claude quota budget (episodes/week without blocking real work).
