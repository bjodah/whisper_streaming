As a consulting software engineer reviewing your architecture and implementation plan, I find `01-IMPL-PLAN.md` to be an exceptionally well-thought-out and accurate diagnosis of the discrepancies between the Python prototype and the Go rewrite. 

You have correctly identified the core issues causing latency and context degradation in the Go version. I **strongly concur** with your 8-phase plan. Phasing out the ticker-based scheduling, restoring prompt reuse, and strictly mirroring Python's `LocalAgreement` (n-gram overlap) logic will bridge the gap in transcription quality and latency.

However, drawing from experience with Go network services and audio processing, there are a few critical architectural aspects overlooked in the plan. Furthermore, I have some specific thoughts on your question regarding direct microphone recording.

Here is my second opinion.

---

### Part 1: What was overlooked in `01-IMPL-PLAN.md`

#### 1. Context Cancellation and Goroutine Leaks (Critical)
Currently, your Go server spawns a `readLoop` goroutine and a `Run` processing loop. The `01-IMPL-PLAN` adds HTTP timeouts (Phase 6), which is good, but it misses **lifecycle management**.
*   **The Issue:** If the client disconnects (e.g., the user kills `netcat`), `readLoop` errors out and closes `stopChan`. However, if the processor is currently blocked waiting for a 2-minute upstream API call to finish, it won't see `stopChan` close until the API responds. 
*   **The Fix:** You must introduce `context.Context`. The upstream OpenAI HTTP request should be created using `http.NewRequestWithContext(ctx, ...)`. When the TCP socket closes, the context should cancel, immediately aborting the in-flight HTTP request. Without this, your proxy will leak goroutines and API budget on abandoned connections.

#### 2. Memory Leak in Slice Reslicing (Technical Go Nuance)
In `internal/processor/stream.go`, `trimBuffer()` does this: `s.audioBuf = s.audioBuf[cutBytes:]`.
*   **The Issue:** In Go, slicing an existing slice keeps the underlying backing array in memory. If a user streams audio for 3 hours, the underlying array will grow to hold 3 hours of PCM data (approx. 345 MB), even though `len(s.audioBuf)` only reports the last 20 seconds.
*   **The Fix:** When trimming the buffer, allocate a new slice and copy the retained data over if the capacity grows too large, or implement a proper ring buffer.
  ```go
  // Prevent memory leak by copying to a fresh slice
  retained := make([]byte, len(s.audioBuf)-cutBytes)
  copy(retained, s.audioBuf[cutBytes:])
  s.audioBuf = retained
  ```

#### 3. Voice Activity Detection (VAD) Discrepancy
The Python codebase relies heavily on the `silero-vad` model (via `VACOnlineASRProcessor`). It detects silence and immediately triggers chunk processing/flushing. 
*   **The Issue:** Your Go plan does not mention VAD. Without VAD, if the user stops speaking, the Go proxy will just buffer the silence until it hits a time limit, send the silence to OpenAI, pay for that transcription time, and likely get hallucinated text back (a known Whisper quirk on silent audio).
*   **The Fix:** While you probably don't want to embed a neural network VAD in your dependency-free Go binary, you should consider implementing a simple energy-based RMS (Root Mean Square) silence detection gate. If the audio energy drops below a threshold for >1 second, trigger an immediate flush rather than waiting for `max-clip-length`.

---

### Part 2: Feasibility and Benefit of Direct Microphone Recording

You asked if it would be feasible and beneficial to add a mode to record directly from the microphone, eliminating the `arecord | nc` pipeline.

#### Feasibility: High, but with a major catch
Yes, it is entirely feasible. You would typically use a library like `github.com/gordonklaus/portaudio` or `github.com/gen2brain/malgo` (miniaudio). 

**The Catch:** Your `README.md` proudly states:
> **Zero C-Dependencies:** ... True Portability: Compiles to a single static binary.

Accessing audio hardware cross-platform natively requires interfacing with ALSA/PulseAudio (Linux), CoreAudio (macOS), or WASAPI (Windows). Almost all Go audio capture libraries require **CGO** (C-bindings). If you enable CGO, you lose the ability to easily cross-compile a static binary (e.g., compiling for macOS from a Linux machine becomes a massive headache). 

#### Benefits:
*   **User Experience:** Phenomenal improvement. Users just run `./whisper-proxy --record` instead of dealing with pipe setups, sample rates (`-r 16000`), and endianness (`S16_LE`) parameters which often frustrate end-users.
*   **Platform Agnostic:** `arecord` is Linux specific. macOS users have to install `sox` and use `rec`. Windows users are largely left out. A built-in microphone mode makes your tool truly cross-platform.

#### Recommendation: The "Sidecar" or "Build Tag" approach
I recommend **against** making microphone capture a core dependency of your proxy server, as it breaks the elegant, dependency-free network-shuffler architecture you've built. 

Instead, I recommend one of two paths:

1.  **Build Tags (Conditional Compilation):**
    Create a separate package in your Go code (e.g., `cmd/whisper-client`) that uses `malgo` to record audio and sends it over TCP to your proxy. Use Go build tags (`//go:build cgo`) so the server remains pure Go, but users can optionally build the client if they want local mic support.

2.  **Keep the Unix Philosophy:**
    Leave the proxy exactly as it is (a TCP in, Text out server). Document how to use it on macOS (`rec -b 16 -r 16000 -c 1 -e signed -t raw - | nc...`) and Windows (`ffmpeg -f dshow ...`). This maintains your zero-dependency guarantee while focusing purely on the complex ASR overlapping-hypothesis logic.

### Summary
Your `01-IMPL-PLAN.md` is excellent and ready to execute. I advise adding **Context cancellation**, **memory-leak safe slice trimming**, and a **rudimentary RMS silence detector** to your plan. Keep the proxy as a pure network service, as adding direct mic support will force you to adopt CGO, complicating your currently beautiful zero-dependency build process.
