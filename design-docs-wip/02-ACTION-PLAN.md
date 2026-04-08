# Action Plan: Benchmark Harness Hardening And UX Regression Campaign

## Scope

This document has two phases:

1. Phase 1 covers how to run the benchmark harness, why it exists, the remaining shortcomings in the current implementation, and a detailed plan to fix them.
2. Phase 2 defines a testing campaign to compare the user experience of the Go proxy in `/work` against the older Python implementation in `/work-old-python-impl`, using the benchmark harness as a simulation of `emacs-client/strisper.el` style usage.

The immediate goal is not just to keep the scripts runnable. The goal is to make the benchmark trustworthy enough to investigate whether the Go rewrite regressed the perceived streaming UX.

## Phase 1: Harness Operation And Hardening

### Why These Scripts Exist

The repository now has a benchmark harness under `scripts/benchmark/` that simulates how `strisper.el` uses the proxy:

- audio is streamed as raw `S16_LE`, 16 kHz, mono PCM over TCP
- the proxy emits timestamped transcript lines on the same TCP connection
- the benchmark captures those emitted lines and scores the resulting stream

This matters because a speech-to-text proxy can have acceptable final transcript quality while still feeling worse in interactive use due to:

- delayed first output
- sparse or bursty updates
- unstable or repeated committed text
- over-trimming or under-trimming behavior
- poor end-of-stream flushing

The harness gives us a repeatable way to measure those behaviors without requiring a microphone or speakers.

### Script Roles

Current harness layout:

- `scripts/benchmark/concat-session.sh`
  - builds a benchmark session from one or more WAV clips
  - writes `session.wav`, `manifest.json`, `reference.txt`, and optional `reference-timings.txt`
- `scripts/benchmark/run-proxy.sh`
  - launches either the Go proxy or the Python implementation
- `scripts/benchmark/run-session.sh`
  - replays one session over TCP and captures transcript events
- `scripts/benchmark/score-run.sh`
  - computes transcript and timing metrics
- `scripts/benchmark/run-all.sh`
  - orchestrates launch, session build, replay, scoring, and result printing

Python helpers:

- `scripts/benchmark/helpers/build_manifest.py`
- `scripts/benchmark/helpers/session_client.py`
- `scripts/benchmark/helpers/score_run.py`

### How To Run The Harness

#### 1. Verify The Upstream Service

The proxies need an OpenAI-compatible transcription upstream.

In this environment, the working local development upstream is reachable at:

```bash
http://host.docker.internal:8007/v1
```

Notably, `localhost:8007` may not resolve correctly from inside the container even though the repository examples mention it.

Use the repository helper to verify the upstream:

```bash
bash tests/scripts/example-transcribe-upsream-service.sh \
  tests/test-data/LibriSpeech/2961-960-0021.wav
```

If you need to verify that the upstream supports the verbose format required by both proxies, use:

```bash
curl -sS -4 http://host.docker.internal:8007/v1/audio/transcriptions \
  -F file=@tests/test-data/LibriSpeech/2961-960-0021.wav \
  -F model=whisper-1 \
  -F response_format=verbose_json \
  -F 'timestamp_granularities[]=word' \
  -F 'timestamp_granularities[]=segment'
```

This should return JSON containing at least:

- `text`
- `words`
- `segments`

#### 2. Run The Go Proxy Through The Harness

```bash
env OPENAI_BASE_URL=http://host.docker.internal:8007/v1 \
    OPENAI_API_KEY=foobar \
    bash scripts/benchmark/run-all.sh -P -p 43120 -c 1 -i go
```

#### 3. Run The Old Python Implementation Through The Harness

```bash
env OPENAI_BASE_URL=http://host.docker.internal:8007/v1 \
    OPENAI_API_KEY=foobar \
    bash scripts/benchmark/run-all.sh -P -p 43121 -c 1 -i python
```

#### 4. Relevant Artifacts

Each run produces:

- a session directory under `tests/benchmark/sessions/`
- a run directory under `tests/benchmark/runs/`

Important run artifacts:

- `events.jsonl`
- `events.txt`
- `session-meta.json`
- `run-meta.json`
- `summary.json`
- `summary.txt`

Important proxy artifacts:

- `tests/benchmark/runs/proxy-latest/proxy-stdout.log`
- `tests/benchmark/runs/proxy-latest/proxy-stderr.log`
- `tests/benchmark/runs/proxy-latest/proxy-meta.json`

### What The Harness Currently Proves

The current implementation is already strong enough to establish:

- both proxies can be launched by the harness
- one-command end-to-end runs work for both implementations
- the harness can use a real local OpenAI-compatible upstream
- the harness can compare transcript quality, first-output timing, event cadence, and coarse timing behavior

That is enough to begin the UX comparison campaign.

### Remaining Issues

Two material shortcomings remain.

#### Issue 1: Proxy Metrics Are Not Reproducible Per Run

Proxy log metrics are still coupled to `tests/benchmark/runs/proxy-latest/`.

Current behavior:

- `run-all.sh` launches the proxy into the shared `proxy-latest` directory
- `score-run.sh` auto-discovers proxy logs from `../proxy-latest/proxy-stdout.log`

Problem:

- once a later run overwrites `proxy-latest`, rescoring an older run can lose or corrupt its request-level metrics
- this breaks reproducibility
- it makes historical comparison fragile

Impact:

- request count
- success/error count
- trim count
- upstream latency
- chunk-size summaries

cannot be trusted after subsequent runs unless the original proxy log is preserved with the run.

#### Issue 2: Duplicate Detection Still Misses Short Repeated Phrases

The improved duplicate detector now catches:

- exact repeated raw lines
- adjacent identical words
- repeated phrases with n-grams of length 3 or more

But it still misses a common regression pattern:

- short repeated segments such as `hello world`
- repeated two-word committed phrases with different timestamps

That matters because repeated short phrases are exactly the kind of output pattern that degrades user experience while not always destroying WER.

### Phase 1 Deliverables

The harness should be upgraded so that:

1. all run-specific metrics are reproducible from the run directory alone
2. repeated short phrase regressions are surfaced clearly in reports
3. the run metadata is explicit enough to compare Go and Python runs without manual reconstruction

### Detailed Fix Plan

#### Workstream A: Make Proxy Metrics Run-Local

Objective:

- eliminate dependence on `proxy-latest` for scoring completed runs

Required changes:

1. `run-all.sh`
   - after the proxy is started, copy or hard-link the active proxy artifacts into the run directory before scoring
   - expected files:
     - `proxy-stdout.log`
     - `proxy-stderr.log`
     - `proxy-meta.json`
   - if the run starts the proxy, record the launched implementation and proxy log paths in `run-meta.json`

2. `score-run.sh`
   - prefer `RUN_DIR/proxy-stdout.log` if present
   - only fall back to `../proxy-latest/proxy-stdout.log` for manual or legacy runs

3. `run-session.sh`
   - optionally accept `--proxy-log-path` in metadata if the orchestration layer wants to pass it through explicitly

Acceptance criteria:

- rescoring an old run after later benchmarks does not change `proxy_metrics`
- copying a run directory elsewhere still preserves enough data to rescore it

#### Workstream B: Strengthen Duplicate And Repetition Detection

Objective:

- detect repeated committed content that harms user experience even when timestamps differ

Required changes:

1. extend duplicate detection in `helpers/score_run.py`
   - add normalized event-text repetition counts, independent of timestamps
   - add repeated 2-gram phrase detection, not only 3+
   - add an event-level measure such as:
     - `repeated_normalized_events`
     - `top_repeated_event_text`
     - `top_repeated_event_count`

2. update `summary.json` and `summary.txt`
   - surface repeated short phrase metrics explicitly
   - make the report easy to scan for user-visible regressions

Recommended scoring additions:

- `repeated_short_phrase_words`
- `repeated_normalized_events`
- `top_repeated_event_text`
- `top_repeated_event_count`

Acceptance criteria:

- synthetic repeated `hello world` style event streams are flagged
- the earlier Python repeated-output failure mode is visible in the report without manual inspection

#### Workstream C: Preserve Comparison Context In Metadata

Objective:

- make side-by-side Go vs Python analysis easier and less error-prone

Required changes:

1. include implementation metadata in the run directory
   - target implementation
   - proxy port
   - upstream base URL
   - relevant proxy args

2. add session/run identifiers to summary output
   - `session_id`
   - `run_id`
   - `implementation`

3. add a small comparison helper or documented process
   - for example `scripts/benchmark/compare-runs.sh GO_RUN PY_RUN`
   - or a Python helper that produces a side-by-side JSON report

Acceptance criteria:

- two completed runs can be compared without opening proxy logs manually

#### Workstream D: Add Regression-Oriented Tests For The Harness

Objective:

- stop reintroducing harness regressions while the benchmark evolves

Required tests:

1. synthetic scorer tests
   - repeated exact lines
   - repeated short phrase with different timestamps
   - missing proxy log
   - malformed proxy log

2. orchestration tests
   - verify that proxy logs are copied into `RUN_DIR`
   - verify rescoring still includes `proxy_metrics`

3. session client tests
   - verify timestamp formatting is stable
   - verify empty-event and timeout behavior

Acceptance criteria:

- a later change cannot silently remove run-local reproducibility
- a later change cannot silently weaken duplicate detection

### Recommended Execution Order

1. Workstream A
2. Workstream B
3. Workstream C
4. Workstream D

Reason:

- reproducibility must come first
- then the report must surface the right regressions
- then make cross-run analysis easier
- then lock the behavior down with tests

## Phase 2: UX Regression Testing Campaign

### Purpose

There are reports that the Go rewrite regressed user experience relative to the older Python implementation.

This campaign is intended to determine whether those reports can be corroborated by the benchmark suite.

The key point is not simply to compare final WER. The campaign should investigate whether the Go proxy behaves worse in ways a user would feel while dictating via `strisper.el`.

### What “User Experience” Means In This Context

The benchmark simulates the client pattern used by `strisper.el`, so the campaign should focus on metrics that map to perceived interactive quality:

- time to first visible text
- number and pacing of visible updates
- whether text arrives smoothly or in bursts
- whether already-shown content is unnecessarily repeated
- whether the proxy truncates useful tail content
- whether end-of-stream output is flushed promptly
- whether the transcript reaches a good final state with minimal omissions

### Campaign Hypotheses

Primary hypothesis:

- the Go proxy may have worse interactive behavior than the Python implementation even if final transcript quality is similar

Candidate sub-hypotheses:

1. Go emits fewer visible updates than Python
2. Go emits later first output than Python
3. Go trims too aggressively or too conservatively
4. Go under-flushes tail content at the end of the session
5. Go repeats short phrases or replays previously committed content more often
6. Go uses larger or less frequent upstream requests, creating a burstier user experience

### Experimental Design

#### A. Test Matrix

Run each session against both implementations:

- Go: `/work`
- Python: `/work-old-python-impl`

Control the following variables:

- same session audio
- same upstream service
- same playback speed
- same frame duration
- same host environment

Recommended baseline settings:

- `-c 1`, `-c 3`, `-c 5`, and `-c 10`
- frame duration `40 ms`
- playback speed `1.0x`
- upstream `http://host.docker.internal:8007/v1`

Optional stress settings:

- playback speed `1.25x`
- playback speed `1.5x`
- smaller frame size if transport stability matters

#### B. Corpus Selection

Use at least three session types:

1. Short baseline session
   - 1 LibriSpeech clip
   - purpose: verify harness and tail-flush behavior

2. Medium streaming session
   - 3 to 5 concatenated LibriSpeech clips
   - purpose: compare first output, cadence, and repeat behavior

3. Long stress session
   - 8 to 10 concatenated LibriSpeech clips
   - purpose: compare trimming, prompt reuse, overlap handling, and end effects

Optional expansion:

- add LibriTTS sessions later for normalized text and more timing coverage

#### C. Repetitions

For each implementation and session type, run at least:

- 3 repeated runs for initial comparison
- 5 repeated runs for any scenario where the results look noisy or borderline

Why:

- upstream variance exists
- first-request warm-up effects exist
- repeated runs help separate real regressions from noise

### Metrics To Compare

#### Primary UX Metrics

- `time_to_first_word_ms`
- `event_count`
- `mean_inter_event_ms`
- repeated short phrase metrics
- repeated event metrics
- final transcript omissions

#### Supporting Metrics

- WER
- coarse timing error
- request count
- mean upstream latency
- trim count
- mean chunk size

#### Qualitative Artifact Review

For a subset of runs, review:

- `events.txt`
- final transcript in `summary.txt`
- proxy log excerpts

This is necessary because two runs can have similar aggregate metrics while still feeling different when read as a stream.

### Success Criteria For Corroborating A Regression Claim

The campaign should treat a UX regression claim as corroborated if at least one of the following is consistently worse for Go than Python across the same scenario:

1. later first output by a practically noticeable margin
2. fewer or much burstier transcript updates
3. more repeated visible content
4. worse tail completion
5. meaningfully worse final omissions despite similar or lower event count

The result should not rely on one anomalous run.

Recommended threshold style:

- require the same directional difference in at least 2 of 3 repeated runs for a scenario
- escalate to 5 runs if the result is close or noisy

### Campaign Procedure

#### Step 1: Baseline Validation

Goal:

- verify that the harness is stable before interpreting results

Tasks:

- run 1-clip session against Go
- run 1-clip session against Python
- confirm artifacts are present
- confirm reports look sane
- confirm run-local proxy metrics are preserved

#### Step 2: Medium Session Comparison

Goal:

- compare interactive behavior under realistic streaming load

Tasks:

- generate one 3-clip and one 5-clip session
- run each session 3 times against Go
- run each session 3 times against Python
- aggregate:
  - first-word latency
  - event count
  - mean inter-event gap
  - repeated-content metrics
  - WER

#### Step 3: Long Session Stress

Goal:

- expose trimming and prompt-reuse regressions

Tasks:

- generate one 8 to 10 clip session
- run 3 times against each implementation
- compare:
  - event cadence
  - trim counts
  - chunk sizes
  - omissions near the end
  - repeated-content metrics

#### Step 4: Targeted Follow-Up

Goal:

- investigate any suspicious pattern found in earlier steps

Possible follow-ups:

- smaller `frame_ms`
- faster playback
- sessions emphasizing silence boundaries
- replay specific clips reported by users as problematic

### Outputs

The campaign should produce:

1. a run inventory
   - implementation
   - session id
   - run id
   - config

2. a comparison table
   - one row per run
   - one summary row per scenario

3. a narrative assessment
   - does the benchmark corroborate a UX regression?
   - if yes, in what specific way?
   - if no, what evidence argues against it?

4. a shortlist of likely root causes
   - for example:
     - request sizing
     - trimming policy
     - overlap behavior
     - tail flush behavior

### Recommended Analysis Questions

After the runs complete, answer these directly:

1. Does Go produce first visible text later than Python?
2. Does Go produce fewer visible updates?
3. Does Go bunch updates into larger bursts?
4. Does Go lose more text at the end?
5. Does Go repeat short phrases more often?
6. Are any observed UX differences explained by request cadence, trim count, or chunk size?

### Exit Criteria

Phase 2 is complete when:

- both implementations have been run across the agreed scenario matrix
- repeated runs exist for each scenario
- the comparison table and narrative summary are written
- the team can say whether the reported UX regression is:
  - corroborated
  - not corroborated
  - inconclusive pending more instrumentation

## Immediate Next Actions

1. Fix run-local proxy metric preservation.
2. Improve repeated short phrase detection.
3. Add minimal scorer tests for repeated phrase regressions.
4. Run the Phase 2 baseline and medium-session comparison matrix against `http://host.docker.internal:8007/v1`.
