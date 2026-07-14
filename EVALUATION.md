# Evaluation

## Metrics, and why they're weighted the way they are

**Factual consistency (weight 0.7, and a hard gate).** Every source fact is given a
stable ID (`amenities.0`, `policies.1`, ...). The English generator must declare
exactly which fact IDs it used. German and French are then generated from that same
ID list — never by translating the English sentence — which removes "translation
drift" as a failure mode and leaves only "did the model state this fact catalog
accurately" as the thing to check. A **second, independent LLM call** (that never
sees the generation prompt) reads the fact catalog and the description and reports,
per fact: matched, *contradicted* (mentioned but with a dropped/altered qualifier —
e.g. "dogs allowed" when the source says "ground-floor rooms only"), or absent, plus
any claim in the text not covered by the catalog at all (`unsupported_claims`, i.e.
hallucination). From that I compute precision/recall/F1, but the pass bar is
**exact match**: zero missing, zero contradicted, zero extra, zero invented. F1 is
reported for ranking/trend purposes, but "close" is still a fail — the brief asks
for identical facts across languages, not mostly-identical ones.

**Nativeness (weight 0.3, soft bar of 4/5).** A separate LLM-as-judge call, blind to
the source facts and to the other languages, rates 1–5 whether the text reads as
native copy vs. a translation, and lists specific translated-sounding phrases. This
is deliberately the smaller weight: a stiff-but-accurate description is a much
smaller problem for a booking platform than a fluent-but-wrong one, which is exactly
the ordering the brief asks for ("the star, weighted heaviest" vs. "the softer
check").

**Overall score** = `0.7 * consistency_F1 + 0.3 * (nativeness/5)`, reported for
trend-watching; the actual pass/fail gate is `exact_match && nativeness >= 4`.

## What was wrong at first, what changed, what it did to the scores

All of the below came from real runs against Claude (`claude-sonnet-4-5`), not
hypothetical failure modes — see `results/run-20260714T062457Z.json` for the final
run this describes.

1. **Fact selection ignored its own instruction.** "Choose 4–7 additional facts"
   produced a 13-fact English base (out of ~18 available) — too many for a "short"
   description and not the kind of curation the brief asks for. Fixed by stating a
   single total-count range ("6–9 facts total, including mandatory ones") instead of
   an "additional" delta. Selections dropped to 10–13 and stayed within a
   defensible range.
2. **Judge output shape crashed the parser.** The nativeness judge occasionally
   returned `"issues": ""` instead of `[]` for the empty case, despite the declared
   JSON schema, and the strict `[]string` unmarshal crashed the whole run. Added a
   tolerant `stringList` JSON type (in `internal/evaluate/jsonutil.go`) that accepts
   either shape. This is the kind of bug you only find by actually running against a
   real model rather than trusting the schema.
3. **Fixing both dimensions in one rewrite made them fight each other.** The first
   real run showed `hotel-002` German oscillating for 4 attempts:
   `F1 0.90 → 1.00 → 0.86 → 1.00`, nativeness stuck at `3/5` throughout — every time
   the retry prompt tried to fix translated-sounding phrasing *and* re-state a
   missing fact at once, the rewrite fixed one and broke the other. Changed the
   retry loop (`internal/pipeline/pipeline.go`) to fix facts first: nativeness
   feedback is only sent once `exact_match` is already true, and it explicitly says
   "do not add, remove, or alter any fact." That alone took `hotel-002` from 2/3
   language passes to 3/3, with German converging on attempt 4 instead of never.
4. **Full-rewrite fixes for phrasing kept introducing new nits.** Even after (3),
   phrasing-only retries would sometimes fix the flagged phrase but reword an
   untouched sentence into something the judge flagged instead — a whack-a-mole
   pattern that didn't reliably converge within the attempt budget. Changed the
   phrasing-only feedback to ask for the smallest possible edit ("change only the
   flagged phrases, keep the rest of the sentence structure") rather than a full
   rewrite, and raised `MaxAttempts` from 4 to 5. Isolated reruns of `hotel-002`
   went from 2/3 → 3/3 language passes with this change.

Net effect across those changes, on the two sample hotels: the very first working
end-to-end run passed 4/6 language outputs; the final configuration passed 5/6 on a
fresh run, with the sixth (`hotel-001` German) failing purely on nativeness
(stuck at 3/5 after 5 attempts) while staying factually exact throughout — i.e. the
consistency gate never let a factually-wrong description through, and the one
persistent failure mode left is a nativeness-judge plateau, not silent drift.

## What I'd do with more time

- **De-noise the nativeness judge.** A single temperature-0 judge call still finds a
  *different* nitpick each time the text changes slightly, so a text can plateau at
  3/5 indefinitely even as it genuinely improves. I'd sample the judge 3x and take
  the median, and/or have it compare against the previous attempt directly ("is this
  strictly more native than the last draft, yes/no") instead of an absolute score.
- **Calibrate the nativeness bar with real native speakers** on a small sample
  instead of trusting the judge's own 1–5 rubric at face value — an LLM judge's "4"
  and a Sylt local's "4" aren't guaranteed to agree.
- **Batch/parallelize** the per-language attempt loop (currently sequential per
  hotel to keep logs and retries easy to reason about) to cut run time.
- **Track cost/latency per pass**, not just quality, since the retry loop trades
  API calls for reliability and a real deployment would want that budgeted.
