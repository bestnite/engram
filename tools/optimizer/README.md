# FSRS optimiser adapter

A small Rust binary that turns a standard review log into a 21-element FSRS v6
weight array as JSON. It wraps the optimiser of the upstream `fsrs` crate; no
FSRS algorithm is reimplemented here (`AGENTS.md` ).

The web-triggered optimisation job (`internal/jobs`) runs this binary as a
subprocess. The algorithm never enters the Go code.

## Upstream dependency (verified 2026-10-02)

| Item | Value |
|---|---|
| crates.io name | `fsrs` |
| Pinned version | `=6.6.2` (exact pin in `Cargo.toml`) |
| Repository | `open-spaced-repetition/fsrs-rs` (the repo is named `fsrs-rs`; the published crate is `fsrs`) |
| Licence | BSD-3-Clause |
| Optimiser entry point | `fsrs::compute_parameters(fsrs::ComputeParametersInput) -> fsrs::Result<Vec<f32>>` |
| Training seed | `fsrs::TrainingConfig::default().seed == 2023` |
| Weight count | `fsrs::DEFAULT_PARAMETERS: [f32; 21]` |

Sources:

- crate page: <https://crates.io/crates/fsrs>
- API docs: <https://docs.rs/fsrs/6.6.2>
- repository: <https://github.com/open-spaced-repetition/fsrs-rs>
- review-log schema: <https://github.com/open-spaced-repetition/fsrs-optimizer>

The upstream repository ships no optimiser CLI — only `examples/optimize.rs`.
This adapter is that missing binary; it follows the example's conversion
(`FSRSItem` accumulation plus the `long_term_review_cnt() > 0` filter) and the
published review-log schema.

## Build

```sh
cd tools/optimizer
cargo build --release
# produces ./target/release/optimizer
```

Build output (`target/`) is gitignored; only sources, `Cargo.lock` and the
fixture are committed.

## CLI contract

```
optimizer [OPTIONS] [INPUT]
```

| Argument / option | Meaning |
|---|---|
| `INPUT` | Path to a review-log file (JSON Lines). Omit or use `-` to read stdin. |
| `--out <PATH>` | Write the weights JSON to `PATH`. `-` or omitted writes to stdout. |
| `--threads <N>` | Worker threads for training. Default `1`. See *Determinism*. |
| `--seed <N>` | Training seed. Default `2023` (the upstream default). |
| `-h`, `--help` | Print usage. |
| `-V`, `--version` | Print the adapter version. |

Standard output carries **only** the weights JSON (plus a trailing newline).
All diagnostics go to stderr and are written in English (`AGENTS.md` §2.1).

### Input format

One JSON object per line. Blank lines are skipped; unknown fields are ignored.

| Field | Type | Required | Notes |
|---|---|---|---|
| `card_id` | integer or string | yes | Groups reviews; never emitted in the output. |
| `review_time` | integer | yes | Milliseconds since the Unix epoch, **UTC**. |
| `review_rating` | integer | yes | `1..=4` (Again/Hard/Good/Easy). Rejected otherwise. |
| `review_state` | integer | no | `0..=3` (New/Learning/Review/Relearning); validated but not used for training. |
| `review_duration` | integer | no | Milliseconds, non-negative; validated but not used for training. |
| `timezone` | string | no | IANA time zone (for example `UTC`); default `UTC`. |
| `day_start` | integer | no | Hour `0..=23` that starts a new day; default `0`. |

Conversion to training items:

1. Reviews are grouped by `card_id` and sorted by `review_time` (ties broken by
   `review_rating`, so the result does not depend on the input line order).
2. Each review is assigned a *review day*: the local calendar date of
   `review_time` in the record's `timezone`, shifted back by `day_start` hours
   (the same rule the scheduler uses for review days). `delta_t` between consecutive reviews of
   a card is the difference in review-day numbers; the first review of a card
   uses `delta_t = 0` and same-day reviews get `delta_t = 0`.
3. For a card with reviews `r0..rn`, the items are the cumulative prefixes
   `[r0]`, `[r0, r1]`, ..., and items with no long-term review
   (`long_term_review_cnt() == 0`) are dropped — identical to
   `examples/optimize.rs`.
4. The surviving items are passed to `compute_parameters` together with their
   per-card ids.

### Output format

A JSON array of exactly 21 numbers, the FSRS v6 weights, for example:

```json
[0.0066308687,0.04044517,...,0.1]
```

This matches the upstream return type: `compute_parameters` returns a
`Vec<f32>` of 21 values (`DEFAULT_PARAMETERS` is declared as `[f32; 21]`). The
adapter fails instead of emitting anything if the optimiser returns a different
count or a non-finite value.

### Exit codes

| Code | Meaning |
|---|---|
| `0` | Success. |
| `2` | Usage error (unknown option, missing option value). |
| `3` | Input/output error: cannot read, parse or validate the review log, or cannot write the output. Includes "fewer than 8 usable training items". |
| `4` | Training error: the optimiser returned an error, a wrong weight count, or a non-finite weight. |

## Determinism

For a fixed input the output is byte-for-byte reproducible:

- The binary sets `RAYON_NUM_THREADS` to `--threads` (default `1`) before
  training starts. A single worker removes any dependence on thread scheduling
  in the parallel reductions of the training backend.
- The training seed is fixed (`--seed`, default `2023`, the upstream default).
- Neither the input nor the output is timestamped.

Reproducing the acceptance evidence (run from `tools/optimizer`):

```sh
./target/release/optimizer testdata/review-log.jsonl --out /tmp/a.json
./target/release/optimizer testdata/review-log.jsonl --out /tmp/b.json
cmp /tmp/a.json /tmp/b.json        # no output, exit 0 => byte-identical
```

The fixture at `testdata/review-log.jsonl` is 363 records over 45 cards
(mixed integer and string `card_id`s, `Asia/Shanghai` / `day_start = 4`, and
explicit day-boundary cases). Reordering its lines yields the same weights,
because items are ordered by card key and per-card review time rather than by
input order.

## Current limits (not yet verified)

- Correctness on real review data is **not** verified; only the fixture is.
- The Go side (`internal/jobs`) does not call the adapter at runtime yet; the
  command injection point is exercised by a Go integration test that skips when
  the binary is absent.
- The exit-4 path (optimiser error) is documented but has no dedicated fixture.
