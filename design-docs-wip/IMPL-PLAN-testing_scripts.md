# Implementation Plan: Bash-Orchestrated Streaming Benchmark Harness

## Goal

Create a small, reproducible benchmark harness that lets a developer evaluate the streaming behavior of `whisper-proxy` without needing a microphone or speakers.

The harness should be Bash-first for orchestration, while allowing a small `python3` helper where Bash is a poor fit for full-duplex TCP replay/capture and scoring utilities.

The harness should:

- replay known audio through the TCP interface (simulating what e.g. the emacs-client/strisper.el use via `arecord | nc`).
- generate longer and more challenging benchmark sessions by concatenating short WAV clips (e.g. `find tests/ -name '*.wav'`)
- capture proxy output in a stable machine-readable form
- measure latency, output behavior, and transcript quality
- optionally use timing references when available to produce quantitative timing scores

## Why This Is Needed

The current repository already contains:

- a TCP middleware that accepts raw `S16_LE`, 16 kHz, mono PCM
- an Emacs client in `emacs-client/strisper.el` that consumes timestamped text lines
- a `tests/` folder with LibriSpeech test-clean clips, reference transcripts, and coarse timing annotations

That is enough to build a useful offline evaluation harness. The missing piece is a repeatable replay, capture, and scoring workflow.

## Relevant Constraints

- The benchmark must work without audio hardware.
- The benchmark must exercise the real TCP protocol, not only the upstream transcription API.
- The benchmark must be able to run against local development builds.
- The benchmark should use existing bundled data in `tests/test-data/LibriSpeech` first, and be able to add `tests/test-data/LibriTTS` later.
- The benchmark should be easy to extend with new corpora later.
- The benchmark should not assume perfect word-level timing reference data is already present.
- The benchmark must reflect the protocol that exists today: emitted transcript lines are committed word events, not unstable partial hypotheses.

## Existing Protocol Shape

The middleware emits lines of the form:

```text
<start_ms> <end_ms> <text>
```

That format matches the consumer logic in `emacs-client/strisper.el`, which parses newline-delimited timestamped text.

Important protocol notes:

- The proxy returns transcript lines on the same TCP connection that receives audio.
- Each emitted line is a committed word event.
- The current wire protocol does not expose unstable partials, retractions, or hypothesis revisions.

The benchmark harness should preserve this contract and treat each line as a stable event for capture and scoring.

## Proposed Script Set

### 1. `scripts/benchmark/run-proxy.sh`

Starts the proxy locally with a known configuration and writes logs to a benchmark output directory.

Responsibilities:

- build or reuse the proxy binary
- set `OPENAI_BASE_URL` and `OPENAI_API_KEY`
- select deterministic runtime flags
- capture stdout/stderr into a run directory
- write run metadata such as git SHA, command line, and environment overrides

Notes:

- This script should support both direct local execution and containerized execution.
- It should default to development-friendly settings but allow overrides via environment variables.
- It should allow the launched command to be swapped so the same harness can target either the Go proxy or the older Python implementation.

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
- Store sample-accurate offsets in the manifest; seconds can be derived for readability.

Why concatenation matters:

- the current `tests/test-data/LibriSpeech` files are short and individually too easy
- stitching multiple clips together creates a realistic streaming workload
- a longer session stresses buffer trimming, overlap handling, prompt reuse, and end-of-stream flushing

### 3. `scripts/benchmark/replay-stream.sh`

Legacy split, only if the team strongly prefers separate transport pieces.

This script would replay a WAV session over TCP at real-time pace, emulating `arecord | nc`.

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

### 4. `scripts/benchmark/run-session.sh`

Runs one benchmark session end to end.

This is the main transport primitive and should replace a separate `capture-output.sh` in the first implementation.

Responsibilities:

- open one full-duplex TCP connection to the proxy
- stream raw PCM frames at the configured pace
- concurrently read transcript lines returned on that same connection
- timestamp transcript arrival times locally
- write machine-readable transcript events for scoring
- optionally write a sender-side transport log

Implementation guidance:

- Implement the transport helper in `python3`.
- Keep the command-line interface simple so Bash can orchestrate it.
- Support half-close if available so the client can signal end-of-audio while still reading remaining transcript events.
- If half-close behavior is unreliable, document the fallback behavior explicitly and capture that limitation in the report.

Suggested event fields:

- `event_index`
- `arrival_monotonic_ms`
- `arrival_wall_time`
- `start_ms`
- `end_ms`
- `text`
- `raw_line`

Suggested outputs:

- `events.jsonl`
- `events.txt`
- `sender.log`
- `session-meta.json`

### 5. `scripts/benchmark/score-run.sh`

Computes benchmark metrics for one run.

Responsibilities:

- compare final committed transcript against the reference transcript
- compute text quality metrics such as WER or a simpler token mismatch score
- compute latency metrics such as time to first output and per-word output delay
- compute timing error when reference timings exist
- emit a summary report in text and JSON form

Scoring approach:

- start with transcript-level scoring if only text references are present
- use coarse segment timing when only phrase timing files exist
- upgrade to word-level timing scoring once word alignment is generated

Scoring rules must be explicit:

- normalize hypothesis and reference text before transcript scoring
- document case folding, whitespace collapsing, and punctuation handling
- prefer a single normalization path across corpora
- if scoring against LibriTTS, prefer `*.normalized.txt` over `*.original.txt`

### 6. `scripts/benchmark/run-all.sh`

Top-level orchestration script for local development.

Responsibilities:

- start the proxy
- build one or more benchmark sessions
- run each session through the transport helper
- score results
- print a summary table

This is the script most developers should run first.

### Optional: `scripts/benchmark/capture-output.sh`

Do not implement this as a first-class script in the MVP.

If a separate capture utility is later desired, it should be a thin formatter over `events.jsonl`, not a second TCP client. The proxy does not publish transcript events on stdout; stdout is for logs.

## Comparing with the previous python prototype

We are working on a branch `golang-rewrite` in the /work folder, to guard against regressions when comparing against
the old python implementation (available in `/work-old-python-impl`), you may instead of launching the `whisper-proxy` go binary instead launch:
```console
(cpython-v3.13-apt-deb) 13:35 root@f8682bbbdabe:/work-old-python-impl# env OPENAI_BASE_URL=http://localhost:8007/v1 OPENAI_API_KEY=foobar python whisper_online_server.py --backend openai-api
DEBUG	Using OpenAI API.
WARNING	Whisper is not warmed up. The first chunk processing may take longer.
INFO	Listening on('localhost', 43007)
```

This can also be useful if the benchmark uncovers a regression: the harness should be able to run the same session and scoring pipeline against both implementations.


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
- `*.json` for summaries and machine-readable run metadata
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
- `sample_rate`
- `channels`
- `bits_per_sample`
- `frame_size_bytes`
- `frame_duration_ms`
- `source_clips`
- `source_path`
- `clip_index`
- `start_sample`
- `end_sample`
- `start_offset_sec`
- `end_offset_sec`
- `reference_text`
- `reference_text_normalized`
- `reference_timings`
- `timing_granularity`
- `timing_source`
- `notes`

Recommended structure for each `source_clips` entry:

- clip id
- WAV path
- transcript source path
- timing source path, if any
- start sample within session
- end sample within session
- start offset seconds
- end offset seconds

## Timing Reference Strategy

The bundled `*.timings.txt` files are useful but coarse.

Use a staged approach:

1. Coarse evaluation:
   - measure whether the emitted transcript appears too early, too late, or out of order
   - use the existing phrase timing data as a sanity check
   - do not present these numbers as precise word-level latency

2. Better timing evaluation:
   - generate word-level timestamps with a forced aligner
   - use those as the ground truth for quantitative word-delay analysis

3. Final evaluation:
   - compare stable output timestamps against aligned reference word times
   - report absolute error and percentile statistics

The plan should treat forced alignment as optional follow-on work, not a prerequisite for the first useful benchmark.

## Metrics To Report

The harness should report at least:

- total session duration
- number of emitted lines
- time to first transcript line
- time to first emitted word
- final transcript text
- WER or token error rate
- duplicate word count
- monotonicity violations in timestamps
- coarse timing error summary when references exist

Optional but useful:

- upstream request latency per chunk
- buffer trimming frequency
- average chunk size sent upstream
- total request count

Do not claim the following metrics unless the implementation is extended to expose the needed data:

- retractions or revisions before commit
- unstable partial count
- time to stable commit distinct from emitted-word timing

## How This Supports `emacs-client/`

The Emacs client expects timestamped text lines and inserts them into a buffer as they arrive.

That means the benchmark suite can evaluate two things at once:

- the middleware protocol behavior
- the consumer experience of incremental, line-oriented transcript updates

This is useful because a system can have good final WER but poor streaming usability if it emits delayed committed words.

## Minimum Viable Implementation

If the team wants the smallest useful first version, implement these four pieces first:

1. `concat-session.sh`
2. `run-session.sh`
3. `score-run.sh`
4. `run-all.sh`

That gives you:

- offline benchmark generation
- real TCP streaming replay plus transcript capture
- automated evaluation output
- one command developers can run without knowing the internal pieces

## Suggested Dependencies

Prefer standard Unix tools plus a small number of well-known utilities:

- `bash`
- `ffmpeg` or `sox`
- `nc`
- `awk`, `sed`, `cut`, `grep`
- `python3` for the transport helper and optionally for scoring or manifest parsing

Avoid making the benchmark require a large Python environment unless a timing aligner is added later.

## Milestones

### Milestone 1: Replayable Baseline

- concatenate 3 to 10 LibriSpeech clips into one session
- run them through one full-duplex session client at real-time pace
- capture the output lines with arrival timestamps
- verify the transcript is sane

### Milestone 2: Basic Scoring

- compare final transcript against reference text
- report elapsed time and request counts
- record proxy logs per run
- record run metadata sufficient to reproduce the run

### Milestone 3: Streaming Quality Metrics

- compute first-output latency
- report timestamp monotonicity
- report coarse per-word delay against available timing references

### Milestone 4: Timing Accuracy

- generate better word-level references
- compute word delay error
- add percentile summaries
- optionally extend the proxy with machine-readable request metrics if log parsing becomes too fragile

## Acceptance Criteria

The work is complete when a developer can:

- run one Bash command to create a benchmark session
- run one Bash command to replay that session through a local proxy and capture transcript events
- run one Bash command to score the resulting transcript stream
- reproduce the benchmark on a machine with no microphone or speakers
- extend the benchmark by dropping new WAV files and transcripts into the manifest
- rerun the same session against either the Go proxy or the Python implementation

## Reproducibility Requirements

Each run directory should capture:

- session manifest
- transcript events
- summary report
- proxy stdout/stderr log
- sender log
- git SHA for the benchmark repository
- target implementation identifier, for example `go` or `python`
- proxy command and runtime flags
- relevant environment variables, redacting secrets as needed

If request-level metrics are parsed from logs, the parser must fail clearly when the expected log fields are absent rather than silently producing partial numbers.

## Open Questions

- Should the benchmark rely on `ffmpeg` or `sox` as the primary concatenation tool?
- Should word-level timing generation be part of this repository or an offline preprocessing step?
- Should request metrics be parsed from current text logs, or should the proxy grow a structured metrics output mode?
- Do we want benchmark runs to drive the proxy directly over TCP only, or later add a mode that replays through the Emacs client for UX validation?
