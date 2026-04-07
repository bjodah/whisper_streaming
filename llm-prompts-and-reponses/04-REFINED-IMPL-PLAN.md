# Refined Implementation Plan for the Go Streaming Transcription Proxy

## Purpose

This document supersedes `01-IMPL-PLAN.md` by incorporating feedback from:

- `02-SECOND-OPINION.md`
- `03-THIRD-OPINION.md`

It preserves the original diagnosis, corrects a few ambiguities, and expands the plan into a more detailed, execution-oriented implementation roadmap.

The goal is not just parity with the Python branch, but a safer and more observable Go implementation that:

- reduces live transcription latency
- prevents unbounded clip growth
- avoids wasted upstream work after disconnects
- improves correctness around overlap and trimming
- remains operationally simple

## Scope

This plan is focused on the Go proxy on branch `golang-rewrite`, compared against the Python implementation on branch `bjodah-customization`.

Primary concerns:

1. The Go branch appears to pause longer before sending audio upstream.
2. Both branches can send increasingly long clips upstream, reaching multi-minute durations.
3. Long requests increase latency and cost.
4. The Go branch dropped several behaviors present in Python or simplified them too aggressively.
5. The Go branch also has some Go-specific lifecycle and memory issues that were not captured in the first plan.

Non-goals for this document:

- direct microphone capture
- full VAD parity with Python's optional Silero VAC mode
- transport/protocol redesign

Those can be revisited later, but they are not on the critical path for fixing the current latency and clip-growth problems.

Important refinement:

- optional VAD support is worth planning for
- but it should not be treated as a prerequisite for the core latency and clip-growth fixes
- it should land only after the base stream processor is correct, observable, cancelable, and window-bounded

## Consolidated Findings

## 1. The Go scheduler adds avoidable latency

The current `StreamProcessor.Run()` is ticker-driven. After each upstream transcription completes, the loop waits for the next ticker event before checking whether enough audio is already buffered for the next request.

Impact:

- adds avoidable idle time after each upstream call
- directly explains why the Go branch can feel slower than Python

This is still the highest-priority issue because it is both high impact and low risk to fix.

## 2. There is still no real cap on clip duration

`--buffer-trimming-sec` is only a trimming threshold. It is not a hard cap on the size of the audio window sent upstream.

Current behavior:

- the retained buffer is repeatedly retranscribed
- trimming only occurs once the threshold is crossed
- trimming only advances to a safe committed point
- if commitment stalls, requests keep growing

Impact:

- upstream clip durations can grow to minutes
- longer requests increase latency and increase the likelihood of further latency

## 3. The Go branch regressed from Python in overlap handling and context management

The Python implementation includes several continuity-preserving mechanisms that the Go rewrite either dropped or simplified:

- stronger hypothesis matching
- overlap deduplication via short n-gram matching
- prompt reuse from scrolled-off committed text
- richer trimming logic

Impact:

- slower commitment
- less trimming opportunity
- more unstable edges when smaller windows are used

## 4. Segment trimming needs a more careful framing

The original plan said the Go rewrite should restore segment-aware trimming to match Python. That needs refinement.

Important nuance:

- the Python code path for some backends uses actual segment timestamps
- but the Python OpenAI backend's `segments_end_ts()` appears to derive end times from `res.words`, not `res.segments`

Implication:

- true segment-aware trimming may be an improvement beyond Python parity, not strictly a restoration of parity, depending on which Python backend was used during comparison

This does not invalidate the idea. It means the plan should distinguish between:

- parity with the currently observed Python/OpenAI behavior
- an intentional improvement using real segment boundaries if the upstream API provides them

## 5. The Go rewrite lacks proper request cancellation

The current code does not propagate `context.Context` from the TCP connection lifecycle into the upstream HTTP request.

Impact:

- if the client disconnects while an upstream request is in flight, the request keeps running
- the goroutine and upstream resources remain in use until the request finishes or fails
- this wastes budget and delays cleanup

This is a critical correctness and operational issue and should be treated as part of the first implementation phase.

## 6. The current trimming approach can retain oversized backing arrays

The current buffer trimming pattern reslices the existing slice. In Go, that keeps the original backing array alive.

Impact:

- long-lived streams can retain large backing arrays even after logical trimming
- memory growth may be larger than `len(audioBuf)` suggests

This does not necessarily explode immediately, but it is cheap to fix and should be addressed while refactoring buffer management.

## 7. Error handling currently creates a bad feedback loop

If an upstream transcription attempt fails or times out:

- the failed request's audio remains in the retained buffer
- more audio accumulates before the next attempt
- the next attempt becomes even larger

Impact:

- timeout or transient failure can cause request size to grow further
- this increases the chance of repeated failure

The first plan mentioned timeouts but not the retry-window consequences. The refined plan includes explicit failure containment behavior.

## 8. Observability is too weak for confident tuning

The code currently lacks enough request-scoped logging to answer basic operational questions per stream:

- how much audio was sent
- how long the upstream call took
- whether trimming happened
- whether a cap was applied
- whether a request was canceled due to disconnect

This makes tuning and regression detection slower than necessary.

## 9. Server lifecycle and concurrency are under-specified

The current server accepts connections indefinitely with no graceful shutdown path and no concurrency limits.

Impact:

- difficult to stop cleanly
- no drain behavior on SIGTERM
- risk of unbounded concurrent upstream work

These are not the root cause of the current latency issue, but they should be addressed before calling the rewrite production-ready.

## Refined Strategic Position

The original plan was directionally correct, but the rollout order should change.

Most important refinement:

- do not introduce a hard `max-clip-length` early as the primary fix

Reason:

- if commitment logic and context reuse are still weak, a hard cap causes more boundary artifacts
- if error handling is still weak, a cap alone does not fix failure behavior
- if scheduling still adds idle time, clip size is only part of the latency picture

The hard cap should be introduced after:

- scheduling is fixed
- cancellation and timeouts exist
- observability exists
- hypothesis logic is stronger
- prompt/context handling is in place or at least designed concretely

In other words, `max-clip-length` is the safety net, not the first crutch.

## Architectural Principles

These principles should guide implementation choices.

### 1. One in-flight upstream transcription per TCP connection

Do not pipeline multiple upstream transcriptions for one connection in the first pass. Keep behavior deterministic and easier to debug.

### 2. Threshold-driven processing, not periodic polling

Work should start as soon as sufficient new audio is available and no request is currently in flight.

### 3. Explicit time domains

The code should distinguish clearly between:

- absolute stream time
- retained buffer start time
- current send-window start time

These must not be conflated.

### 4. Cancellation is part of correctness

TCP disconnects and server shutdown must cancel in-flight HTTP requests.

### 5. Bound all growth

Bound:

- upstream request duration
- retained audio size
- number of concurrent connections
- request lifetime

### 6. Improve parity before adding speculative features

Focus first on fixing latency, commitment, trimming, and cancellation. Defer optional silence detection and microphone capture until the core path is stable.

## Data Model Refinement

The first plan identified the need for correct offset handling when clip windowing is introduced. This plan makes that explicit.

Recommended stream state concepts:

- `bufferOffsetSec`
  - absolute start time of the retained audio buffer
- `sendWindowOffsetSec`
  - absolute start time of the audio window sent in the current upstream request
- `lastCommittedTimeSec`
  - absolute timestamp of the latest committed word
- `lastProcessedBufferLenBytes`
  - number of retained-buffer bytes already represented in the previous request scheduling state

Important rule:

- hypothesis processing must use the absolute start time of the sent window, not merely the absolute start time of the retained buffer

That distinction becomes essential once `max-clip-length-sec` is introduced.

## Refined Rollout Order

The updated order is:

1. Phase A: Instrumentation, context cancellation, timeouts, and scheduler refactor
2. Phase B: API response model expansion and internal state refactor
3. Phase C: Hypothesis buffer parity improvements
4. Phase D: Trim logic improvements and memory-safe buffer management
5. Phase E: Prompt/context reuse
6. Phase F: Hard max clip length with overlap
7. Phase G: Failure handling, connection controls, and graceful shutdown
8. Phase H: Optional VAD support
9. Phase I: Integration tests, tuning, and optional follow-up work

This differs from the original plan in two important ways:

- scheduling and observability are merged into the first phase
- hard clip capping is postponed until correctness and continuity are better supported
- optional VAD is explicitly recognized as valuable, but it is sequenced after the core path is fixed

## Detailed Implementation Plan

## Phase A: Instrumentation, Context Cancellation, Timeouts, and Scheduler Refactor

### Objectives

1. Remove the avoidable scheduler-induced latency.
2. Ensure disconnects cancel in-flight upstream requests.
3. Add enough logging to measure improvement immediately.
4. Prevent indefinite upstream hangs.

### Code areas

- `internal/processor/stream.go`
- `internal/api/openai.go`
- `internal/server/tcp.go`
- `cmd/whisper-proxy/main.go`

### Detailed tasks

1. Thread `context.Context` through connection handling.
2. Create one connection-scoped context per TCP connection.
3. Cancel that context when:
   - socket read returns EOF or fatal error
   - write to client fails terminally
   - server shutdown begins
4. Change the API client to use `http.NewRequestWithContext(...)`.
5. Add configurable HTTP timeout behavior.
6. Replace the fixed ticker processing loop with threshold-driven scheduling.

### Scheduler design

Recommended shape:

1. Reader goroutine appends PCM bytes into the retained buffer.
2. Reader goroutine signals that new audio has arrived.
3. Processing loop wakes on:
   - new audio arrival
   - connection shutdown
   - completion of current upstream request
4. Processing loop checks:
   - do we have at least `minChunk` of new audio relative to the current scheduling state?
   - is an upstream request already in flight?
5. If enough audio is available and no request is in flight, build the next send window immediately and issue the request.
6. When the request completes, re-check the buffer immediately before blocking again.

### Logging requirements

Every upstream request should log at least:

- connection ID
- request sequence number
- retained buffer duration
- sent audio duration
- send window offset
- whether a hard cap was applied
- upstream latency
- returned word count
- returned segment count if present
- whether the request ended in success, timeout, cancellation, or upstream error

### Timeout behavior

Initial policy:

- use a bounded timeout from configuration
- make the default conservative

Recommended first default:

- `30s`

Later improvement:

- scale timeout to clip duration once `max-clip-length-sec` exists

### Deliverables

1. No fixed ticker in stream processing.
2. Context-aware upstream requests.
3. Structured request logs with connection correlation.
4. Configurable HTTP timeout.

### Exit criteria

- a disconnected client cancels an in-flight request promptly
- consecutive requests are sent without waiting for the next periodic tick
- logs can show the before/after latency profile

## Phase B: API Response Model Expansion and Stream State Refactor

### Objectives

1. Prepare the code for better trimming and prompt handling.
2. Make time and window semantics explicit.

### Code areas

- `internal/api/openai.go`
- `internal/processor/stream.go`
- `internal/processor/hypothesis.go`

### Detailed tasks

1. Expand `TranscriptionResponse` to include segments if available.
2. Define a `Segment` type with at least:
   - `Start`
   - `End`
   - optionally `Text` if useful for debugging
3. Request both:
   - `timestamp_granularities[] = word`
   - `timestamp_granularities[] = segment`
4. Refactor processing code so send-window metadata is explicit.
5. Add helper functions to:
   - compute retained buffer duration
   - compute send window boundaries
   - map relative timestamps from upstream into absolute stream timestamps

### Important design note

At this stage, segment data is introduced as capability, not yet relied upon for all trimming decisions. This keeps the refactor incremental.

### Deliverables

1. Extended API response parsing.
2. Explicit send-window offset model.
3. Internal helpers for absolute timestamp normalization.

## Phase C: Hypothesis Buffer Parity Improvements

### Objectives

1. Make commitment behavior closer to Python.
2. Improve trimming opportunities by making commitment more reliable.
3. Reduce duplication and unstable overlap behavior.

### Why this phase comes before the hard cap

If commitment remains weak, hard-capped windows will amplify transcript boundary instability.

### Code areas

- `internal/processor/hypothesis.go`
- tests under `internal/processor`

### Detailed tasks

Refactor the Go hypothesis buffer to mirror the Python model more closely:

1. Keep separate state for:
   - committed words still relevant to buffer overlap
   - previous uncommitted hypothesis
   - newly returned hypothesis
2. Filter out words clearly before `lastCommittedTimeSec`.
3. Add short n-gram overlap removal against recently committed words.
4. Compute the longest common prefix between the previous uncommitted hypothesis and the new hypothesis.
5. Commit only the stable prefix.
6. Retain the unstable tail for later confirmation.

### Recommended constraints

- preserve absolute timestamps internally once shifted
- normalize text conservatively
- do not over-collapse punctuation or spacing unless tested

### Deliverables

1. Richer hypothesis state.
2. Overlap deduplication.
3. More faithful LocalAgreement-style behavior.

### Exit criteria

- repeated overlap no longer causes obvious duplication
- commitment advances on representative cases where the old Go implementation stalled

## Phase D: Trim Logic Improvements and Memory-Safe Buffer Management

### Objectives

1. Improve retained-buffer trimming behavior.
2. Prevent buffer-memory retention from reslicing.
3. Clarify parity vs intentional improvement for segment trimming.

### Code areas

- `internal/processor/stream.go`
- `internal/api/openai.go`

### Trim strategy hierarchy

Use a prioritized trim strategy:

1. Prefer trimming at safe completed boundaries if available.
2. Fall back to committed-time-based trimming.
3. Never trim beyond what the hypothesis layer considers safely committed.

### Important parity note

Because Python's OpenAI path may be effectively using word-end times rather than real segment ends, implement this phase in two steps:

#### Step D1: parity-safe trimming

- improve trimming decisions using the currently available safe committed boundary logic
- if segment-like boundaries are available but not trustworthy, do not force dependence on them yet

#### Step D2: optional real segment-boundary improvement

- if the upstream response returns trustworthy `segments`
- and the data behaves as expected in tests
- prefer trimming at the penultimate completed segment within committed territory

### Memory-safe trimming requirement

Whenever a substantial prefix is dropped from `audioBuf`, ensure the retained slice does not indefinitely hold a huge backing array.

Implementation options:

1. simple copy-on-trim
2. copy only when capacity exceeds a multiple of length
3. replace the linear slice with a ring buffer later if needed

Recommended initial choice:

- simple copy-on-trim with a capacity sanity threshold

This is simpler than a ring buffer and sufficient for the expected window sizes once capping exists.

### Deliverables

1. better trim decisions
2. memory-safe retained buffer management
3. clearer separation between:
   - parity-safe trimming
   - optional real segment trimming improvement

## Phase E: Prompt/Context Reuse

### Objectives

1. Preserve continuity when the active audio window shrinks.
2. Reduce edge artifacts before the hard clip cap lands.

### Code areas

- `internal/processor/stream.go`
- `internal/api/openai.go`
- possibly `internal/processor/hypothesis.go`

### Detailed tasks

1. Track committed words that are no longer inside the active retained audio buffer.
2. Build a bounded prompt from the trailing committed text outside the active window.
3. Add optional prompt support to `api.Client.Transcribe(...)`.
4. Send the prompt in multipart form only when non-empty.

### Recommended initial policy

- mirror Python's rough 200-character trailing context budget

### Design rules

- prompt text must come only from committed material
- do not include unstable tail text
- do not let prompt construction affect absolute timestamp logic

### Deliverables

1. prompt-aware transcription requests
2. bounded prompt extraction from committed history

## Phase F: Hard Max Clip Length with Overlap

### Objectives

1. Bound upstream request duration.
2. Stabilize tail latency.
3. Keep enough overlap to soften clip boundaries.

### Why this phase lands here

By this point:

- scheduler latency is fixed
- requests are cancelable
- trimming is better
- hypothesis commitment is stronger
- prompt reuse exists

That makes the hard cap much safer to introduce.

### New configuration

Add:

- `--max-clip-length-sec`
- `--clip-overlap-sec`

Recommended initial defaults:

- `max-clip-length-sec = 20.0`
- `clip-overlap-sec = 2.0`

### Detailed windowing rules

1. Let the retained buffer span:
   - `[bufferOffsetSec, bufferOffsetSec + retainedDurationSec)`
2. Build the send window as:
   - full retained buffer if retained duration is within cap
   - otherwise a suffix window ending at the latest available audio and bounded by `max-clip-length-sec`
3. Include overlap by allowing the window start to move earlier, up to `clip-overlap-sec`, while staying within the retained buffer.
4. Record the send window start as `sendWindowOffsetSec`.
5. Normalize all returned timestamps against `sendWindowOffsetSec`.

### Important correctness conditions

- timestamps sent to the client remain absolute and monotonic
- overlap does not cause duplicate committed output
- hypothesis state is robust to repeated words across overlap windows

### Failure behavior

If a capped request fails:

- do not automatically allow the next request window to grow beyond the cap
- keep retry attempts bounded to a safe send window

### Deliverables

1. hard cap on request duration
2. overlap-aware send window construction
3. timestamp normalization using send-window offset

## Phase G: Failure Handling, Connection Controls, and Graceful Shutdown

### Objectives

1. Prevent runaway retry growth after failures.
2. Bound concurrency.
3. Support clean shutdown.

### Code areas

- `internal/server/tcp.go`
- `internal/processor/stream.go`
- `cmd/whisper-proxy/main.go`

### Failure handling tasks

1. Distinguish between:
   - context cancellation
   - timeout
   - upstream HTTP error
   - decode/parsing error
2. On transient request failure:
   - keep retry windows bounded
   - avoid turning the next attempt into an even larger request
3. Add simple retry/backoff rules only if needed.

Recommended initial posture:

- no aggressive automatic retries in the first implementation
- prefer bounded next-attempt behavior over hidden retry loops

### Connection limiting

Add a configurable maximum number of concurrent connections.

Recommended initial default:

- something conservative such as `10`

If the limit is reached:

- reject new connections cleanly and log the event

### Graceful shutdown

Add signal handling so the server can:

1. stop accepting new connections
2. cancel active connection contexts
3. allow a short drain period if desired
4. exit cleanly

### Deliverables

1. bounded post-failure behavior
2. concurrency limit
3. signal-aware graceful shutdown

## Phase H: Testing, Benchmarking, and Optional Follow-Up Work

## Phase H: Optional VAD Support

### Objectives

1. Reduce silence-induced latency.
2. Flush earlier at utterance boundaries.
3. Avoid sending unnecessary silence upstream.
4. Keep VAD optional so the proxy can preserve its current lightweight default deployment profile.

### Why VAD is not in the core path

VAD is valuable, but it is not the first fix to make.

Reasons:

- the largest current latency issue is the scheduler
- clip growth is primarily a windowing and trimming problem
- adding VAD before the base processor is correct makes debugging harder
- a neural VAD backend introduces new operational dependencies

Therefore:

- VAD should be added only after Phases A through G
- the proxy should remain fully usable with VAD disabled

### Strategic position

Implement VAD as an optional endpointing and silence-gating layer, not as a required dependency of the stream processor.

Recommended user-facing configuration:

- `--vad=off|rms|silero`

Recommended default:

- `off`

Recommended rollout:

1. `off`
2. `rms`
3. optional `silero`

### Backend strategy

#### `off`

Behavior:

- current stream processor behavior without voice gating

Use case:

- default deployment
- simplest operational mode
- preserves current dependency profile

#### `rms`

Behavior:

- use a lightweight energy-based gate over incoming PCM
- detect likely speech / non-speech transitions from short rolling frames
- on sustained silence after speech, trigger an earlier flush opportunity

Advantages:

- no ONNX runtime
- no CGO requirement
- easier to test and tune
- good first step for cost and latency reduction on silence

Limitations:

- less accurate than model-based VAD
- requires threshold tuning for noisy environments

#### `silero`

Behavior:

- use Silero VAD via ONNX as an optional backend
- higher-quality speech endpointing than naive RMS gating

Advantages:

- better utterance boundary detection
- better resistance to background noise than a simple RMS threshold

Costs:

- likely breaks the current “zero C-dependencies / single static binary” positioning depending on the chosen Go wrapper and runtime packaging
- introduces model/runtime distribution concerns
- raises build and deployment complexity

### Architectural design

Introduce a small VAD interface, for example:

- `Reset()`
- `Process(pcm []byte) ([]VADEvent, error)`
- optional `Flush() []VADEvent`

Where `VADEvent` can represent:

- speech start
- speech end
- maybe intermediate state if needed

The stream processor should not depend on a specific VAD implementation. It should depend only on:

- whether speech is currently active
- whether a speech segment just ended
- whether an early flush should be triggered

### Integration point in the pipeline

Recommended placement:

1. audio arrives from TCP
2. audio is appended to the retained buffer
3. optional VAD processes the new PCM incrementally
4. VAD may emit one of:
   - no event
   - speech start
   - speech end
5. the scheduler then decides whether to:
   - keep accumulating
   - process immediately because `min-chunk-size` is reached
   - process early because VAD observed end-of-speech

Important rule:

- VAD should accelerate or shape request timing
- it should not replace the core buffer/window/hypothesis logic

### Detailed RMS design

The first VAD backend should be a simple RMS-based detector.

Suggested implementation shape:

1. convert incoming `S16_LE` PCM frames to normalized sample amplitudes
2. evaluate short rolling windows, for example:
   - 20ms to 30ms frame size
   - small hangover / hysteresis window
3. maintain state such as:
   - currently in speech
   - consecutive silent frames
   - consecutive speech frames
4. trigger:
   - speech start after enough speech frames
   - speech end after enough silence frames following speech

Suggested initial tuning knobs:

- `--vad-rms-threshold`
- `--vad-min-speech-ms`
- `--vad-min-silence-ms`
- `--vad-speech-pad-ms`

Recommended initial behavior:

- if VAD observes speech end and there is buffered speech not yet processed, schedule an immediate transcription request even if the next regular threshold wakeup has not happened yet

### Detailed Silero design

Silero should be treated as a separately packaged backend.

Recommended constraints:

1. do not make Silero the default
2. do not make Silero a mandatory dependency of the base server build
3. isolate it behind:
   - build tags, or
   - a separate package/module, or
   - a distinct optional binary flavor

Reason:

- many Go Silero/ONNX integrations introduce CGO and runtime packaging complexity
- that conflicts with the current repo messaging around portability and zero C-dependencies

### Interaction with clip capping and prompt reuse

VAD changes when the proxy chooses to send audio, so it must interact cleanly with existing phases.

Rules:

1. VAD-triggered sends must still respect `max-clip-length-sec`.
2. VAD-triggered sends must still use overlap and prompt/context reuse where configured.
3. VAD-triggered sends must still normalize timestamps using the send-window offset.
4. VAD must not bypass hypothesis commitment logic.

### Failure and fallback behavior

If VAD fails:

- log the failure with the connection ID
- either disable VAD for that stream or fail back to `off`
- do not break core transcription

For `silero`, if model/runtime initialization fails:

- either fail startup clearly when `--vad=silero` is explicitly requested
- or downgrade to `off` only if that behavior is intentionally configured

### Deliverables

1. pluggable VAD interface
2. `off` mode
3. `rms` backend
4. optional `silero` backend design or implementation
5. VAD-aware early flush scheduling

### Tests

Add tests for:

- silence does not repeatedly trigger flushes
- speech end triggers earlier processing than chunk-size-only scheduling
- VAD events do not break timestamp monotonicity
- `off` mode preserves previous behavior
- `rms` backend behaves sensibly on synthetic PCM fixtures

Integration tests should cover:

- speech followed by silence
- long silence before speech
- alternating speech/silence
- disconnect during VAD-enabled processing

### Success criteria

VAD is successful if it:

1. reduces silence sent upstream
2. improves end-of-utterance latency
3. does not degrade transcript continuity materially
4. remains optional and operationally isolated

## Phase I: Testing, Benchmarking, and Optional Follow-Up Work

### Objectives

1. Make the new behavior safe to evolve.
2. Validate latency and quality tradeoffs with evidence.

### Unit test additions

#### Hypothesis tests

- overlap deduplication
- longest common prefix commitment
- timestamp normalization from send window offsets
- behavior around clip overlap
- behavior after committed-time trims

#### Buffer/window tests

- retained buffer trimming correctness
- memory-safe trim behavior
- send-window construction with cap and overlap
- fallback when retained buffer is shorter than cap

#### API client tests

- prompt field included only when present
- word and segment granularities requested
- timeout/cancel behavior observable in tests

### Integration tests

Add an end-to-end test harness with:

- a mock upstream transcription service
- a local TCP client that feeds known PCM at controlled pacing
- assertions on:
  - request timing
  - clip durations
  - offsets
  - emitted transcript timing
  - cancellation on disconnect

This is more valuable than unit tests alone because many of the important failure modes are cross-component.

### Benchmarking

Measure:

- request cadence before and after scheduler refactor
- average sent clip duration
- tail latency
- memory usage under long-lived streams
- copy volume if practical

### Optional follow-up items

These are explicitly out of the core plan but worth tracking:

1. ring buffer instead of copy-on-trim
2. dynamic timeout proportional to clip duration
3. optional client tooling for microphone capture

Priority recommendation:

- consider only post-VAD refinements here after the core phases above are validated

## Config Additions Summary

Recommended new or refined configuration fields:

- `--http-timeout-sec`
- `--max-clip-length-sec`
- `--clip-overlap-sec`
- `--max-connections`

Existing configuration to keep:

- `--min-chunk-size`
- `--buffer-trimming-sec`

Potential future config, not required now:

- graceful shutdown drain timeout

## Recommended Defaults

Initial recommended defaults:

- `min-chunk-size = 1.0s`
- `buffer-trimming-sec = 15.0s`
- `http-timeout-sec = 30.0s`
- `max-clip-length-sec = 20.0s`
- `clip-overlap-sec = 2.0s`
- `max-connections = 10`

These should be treated as starting values, not finalized tuning.

## Risks and Mitigations

## Risk: hard cap introduces transcript edge artifacts

Mitigations:

- strengthen hypothesis logic before enabling the cap
- add overlap
- add prompt reuse
- use careful absolute offset normalization

## Risk: cancellation introduces partial-state bugs

Mitigations:

- keep one in-flight request per connection
- make request completion and cancellation paths explicit
- cover disconnect behavior in integration tests

## Risk: trimming becomes too aggressive

Mitigations:

- trim only within safely committed territory
- prefer incremental rollout
- keep committed-time fallback

## Risk: memory optimizations complicate correctness

Mitigations:

- start with copy-on-trim, not a ring buffer
- add ring buffer only if profiling justifies it

## Risk: phase scope creep

Mitigations:

- defer microphone capture
- defer VAD beyond a simple future roadmap note
- avoid adding speculative features before the latency path is stable

## Final Recommended Execution Sequence

The recommended implementation order is:

1. Phase A
   - instrumentation
   - context cancellation
   - timeouts
   - scheduler refactor
2. Phase B
   - API response model expansion
   - explicit send-window state
3. Phase C
   - hypothesis parity improvements
4. Phase D
   - trim logic improvements
   - memory-safe buffer management
5. Phase E
   - prompt/context reuse
6. Phase F
   - hard max clip length with overlap
7. Phase G
   - failure containment
   - connection limit
   - graceful shutdown
8. Phase H
   - optional VAD support
   - `off|rms|silero` sequencing
9. Phase I
   - integration tests
   - benchmarking
   - optional follow-up evaluation

## Expected Outcome

If the plan above is executed in order, the Go rewrite should improve in the following ways:

1. shorter perceived pauses between upstream requests
2. bounded and more predictable clip durations
3. lower tail latency for live transcription
4. better continuity when smaller windows are used
5. less wasted work after disconnects and timeouts
6. safer memory behavior for long-lived streams
7. a clearer operational picture through structured logs and tests

The key refinement relative to `01-IMPL-PLAN.md` is that the first wins should come from lifecycle correctness, scheduling, and observability, while hard clip capping should land only after the hypothesis and continuity story is strong enough to support it.
