# Hotel description localization

Generates a short English/German/French marketing description per hotel from a
shared source-of-facts, and automatically scores every language for (1) factual
consistency against the source and against each other, and (2) how natively it
reads. See [EVALUATION.md](EVALUATION.md) for the metrics and how the score was
improved run over run.

## Requirements

- Go 1.22+
- An Anthropic API key (https://console.anthropic.com)

## Setup

```bash
git clone <this repo>
cd test-repo
go build ./...   # optional sanity check; `go run` below builds automatically
```

## Passing your API key

```bash
export ANTHROPIC_API_KEY=sk-ant-...
```

Optional overrides:

```bash
export ANTHROPIC_MODEL=claude-sonnet-4-5-20250929   # default
export ANTHROPIC_BASE_URL=https://api.anthropic.com # default
```

No other provider is wired up today. The generator and evaluator only depend on the
small `llm.Client` interface in `internal/llm/client.go`, so adding another provider
means implementing that one interface (see `internal/llm/anthropic.go` for the
reference implementation) — nothing in `generate`, `evaluate`, or `pipeline` is
Anthropic-specific.

## Run it

```bash
go run ./cmd/localize
```

This runs both sample hotels in `testdata/hotels.json`, prints per-attempt scores
and the final descriptions to stdout, and writes the full attempt history (every
draft, every check result) to `results/run-<timestamp>.json`.

Flags:

```bash
go run ./cmd/localize -hotels path/to/hotels.json   # different source data
go run ./cmd/localize -hotel-id hotel-002           # only run one hotel
go run ./cmd/localize -max-attempts 3               # override the retry cap (default 5)
go run ./cmd/localize -out somewhere/               # change the results directory
```

Source data must match the shape of `testdata/hotels.json`: `id`, `name`, `city`,
`country`, `setting`, `amenities[]`, `rooms[]`, `nearby[]`, `policies[]`,
`price_band`.

A run makes real API calls (generation + fact-extraction + nativeness judging, per
language, per attempt) and takes a few minutes for two hotels; expect it to cost a
small number of dollars in API usage depending on how many retries are needed.

## Run the tests

```bash
go test ./...
```

The test suite does **not** call any LLM API — `internal/llm/llmfake` is a scripted
fake client used to (a) unit-test the scoring math directly (dropped facts,
contradicted qualifiers, invented claims, extra facts — see
`internal/evaluate/consistency_test.go`), and (b) drive the full retry loop in
`internal/pipeline/pipeline_test.go` through a scripted "drops a fact, then fixes
it" / "reads like a translation, then improves" scenario and assert the loop
actually catches and recovers from both.

## How it works

1. `generate.EnglishBase` gives the model the hotel's full fact catalog (each fact
   has a stable ID) and asks it to pick 6–9 facts and write the English description,
   returning which fact IDs it used.
2. `generate.InLanguage` generates German and French **from that same fact ID
   list**, not by translating the English text — each language is told to write
   natively and to state exactly those facts, nothing more.
3. `evaluate.Extract` independently re-reads each description against the *full*
   fact catalog (not just the selected subset) and reports which facts are
   accurately stated, which are stated but with a dropped/altered qualifier
   ("contradicted"), and which claims aren't backed by any catalog fact at all.
4. `evaluate.Score` turns that into precision/recall/F1 and an `exact_match` flag
   against the intended fact set.
5. `evaluate.JudgeNativeness` independently rates 1–5 how natively each text reads.
6. `pipeline.RunHotel` retries a language (with targeted feedback: facts first, then
   phrasing, once facts are exact) until it passes `exact_match && nativeness >= 4`
   or the attempt cap is hit, recording every attempt.

## Project layout

```
cmd/localize/          CLI entrypoint
internal/model/        hotel + fact-catalog types, JSON loading
internal/llm/           provider-agnostic Client interface + Anthropic implementation
internal/llm/llmfake/  scripted fake client used by tests
internal/generate/     English base + per-language generation
internal/evaluate/     fact extraction/scoring + nativeness judging
internal/pipeline/     generate -> check -> retry orchestration
testdata/hotels.json   the source facts given in the brief
results/               JSON output of each run (attempt-by-attempt)
```
