# Third Opinion: Streaming Transcription Proxy Implementation Plan Review

## Executive Summary

The implementation plan (`01-IMPL-PLAN.md`) is thorough, well-structured, and correctly identifies the core issues. The second opinion (`02-SECOND-OPINION.md`) raises valid Go-specific concerns. Below I offer my own assessment after reading both documents and auditing the full codebase across both branches.

---

## Part 1: Assessment of the Implementation Plan

### What the plan gets right

The diagnosis is excellent. The six findings are all accurate and well-evidenced by the code:

1. **Ticker-based scheduling delay** — Confirmed. `stream.go:53` creates a fixed ticker and the select loop on line 63 means after every completed API call, the processor idles until the next tick. This is the single biggest latency contributor in the Go rewrite and the easiest win.

2. **No hard max clip length** — Confirmed. `trimBuffer()` (line 119) only trims when `audioSec > s.trimSec` and only up to `lastCommittedTime`. If commitment stalls, the buffer grows unbounded.

3. **Missing segment-aware trimming** — Confirmed. The API client only requests `timestamp_granularities[] = word` (line 59 of `openai.go`). No segment data is available for trimming decisions.

4. **Missing prompt reuse** — Confirmed. The `Transcribe()` method has no prompt parameter. Python constructs a 200-char trailing context prompt from committed text that has scrolled out of the audio window.

5. **Simpler hypothesis logic** — Confirmed and significant. Python's `HypothesisBuffer` maintains three distinct lists (`commited_in_buffer`, `buffer`, `new`) and performs n-gram overlap removal before computing the longest common prefix. Go's version only has `prevWords`, does a straight prefix comparison, and completely lacks the overlap deduplication step.

6. **No HTTP timeout** — Confirmed. Line 37 of `openai.go`: `HTTP: &http.Client{}` with no timeout configuration.

### What I would do differently

#### 1. Merge Phases 1 and 6 — scheduling fix + observability should ship together

The plan's Phase 8 (rollout sequence) says "add observability first," but the detailed phases put the scheduling fix as Phase 1 and observability as Phase 6. I would combine them. The scheduling fix is the highest-impact, lowest-risk change. Shipping it with basic request-duration logging gives you immediate feedback on the improvement without delaying the most impactful fix.

**Recommendation:** Implement the threshold-driven processing loop and add per-request structured logging (audio duration sent, upstream latency, buffer size) in a single phase. HTTP timeouts can ride along trivially.

#### 2. Phase 2 (max clip length) needs more design specificity on the windowing model

The plan says "send only the newest `max-clip-length-sec` window" and "include up to `clip-overlap-sec` of prior overlap." But it doesn't specify **how the buffer offset interacts with this windowing**.

Currently, `processChunk` sends the entire `audioBuf` snapshot and `bufferOffset` tracks how many seconds have been trimmed from the front. If you introduce a clip-length cap, you now have two independent truncation points:

- The **front of the buffer** (controlled by trimming / `bufferOffset`)
- The **start of the send window** (controlled by max clip length)

These need to be carefully distinguished. The offset passed to hypothesis processing must reflect the absolute time of the *sent* window's start, not just the buffer's start. The plan acknowledges this ("compute the correct transcription time offset") but doesn't spell out the data model. I would add a `sendWindowOffset` field distinct from `bufferOffset` to keep the math clean.

#### 3. Phase 5 (hypothesis buffer) should come before Phase 2 (max clip length), not after

The plan's Phase 8 ordering places hypothesis improvements last. I disagree. The hypothesis buffer is the mechanism that drives commitment, and commitment is what enables trimming. If you cap clip length (Phase 2) before fixing the hypothesis buffer (Phase 5), you'll trigger the cap more often because commitment is still stalling. This increases boundary artifacts before you have the tooling (prompt reuse, better overlap handling) to mitigate them.

**Recommended implementation order:**
1. Scheduling fix + observability + HTTP timeouts (Phases 1, 6 merged)
2. Segment timestamps in API client (Phase 3, data model only)
3. Hypothesis buffer improvements (Phase 5)
4. Segment-aware trimming (Phase 3, processor logic)
5. Prompt/context reuse (Phase 4)
6. Hard max clip length with overlap (Phase 2)

This way, each subsequent change benefits from the improvements that precede it, and the hard cap is the final safety net rather than a crutch compensating for weak commitment logic.

#### 4. The plan underspecifies the snapshot-copy overhead

Every processing cycle, `stream.go:69-70` copies the entire audio buffer:

```go
snapshot := make([]byte, currentLen)
copy(snapshot, s.audioBuf)
```

At 32,000 bytes/sec and a 15-second buffer, that's ~480KB copied per request. After Phase 2's max clip cap, it's bounded at ~640KB (20s). This isn't catastrophic, but it's wasteful and will show up in GC pressure under load with multiple concurrent streams.

**Recommendation:** Consider a read-only view pattern or a ring buffer. At minimum, after the clip cap is in place, only copy the window being sent rather than the full buffer.

#### 5. The plan doesn't address the `ToWAV` double-copy

`audio.ToWAV()` uses `append(header, pcm...)` which allocates a new slice and copies the entire PCM payload again. Combined with the snapshot copy, every processing cycle copies the audio data twice. A pre-allocated buffer with the header written in place would eliminate one copy.

#### 6. Missing: error recovery / retry strategy

The plan adds HTTP timeouts (good) but doesn't address what happens after a timeout or transient API error. Currently (`stream.go:111`), errors are logged and silently dropped — the processing cycle is skipped. This means:

- The audio that was being transcribed remains in the buffer
- On the next cycle, an even longer clip is sent (because more audio accumulated during the failed attempt)
- This creates a positive feedback loop: timeout → longer clip → higher chance of timeout

**Recommendation:** After a failed API call, cap the retry window to prevent the next attempt from being even longer. Consider a simple exponential backoff with a "shed the oldest audio" strategy if errors persist.

### What was missed entirely

#### 1. `segments_end_ts` mismatch in Python's OpenAI backend

This is a subtle but important finding. The plan recommends restoring segment-aware trimming to match Python. However, examining the Python `OpenaiApiASR` class (the backend relevant to this comparison), `segments_end_ts()` returns:

```python
def segments_end_ts(self, res):
    return [s.end for s in res.words]  # NOTE: iterates .words, not .segments
```

This returns **word** end timestamps, not segment end timestamps. The other Python backends (`FasterWhisperASR`, `WhisperTimestampedASR`) return actual segment boundaries, but the OpenAI API backend being used here doesn't. This means the Python branch's `chunk_completed_segment()` is effectively chunking at **word** boundaries when using the OpenAI API, not at semantic segment boundaries.

**Implication:** The segment-aware trimming in Phase 3 may be targeting behavior that the Python branch doesn't actually exhibit with the OpenAI API backend. You should verify which backend was used in the comparison test. If it was `openai-api`, the Go rewrite should match the word-level chunking behavior, and requesting segment timestamps from the API is a nice-to-have improvement *beyond* Python parity, not a restoration of parity.

#### 2. No graceful shutdown or signal handling

The server's `Listen()` method loops forever with no way to shut down cleanly. There's no `os.Signal` handling, no `context.Context` threading through the accept loop, no drain-then-close behavior. This matters for deployment: a SIGTERM during an active transcription will abruptly kill the connection without flushing remaining output.

#### 3. No connection limiting

The Go server spawns a goroutine per connection with no concurrency limit. Each connection holds an audio buffer and makes API calls. Under load, this means unbounded memory growth and unbounded concurrent upstream API calls. The Python server, by contrast, only serves one client at a time.

**Recommendation:** Add a semaphore or connection limit (configurable, default to something reasonable like 10).

---

## Part 2: Assessment of the Second Opinion

### Points I agree with

#### Context cancellation and goroutine leaks — Strongly agree

This is the most important point in the second opinion. The Go code has zero `context.Context` usage. If a client disconnects during a multi-second API call, the processing goroutine blocks until the API responds, then discovers the connection is closed when it tries to write. During that time, it holds memory, an HTTP connection to the upstream API, and potentially delays any connection-scoped cleanup.

The fix is straightforward: thread `context.Context` from the TCP connection through `StreamProcessor.Run()` into `api.Client.Transcribe()` using `http.NewRequestWithContext()`. Cancel the context when the read loop detects EOF/error.

#### Memory leak in slice reslicing — Agree in principle

The consultant correctly identifies that `s.audioBuf = s.audioBuf[cutBytes:]` retains the original backing array. For a 3-hour stream that's ~345MB held despite only needing ~640KB (20s of audio after clip cap).

However, the severity is somewhat mitigated by the upcoming max clip length cap — once in place, trimming happens frequently enough that the backing array won't grow as dramatically. Regardless, the fix is trivial (copy to a fresh slice when trimming) and should be included. It's cheap insurance.

#### VAD / silence detection — Agree directionally, disagree on priority

The consultant is right that Whisper hallucinates on silence and that sending silent audio to the API wastes budget. However:

1. The Python `VACOnlineASRProcessor` is an **optional wrapper** enabled with `--vac`. The base `OnlineASRProcessor` doesn't use VAD either.
2. A simple RMS energy gate is a reasonable middle ground, but it's a **new feature**, not a "missing" parity item.
3. It adds complexity to the processing loop (flush timing, VAD state management, threshold tuning).

**My recommendation:** Add it to the roadmap but not to the current plan. The current plan is already substantial (8 phases). RMS silence gating should be Phase 9 or a separate work item, pursued after the core commitment/trimming/scheduling improvements are validated.

### Points I'd nuance

#### Microphone recording (build tags / sidecar) — Good advice, but overthinking it

The consultant's analysis of CGO implications is correct and the build-tag recommendation is sound. But I'd go further: this is not a feature you should pursue now at all. The proxy's value proposition is as a network service. Clients can be implemented in any language. Spending engineering effort on audio capture detracts from solving the actual transcription quality and latency issues described in the plan. Revisit only if user demand justifies it.

---

## Part 3: Additional Recommendations

### 1. Add structured logging with connection IDs immediately

Every log line from the processor should include a connection identifier. Currently, concurrent connections produce interleaved logs with no way to correlate. This is a prerequisite for any meaningful production debugging.

### 2. Consider using `io.Pipe` or channel-based audio delivery instead of mutex-guarded buffer

The current design has the read loop and processing loop contending on `s.mu` for every read and every process cycle. A channel-based or pipe-based approach would be cleaner:

- Read loop writes chunks into a channel
- Processing loop drains the channel into its local buffer

This eliminates the mutex, makes the threshold-driven wake-up natural (channel receive), and separates the I/O concerns cleanly. It also aligns well with the plan's Phase 1 goal of replacing the ticker with threshold-driven processing.

### 3. Add integration test infrastructure

The plan's Phase 7 describes unit tests, which are necessary. But the most valuable test for this system is an integration test that:

- Streams known audio (a WAV file converted to raw PCM, fed at real-time speed)
- Runs the proxy against a mock upstream API that returns deterministic responses
- Asserts on output timing and content

This would catch scheduling regressions, timestamp math errors, and trim behavior issues that are hard to unit test in isolation.

### 4. Pin the upstream request timeout to be proportional to clip duration

A flat 30-second timeout is a reasonable starting point, but once `max-clip-length-sec` is configurable, the timeout should scale with it. A 5-second clip shouldn't wait 30 seconds for a response. Consider: `timeout = max(10s, clip_duration * 2)` or similar.

---

## Summary Table

| Topic | Impl Plan | Second Opinion | My Assessment |
|-------|-----------|---------------|---------------|
| Ticker scheduling fix | ✅ Correct, Phase 1 | — | ✅ Agree, highest priority |
| Max clip length | ✅ Correct, Phase 2 | — | ✅ Agree, but implement last after hypothesis/prompt fixes |
| Segment-aware trimming | ✅ Correct, Phase 3 | — | ⚠️ Verify Python actually uses segment boundaries with OpenAI API backend (it may not) |
| Prompt reuse | ✅ Correct, Phase 4 | — | ✅ Agree |
| Hypothesis buffer | ✅ Correct, Phase 5 | — | ✅ Agree, but elevate priority — implement before clip cap |
| HTTP timeouts | ✅ Correct, Phase 6 | — | ✅ Agree, merge with Phase 1 |
| Context cancellation | ❌ Missing | ✅ Critical | ✅ Strongly agree, add to Phase 1 |
| Memory leak (slice) | ❌ Missing | ✅ Valid | ✅ Agree, trivial fix, include in trimming refactor |
| VAD / silence detection | ❌ Not in scope | ✅ Recommended | ⚠️ Agree it's valuable, but defer to a later phase |
| Microphone recording | ❌ Not in scope | ✅ Build-tag approach | 🔽 Deprioritize entirely, solve proxy quality issues first |
| Error recovery / retry | ❌ Missing | ❌ Missing | ✅ Add — failed API calls create a clip-length positive feedback loop |
| Connection limiting | ❌ Missing | ❌ Missing | ✅ Add — unbounded goroutines per connection |
| Graceful shutdown | ❌ Missing | ❌ Missing | ✅ Add — signal handling for clean drain |
| Snapshot copy overhead | ❌ Missing | ❌ Missing | ⚠️ Minor, but worth addressing alongside ring buffer |
| `segments_end_ts` bug | ❌ Missing | ❌ Missing | ⚠️ Python OpenAI backend returns word timestamps, not segments — verify test conditions |
