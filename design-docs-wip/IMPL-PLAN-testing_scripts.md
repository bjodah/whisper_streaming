# Implementation Plan: Bash-Based Streaming Benchmark Scripts

## Goal

Create a small, reproducible set of Bash scripts that lets a developer evaluate the streaming behavior of `whisper-proxy` without needing a microphone or speakers.

The scripts should:

- replay known audio through the TCP interface (simulating what e.g. the emacs-client/strisper.el use via `arecord | nc`).
- generate longer and more challenging benchmark sessions by concatenating short WAV clips (e.g. `find tests/ -name '*.wav'`)
- capture proxy output in a stable machine-readable form
- measure latency, stability, and transcript quality
- optionally use timing references when available to produce quantitative timing scores

## Why This Is Needed

The current repository already contains:

- a TCP middleware that accepts raw `S16_LE`, 16 kHz, mono PCM
- an Emacs client in `emacs-client/strisper.el` that consumes timestamped text lines
- a `tests/` folder with LibriSpeech test-clean clips, reference transcripts, and coarse timing annotations

That is enough to build a useful offline evaluation harness. The missing piece is a repeatable replay and scoring workflow.

## Relevant Constraints

- The benchmark must work without audio hardware.
- The benchmark must exercise the real TCP protocol, not only the upstream transcription API.
- The benchmark must be able to run against local development builds.
- The benchmark should use existing bundled data in `tests/test-data/LibriSpeech`.
- The benchmark should be easy to extend with new corpora later.
- The benchmark should not assume perfect word-level timing reference data is already present.

## Existing Protocol Shape

The middleware emits lines of the form:

```text
<start_ms> <end_ms> <text>
```

That format matches the consumer logic in `emacs-client/strisper.el`, which parses newline-delimited timestamped text.

The benchmark scripts should preserve this contract and treat each line as a stable event for capture and scoring.

## Proposed Script Set

### 1. `scripts/benchmark/run-proxy.sh`

Starts the proxy locally with a known configuration and writes logs to a benchmark output directory.

Responsibilities:

- build or reuse the proxy binary
- set `OPENAI_BASE_URL` and `OPENAI_API_KEY`
- select deterministic runtime flags
- capture stdout/stderr into a run directory

Notes:

- This script should support both direct local execution and containerized execution.
- It should default to development-friendly settings but allow overrides via environment variables.

### 2. `scripts/benchmark/concat-session.sh`

Builds longer benchmark sessions by concatenating several short WAV files into one continuous WAV file.

Responsibilities:

- choose a clip sequence from the bundled LibriSpeech inputs
- concatenate raw audio in the correct format
- produce a session manifest that records clip boundaries
- merge reference text into a single session transcript
- optionally merge per-clip timing annotations into session-relative timing data

Implementation guidance:

- Use `ffmpeg` or `sox` for concatenation, depending on what is available.
- Normalize everything to 16 kHz, mono, 16-bit PCM WAV.
- Preserve exact clip durations and cumulative offsets in the manifest.

Why concatenation matters:

- the current `tests/test-data/LibriSpeech` files are short and individually too easy
- stitching multiple clips together creates a realistic streaming workload
- a longer session stresses buffer trimming, overlap handling, prompt reuse, and end-of-stream flushing

### 3. `scripts/benchmark/replay-stream.sh`

Replays a WAV session over TCP at real-time pace, emulating `arecord | nc`.

Responsibilities:

- read the benchmark WAV file
- convert it to raw PCM if needed
- send audio in fixed-size frames, e.g. 20 ms or 40 ms chunks
- sleep between writes so the stream arrives in real time
- optionally support jitter, burst mode, and accelerated mode
- connect to the proxy TCP port directly

Required behavior:

- default transport should be raw PCM over TCP
- frame pacing must be configurable
- the script should write exactly the same byte format the real client sends

Optional behavior:

- jitter injection to simulate unstable capture
- silence padding between concatenated utterances
- network delay simulation when benchmarking robustness

### 4. `scripts/benchmark/capture-output.sh`

Captures proxy output from stdout or from a TCP client and writes structured artifacts for scoring.

Responsibilities:

- save raw transcript lines
- save timestamps for when each line arrived
- save the proxy log
- optionally save the replay sender log

The goal is to make every run replayable and inspectable.

### 5. `scripts/benchmark/score-run.sh`

Computes benchmark metrics for one run.

Responsibilities:

- compare final committed transcript against the reference transcript
- compute text quality metrics such as WER or a simpler token mismatch score
- compute latency metrics such as time to first output and time to stable commit
- compute timing error when reference timings exist
- emit a summary report in text and JSON form

Scoring approach:

- start with transcript-level scoring if only text references are present
- use coarse segment timing when only phrase timing files exist
- upgrade to word-level timing scoring once word alignment is generated

### 6. `scripts/benchmark/run-all.sh`

Top-level orchestration script for local development.

Responsibilities:

- start the proxy
- build one or more benchmark sessions
- replay each session
- capture output
- score results
- print a summary table

This is the script most developers should run first.

## Comparing with the previous python prototype

We are working on a branch `golang-rewrite` in the /work folder, to guard against regressions when comparing against
the old python implementation (available in `/work-old-python-impl`), you may instead of launching the `whisper-proxy` go binary instead launch:
```console
(cpython-v3.13-apt-deb) 13:35 root@f8682bbbdabe:/work-old-python-impl# env OPENAI_BASE_URL=http://localhost:8007/v1 OPENAI_API_KEY=foobar python whisper_online_server.py --backend openai-api
DEBUG	Using OpenAI API.
WARNING	Whisper is not warmed up. The first chunk processing may take longer.
INFO	Listening on('localhost', 43007)
```

This can also be useful if we find that our benchmark scripts are underperforming: they might actually be uncovering a regression compared to the older python implementation.


## Data Layout

Use a dedicated benchmark workspace under `tests/` or `benchmarks/`, for example:

```text
tests/
  test-data/
    LibriSpeech/
    LibriTTS/
  benchmark/
    sessions/
    runs/
    reports/
    manifests/
```

Suggested artifact types:

- `*.wav` for concatenated sessions
- `*.jsonl` for session manifests and emitted transcript events
- `*.txt` for human-readable summaries
- `*.log` for proxy and replay logs

## Benchmark Session Format

Each session should include:

- an input WAV file
- a manifest with the source clips and offsets
- a reference transcript
- optional reference timing data
- metadata such as sample rate, frame size, and replay speed

Example manifest fields:

- `session_id`
- `source_clips`
- `start_offset_sec`
- `end_offset_sec`
- `reference_text`
- `reference_timings`
- `sample_rate`
- `frame_size_bytes`

## Timing Reference Strategy

The bundled `*.timings.txt` files are useful but not precise enough for all purposes.

Use a staged approach:

1. Coarse evaluation:
   - measure whether the emitted transcript appears too early, too late, or out of order
   - use the existing phrase timing data as a sanity check

2. Better timing evaluation:
   - generate word-level timestamps with a forced aligner
   - use those as the ground truth for quantitative word-delay analysis

3. Final evaluation:
   - compare stable output timestamps against aligned reference word times
   - report absolute error and percentile statistics

## Metrics To Report

The scripts should report at least:

- total session duration
- number of emitted lines
- time to first transcript line
- time to first committed word
- final transcript text
- WER or token error rate
- duplicate word count
- monotonicity violations in timestamps
- mean and p95 word timing error when references exist

Optional but useful:

- number of retractions or revisions before commit
- upstream request latency per chunk
- buffer trimming frequency
- average chunk size sent upstream

## How This Supports `emacs-client/`

The Emacs client expects timestamped text lines and inserts them into a buffer as they arrive.

That means the benchmark suite can evaluate two things at once:

- the middleware protocol behavior
- the consumer experience of incremental, line-oriented transcript updates

This is useful because a system can have good final WER but poor streaming usability if it emits unstable or overly delayed partials.

## Minimum Viable Implementation

If the team wants the smallest useful first version, implement these three scripts first:

1. `concat-session.sh`
2. `replay-stream.sh`
3. `score-run.sh`

That gives you:

- offline benchmark generation
- real TCP streaming replay
- automated evaluation output

## Suggested Dependencies

Prefer standard Unix tools plus a small number of well-known utilities:

- `bash`
- `ffmpeg` or `sox`
- `nc`
- `awk`, `sed`, `cut`, `grep`
- `python3` only if needed for scoring or manifest parsing

Avoid making the benchmark require a large Python environment unless a timing aligner is added later.

## Milestones

### Milestone 1: Replayable Baseline

- concatenate 3 to 10 LibriSpeech clips into one session
- replay them into the proxy at real-time pace
- capture the output lines
- verify the transcript is sane

### Milestone 2: Basic Scoring

- compare final transcript against reference text
- report elapsed time and request counts
- record proxy logs per run

### Milestone 3: Streaming Quality Metrics

- compute first-output latency
- compute stability or revision counts
- report timestamp monotonicity

### Milestone 4: Timing Accuracy

- generate better word-level references
- compute word delay error
- add percentile summaries

## Acceptance Criteria

The work is complete when a developer can:

- run one Bash command to create a benchmark session
- run one Bash command to replay that session through a local proxy
- run one Bash command to score the resulting transcript stream
- reproduce the benchmark on a machine with no microphone or speakers
- extend the benchmark by dropping new WAV files and transcripts into the manifest

## Open Questions

- Should the benchmark rely on `ffmpeg` or `sox` as the primary concatenation tool?
- Do we want a purely Bash implementation for scoring, or is a small `python3` helper acceptable?
- Should word-level timing generation be part of this repository or an offline preprocessing step?
- Do we want benchmark runs to drive the proxy directly over TCP, or also include a mode that replays through the Emacs client for UX validation?
