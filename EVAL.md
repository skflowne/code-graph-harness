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

## Model × harness axes (how we get free volume + generalization)

We run **two harnesses**, and the **local model is the common thread run in both** — it's
free, carries the volume, and isolates the harness effect (same model, vary harness × graph).
The frontier arm is **harness-bound** (Pi can't run Claude):

| Harness | Scaffolding | Frontier arm | Local arm (common) |
|---|---|---|---|
| **Claude Code** | rich (product context) | Claude (Max-covered) | Qwen3-Coder-30B-A3B (free) |
| **Pi** | minimal (clean-room control) | OpenAI (pay-$, sparse) | Qwen3-Coder-30B-A3B (free) |

**How each is driven:**
- **Claude Code:** backend swapped via `ANTHROPIC_BASE_URL` — Claude natively, or the local
  model (Ollama v0.14+ speaks the Anthropic API natively). Local arms hit localhost → **no Max
  quota**, full product-harness fidelity. No separate runner needed.
- **Pi:** minimal harness; frontier = OpenAI (cheap tier / OpenRouter to bound $), plus the
  local model. Our tools reach Pi via an MCP-adapter package or a native Pi TS extension over
  the daemon; see `PLAN.md` for the barrier caveat.

**Why two harnesses (Pi's payoff):** Pi adds a second frontier *family* (OpenAI) **and** a
minimal *harness*, so we can claim the graph **generalizes** — not "helps Claude in Claude
Code," but "helps frontier + local models across rich and bare-bones harnesses." Pi is the
low-scaffolding control (recall harness scaffolding swings results 10–20 pts).

**Hypotheses:**
- **H1 (main effect):** graph improves each (harness, model) cell.
- **H2 (interaction):** graph helps the weaker/local model **more** — strong models
  grep-navigate well and need it less.
- **H3 (value story):** does **local+graph approach frontier−graph** (Claude in CC, OpenAI in
  Pi)? A free local model + graph rivaling an ungraphed frontier model reframes who this is for.
- **H4 (generalization):** does the graph delta survive the minimal harness (Pi) and a
  non-Claude frontier family (OpenAI)?

**Clean vs confounded:** the graph **delta within each (harness, model) cell is always clean**
— that is the primary result. Cross-cell frontier comparison (Claude-in-CC vs OpenAI-in-Pi)
mixes harness *and* family → report descriptively, never attribute to harness alone. The
local-model-in-both runs isolate harness.

**Caveat:** a model that underuses the tools measures **discoverability**, not the graph's
ceiling. Log **per-(harness,model) tool adoption** and read deltas in that light.

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
Paired per-item deltas; bootstrap CIs where N allows; report **per spread-bin × (harness,
model) cell**. Free local volume (in both harnesses) gives real power on H1/H2/H4; frontier
arms (Claude/OpenAI) are sparse → treat their CIs as directional. Tier A (free, large)
underwrites confidence in the machinery.

## Decisions locked
- **Local model: Qwen3-Coder-30B-A3B (NVFP4)**, CPU expert-offload (`--n-cpu-moe`) to fit a
  16 GB RTX 5080 laptop (MoE, 3 B active → stays fast). Pinned for comparability.
- **Harnesses: Claude Code (rich) + Pi (minimal control).** No separate model-agnostic runner
  — Claude Code swaps backend via `ANTHROPIC_BASE_URL`; Pi runs its own providers.

## Open items (Phase 2)
- **Pi adapter** — tool exposure (MCP-adapter package vs native TS extension) and whether a Pi
  extension can wrap Edit/Write to fire the staleness barrier (else Pi = freshness-metadata +
  model-instruction only). See `PLAN.md`.
- **OpenAI arm** — pick a cheap tier / OpenRouter to bound out-of-pocket $; keep sparse.
- Spread-bin thresholds (compute from gold patches; calibrate on a pilot).
- Final Tier B question set + Tier C task list (TS sources vs curated).
- Per-milestone frontier budget (Claude Max episodes + OpenAI $) without blocking real work.
