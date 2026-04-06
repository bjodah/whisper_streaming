# Streaming Transcription Proxy: Problem Summary, Findings, and Implementation Plan

## Problem Summary

We are comparing two branches of the same streaming transcription proxy:

- `bjodah-customization`: original Python implementation
- `golang-rewrite`: current Go rewrite

Observed problems:

1. The Go rewrite appears to introduce longer pauses before chunks are sent upstream for transcription compared to the Python version.
2. Both branches appear to send progressively longer clips to the upstream Whisper-compatible transcription service.
3. In upstream service logs, clip lengths continue to grow, at least into multi-minute requests.
4. Those long requests add significant latency to the live transcription experience.
5. A possible mitigation is to introduce a configurable maximum clip length, for example 20 seconds, but this raises concern about reduced quality near clip boundaries.
6. There is also interest in identifying any additional shortcomings in the Go rewrite.

## Findings

### 1. The Go rewrite adds avoidable scheduling delay

The Go implementation uses a fixed ticker in `internal/processor/stream.go` to decide when to process audio. After each transcription request finishes, it waits for the next tick before checking whether enough new audio is already buffered.

Effect:

- This can add up to roughly one extra `min-chunk-size` interval of idle delay after every upstream call.
- That directly matches the observed symptom that the Go branch feels like it pauses longer than Python.

Why Python behaves differently:

- The Python server blocks until it has enough audio, then processes immediately.
- It does not impose an extra periodic scheduling wait after the previous request finishes.

### 2. There is currently no true maximum clip length in the Go rewrite

The Go branch has `--buffer-trimming-sec`, but this is not a hard cap on request duration.

Current Go behavior:

- The entire retained buffer is retranscribed on each request.
- Trimming only happens if buffer length exceeds the threshold.
- Even then, trimming only advances to the last committed timestamp.
- If commitment stalls, the request length keeps growing.

Effect:

- Clip duration can continue to grow well beyond the trimming threshold.
- Multi-minute upstream requests are therefore expected under some conditions.

Conclusion:

- A dedicated `max-clip-length` setting does make sense.
- It should be treated as a hard operational cap, not as a soft trimming threshold.

### 3. The Python branch also accumulates long audio, but it has stronger trimming logic

The Python branch is not immune to long accumulated context. It also retranscribes buffered audio and can grow requests. However, it has better mechanisms for cutting the buffer at stable boundaries.

Python advantages:

- It asks the upstream service for both word and segment timestamps.
- It trims at the penultimate completed segment when the buffer grows beyond the threshold.
- It can preserve more stable chunk boundaries than the Go rewrite currently can.

Go gap:

- The Go client currently only requests word timestamps.
- That removes the segment-based trimming strategy entirely.

### 4. The Go rewrite dropped prompt reuse from prior committed text

The Python implementation constructs a prompt from committed text that has already scrolled out of the active audio buffer and sends that prompt with each transcription request.

Purpose:

- Preserve continuity after chunking
- Reduce edge artifacts
- Improve consistency when the active audio window is shortened

Go gap:

- The Go client does not currently send any prompt at all.

Effect:

- If we introduce a hard clip cap without restoring prompt/context reuse, transcript quality near clip boundaries is more likely to degrade.

### 5. The Go hypothesis logic is simpler and more brittle than Python

The Python branch uses richer overlap handling:

- It keeps committed words in buffer
- It removes repeated overlap via short n-gram matching
- It then commits the longest common prefix across iterations

The Go branch:

- Compares only a normalized common prefix between previous and current word lists
- Does not replicate the same overlap-deduplication behavior
- Does not retain the same richer committed-buffer semantics

Effect:

- Commitment can stall more easily
- Delayed commitment also delays buffer trimming
- That increases the chance of long upstream clips

### 6. The Go HTTP client has no timeout

The Go API client uses the default `http.Client` without configuring request timeouts.

Effect:

- Slow or stuck upstream requests can block a stream processor indefinitely
- This can amplify the perception of pauses and create poor failure behavior

## Recommendation Summary

Yes, introducing a configurable `max-clip-length` is justified.

Recommended default:

- `20s` maximum clip length

However, a clip cap alone is not sufficient. The recommended fix is a combination of:

1. Remove the avoidable scheduling delay in the Go processor.
2. Add a hard `max-clip-length` with a small overlap window.
3. Restore segment-aware trimming.
4. Restore prompt reuse from committed text.
5. Strengthen the Go hypothesis logic to match Python more closely.
6. Add HTTP timeouts and observability around upstream latency and clip duration.

## Detailed Implementation Plan

## Phase 1: Fix scheduling behavior in the Go stream processor

### Objective

Remove the extra idle wait between consecutive upstream transcription requests.

### Current issue

`StreamProcessor.Run()` is driven by a fixed ticker. That means work is checked periodically rather than being scheduled immediately when enough audio is available.

### Planned change

Refactor `internal/processor/stream.go` so processing becomes threshold-driven instead of ticker-driven.

### Proposed design

1. Keep the reader goroutine that continuously appends PCM bytes into `audioBuf`.
2. Add a signaling mechanism from the reader to the processing loop, for example:
   - a buffered channel that indicates “new audio arrived”, or
   - a condition variable on buffer growth.
3. In the processing loop:
   - check whether `len(audioBuf) - lastProcessedLen >= minChunkBytes`
   - if true and no request is in flight, start processing immediately
   - after a request completes, re-check the buffer immediately before waiting again
4. Preserve sequential upstream requests per connection unless there is a deliberate reason to allow pipelining.

### Expected result

- No extra post-request waiting
- Lower perceived latency
- Behavior closer to Python

### Implementation notes

- Avoid spawning overlapping upstream requests for the same connection in the first iteration of this change.
- Keep the logic single-flight per stream until metrics show a need for more concurrency.

## Phase 2: Introduce a hard max clip length with overlap

### Objective

Prevent upstream request durations from growing without bound.

### New configuration

Add the following command-line options and config fields:

- `--max-clip-length-sec`
- `--clip-overlap-sec`

Recommended defaults:

- `max-clip-length-sec = 20.0`
- `clip-overlap-sec = 2.0`

### Semantics

- `max-clip-length-sec` is a hard cap on the audio sent in a single upstream request.
- `clip-overlap-sec` keeps a small amount of previous audio in the request window to reduce boundary artifacts.

### Planned behavior

When preparing the next upstream request:

1. Determine the active audio window from `audioBuf`.
2. If buffer duration is less than or equal to `max-clip-length-sec`, send it all.
3. If it exceeds the cap:
   - send only the newest `max-clip-length-sec` window
   - include up to `clip-overlap-sec` of prior overlap where appropriate
   - compute the correct transcription time offset for the chosen window

### Important constraint

The overlap must be reflected in timestamp normalization and hypothesis matching so output timestamps remain monotonic and correct.

### Expected result

- Upper bound on upstream request size
- More predictable latency and cost
- Better live-stream responsiveness

## Phase 3: Restore segment-aware trimming

### Objective

Trim the rolling buffer at safe semantic boundaries, not only at the last committed word.

### Current issue

The Go client only requests word timestamps, so it cannot use the Python segment-based trimming strategy.

### Planned change

Extend the Go API response types to include segment timestamps and request:

- `timestamp_granularities[] = word`
- `timestamp_granularities[] = segment`

### Data model updates

In `internal/api/openai.go`:

1. Add a `Segment` type with at least:
   - `Start`
   - `End`
   - any additional fields needed later
2. Add `Segments []Segment` to `TranscriptionResponse`

### Processor updates

In `internal/processor/stream.go`:

1. Replace or supplement `trimBuffer()` with segment-based chunking logic.
2. Mirror the Python behavior:
   - if the buffer exceeds the trimming threshold
   - look at returned segment end times
   - trim at the penultimate completed segment if it is within committed territory
3. Keep the existing committed-time-based fallback if no safe segment boundary exists.

### Expected result

- Earlier and safer buffer cuts
- Lower chance of multi-minute clips
- Closer parity with Python behavior

## Phase 4: Reintroduce prompt/context reuse

### Objective

Preserve transcription continuity when clips are bounded more aggressively.

### Current issue

A hard clip cap without context reuse increases the risk of transcript drift and edge artifacts.

### Planned change

Port the Python prompt construction concept into the Go rewrite.

### Proposed design

Track committed words separately from the active audio buffer. For each upstream request:

1. Identify committed text that lies entirely before the active audio window.
2. Build a prompt from the trailing committed context, capped to a reasonable size such as:
   - 200 characters, matching Python, or
   - a similar bounded token budget
3. Send that prompt in the transcription request when supported by the upstream service.

### Data structure updates

Add state to the processor or hypothesis buffer for:

- committed words retained outside the current audio window
- active buffer offset
- utility methods to extract prompt text

### API client updates

In `internal/api/openai.go`:

1. Add an optional `prompt` field to the multipart request.
2. Thread prompt text from the processor into `Transcribe(...)`.

### Expected result

- Better continuity across clip boundaries
- Lower quality loss from smaller request windows

## Phase 5: Strengthen the Go hypothesis buffer

### Objective

Make commitment and overlap handling behave more like the Python branch.

### Current issue

The current Go `HypothesisBuffer` is too simple and may fail to commit text that Python would commit reliably.

### Planned change

Refactor `internal/processor/hypothesis.go` to track the same categories Python uses:

- committed-in-buffer
- previous uncommitted buffer
- newly received words

### Proposed algorithm

1. Shift new timestamps by `bufferOffset`.
2. Drop words clearly older than `lastCommittedTime`.
3. Remove repeated overlap at the beginning of the new hypothesis using short n-gram matching against recently committed words.
4. Commit the longest common prefix between the prior buffered hypothesis and the new hypothesis.
5. Retain the remainder as the new uncommitted buffer.

### Additional details

- Preserve normalization rules carefully.
- Keep output timestamps monotonic.
- Ensure the logic works correctly when the active audio window starts after a hard clip cut.

### Expected result

- Faster, more stable commitment
- Better trimming opportunities
- Less duplication and fewer edge artifacts

## Phase 6: Add HTTP timeouts and request-scoped observability

### Objective

Improve operational behavior during slow upstream responses and make latency causes easier to diagnose.

### Planned change

In `internal/api/openai.go`:

1. Configure `http.Client` with timeouts, including:
   - total request timeout
   - transport-level timeouts as needed
2. Log request duration and request clip duration.

### Suggested metrics/log fields

For each transcription request, log:

- connection or stream identifier
- audio duration sent upstream
- buffer duration currently retained
- overlap duration
- request start timestamp
- request end timestamp
- total upstream latency
- number of words returned
- whether trimming occurred
- whether a hard cap was applied

### Expected result

- Better debugging for latency regressions
- Safer failure behavior
- Clear evidence that the cap and scheduling changes are working

## Phase 7: Expand tests before and during refactoring

### Objective

Reduce regression risk while porting Python behavior into the Go rewrite.

### Test additions

#### Hypothesis tests

Add tests for:

- repeated overlap words
- longest common prefix commitment
- timestamp shifting by buffer offset
- behavior after clip cuts
- duplicate suppression around overlap

#### Stream processor tests

Add tests for:

- immediate processing once enough audio is buffered
- no extra idle delay after a completed request
- hard cap enforcement on upstream clip duration
- overlap window selection
- trimming at segment boundaries
- fallback trimming when segment boundaries are unavailable

#### API client tests

Add tests for:

- multipart request contains both word and segment timestamp options
- optional prompt field is sent when present
- timeout configuration is applied
- response parsing includes segments

## Phase 8: Rollout sequence

### Recommended order of implementation

1. Add observability first.
2. Refactor scheduling away from ticker-based polling.
3. Add HTTP timeouts.
4. Add segment timestamps to the API client and response parser.
5. Port segment-aware trimming.
6. Add hard `max-clip-length-sec` and `clip-overlap-sec`.
7. Port prompt/context reuse.
8. Upgrade hypothesis matching to better mirror Python.
9. Tune defaults from measurements.

### Why this order

- Scheduling fixes likely give the clearest immediate latency improvement.
- Observability makes later tuning evidence-based.
- Segment trimming and prompt reuse reduce the quality risk of enforcing a hard cap.
- Hypothesis improvements improve commitment behavior and buffer health across all earlier changes.

## Default Values To Start With

Recommended initial defaults:

- `min-chunk-size = 1.0s`
- `buffer-trimming-sec = 15.0s`
- `max-clip-length-sec = 20.0s`
- `clip-overlap-sec = 2.0s`
- upstream request timeout: start with something conservative such as `30s`

These are starting points only and should be validated with real logs after instrumentation lands.

## Risks and Tradeoffs

### Risk: boundary quality degradation

Cause:

- Smaller clips reduce available context

Mitigations:

- overlap window
- prompt reuse
- stronger hypothesis matching
- segment-aware trimming

### Risk: timestamp mistakes after windowing

Cause:

- hard-capped windows and overlap require careful offset calculations

Mitigations:

- explicit tests for offset math
- monotonic timestamp checks
- detailed per-request logging

### Risk: over-trimming

Cause:

- aggressive cuts at unstable points may hurt transcript continuity

Mitigations:

- prefer completed segment boundaries
- fall back to committed-word trimming only when necessary
- preserve overlap

## Expected Outcome

If the recommended changes are implemented, expected improvements are:

1. Shorter perceived pauses in the Go branch
2. Bounded upstream clip duration
3. Lower live transcription latency
4. More predictable upstream cost and performance
5. Better parity between the Go rewrite and the Python implementation
6. A safer foundation for further tuning of quality versus latency
