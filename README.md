# Whisper Streaming Proxy (Go Edition)

A highly optimized, dependency-free middleware proxy written in Go that allows streaming real-time transcription using the OpenAI Whisper API. The go implementation is based on https://github.com/ufal/whisper_streaming

> AI DISCLAIMER 🤖: Almost all source code in this repository has been generated via (back-and-forth) interactions with LLMs (large language models).

This proxy accepts raw streaming audio (`S16_LE`, 16000Hz, mono) over a TCP socket, dynamically packages chunks into WAV format in memory, streams them to an OpenAI-compatible API, performs overlapping hypothesis matching (LocalAgreement) to confirm stable text, and streams timestamped text back to the TCP client.

## Features
- **Zero C-Dependencies:** No need for `librosa`, `libsndfile`, or Python virtual environments.
- **Ultra-Low Memory:** Uses < 15MB of RAM.
- **True Portability:** Compiles to a single static binary.

## Requirements

- **Go:** 1.21+
- **Upstream API:** an OpenAI-compatible `/audio/transcriptions` endpoint
- **Runtime tools for microphone streaming:** `arecord` and `nc`
- **Optional Emacs client:** Emacs with built-in ERT support
- **Full lint/test script:** `golangci-lint` installed at `$(go env GOPATH)/bin/golangci-lint`
- **Benchmark harness:** `python3` and `ffmpeg`

## Build and Run

1. **Set the upstream API environment:**
   ```bash
   export OPENAI_BASE_URL="http://localhost:8007/v1"
   export OPENAI_API_KEY="foobar"
   ```
2. **Build the binary:**
   ```bash
   go build -o bin/whisper-proxy ./cmd/whisper-proxy
   ```
3. **Run the server:**
   ```bash
   ./bin/whisper-proxy --port 43007 --language en
   ```

For live microphone sessions, `--vad rms` is recommended so long silences do
not keep generating unnecessary upstream requests.

## Runtime Flags

These are all command-line flags currently exposed by `whisper-proxy`.

| Flag | Default | What it does | When to change it / tradeoffs |
|------|---------|--------------|--------------------------------|
| `--port` | `43007` | TCP port the proxy listens on for raw PCM audio clients. | Change it when `43007` is already in use, or when you want multiple proxy instances. Low risk; clients must connect to the same port. |
| `--language` | `""` | Passes a fixed language code to the upstream transcription API. Empty means auto-detect. | Set it for single-language dictation to reduce ambiguity and sometimes improve consistency. Leave it empty for mixed-language or unknown input. Tradeoff: forcing the wrong language can hurt recognition badly. |
| `--min-chunk-size` | `1.0` | Minimum amount of newly received audio, in seconds, before the proxy sends another upstream transcription request. | Increase it to reduce request count and cost, and to give the model more context per request. Tradeoff: higher values can delay intermediate updates; lower values feel more live but create more upstream traffic and potentially less stable hypotheses. |
| `--buffer-trimming-sec` | `15.0` | Threshold for trimming old retained audio from the in-memory buffer after safe content has already been committed. | Increase it if you want more historical audio context across long sessions. Tradeoff: more memory retained and larger effective working set; lower values trim more aggressively and can reduce context around boundaries. |
| `--http-timeout-sec` | `30.0` | Timeout for each upstream `/audio/transcriptions` request. | Increase it if your upstream is slow or remote. Lower it if you prefer faster failure on bad network/backend conditions. Tradeoff: too low causes avoidable timeouts; too high makes failure recovery slower. |
| `--max-clip-length-sec` | `20.0` | Hard cap on the duration of audio sent in a single upstream request. If the retained buffer is longer, only the newest window is sent. | Lower it to reduce per-request payload size and latency spikes; raise it to give the model more context. Tradeoff: higher values cost more bandwidth/latency per request, while lower values make overlap/prompt continuity more important. |
| `--clip-overlap-sec` | `2.0` | Overlap between consecutive capped clip windows when `--max-clip-length-sec` is active. | Increase it if you see boundary instability after clipping; decrease it to reduce redundant re-sent audio. Tradeoff: more overlap usually improves continuity but increases repeated context and upstream work. |
| `--max-connections` | `10` | Maximum number of simultaneous client TCP connections the server will accept. Additional clients are rejected. | Raise it for multi-user or multi-client deployments. Tradeoff: higher values allow more concurrency but increase CPU, memory, and upstream API load. |
| `--shutdown-drain-sec` | `5.0` | Grace period during process shutdown to let active client handlers finish draining before the server exits. | Increase it if you want cleaner shutdowns during active sessions; lower it for faster process termination. Tradeoff: longer values are friendlier to live sessions but slow down shutdown/redeploy. |
| `--vad` | `off` | Voice activity detection mode. Supported values are `off` and `rms`. Speech-end events can trigger earlier processing/flush behavior. | `rms` is usually preferable for live microphone use, especially to avoid pointless silence polling. Tradeoff: VAD tuning matters; if the detector is too aggressive it can cut speech into fragments, while `off` is simpler but wastes more requests during silence. |
| `--vad-rms-threshold` | `0.02` | RMS energy threshold used when `--vad rms` is enabled. | Raise it in noisy environments so background noise does not count as speech; lower it for quiet speakers or quiet microphones. Tradeoff: too high misses soft speech, too low mistakes noise for speech. |
| `--vad-min-speech-ms` | `120` | Minimum continuous speech duration before VAD declares “speech started”. | Increase it if short clicks/noise bursts falsely trigger speech; lower it if you want faster detection of brief utterances. Tradeoff: higher values reduce false starts but can clip very short initial speech. |
| `--vad-min-silence-ms` | `400` | Minimum continuous silence duration before VAD declares “speech ended”. | Increase it if natural pauses are being mistaken for end-of-speech; lower it if you want faster flushes at the end of phrases. Tradeoff: lower values feel more responsive but can fragment speech across pauses. |
| `--debug` | `false` | Enables debug-level logging via `slog`. | Turn it on while tuning chunking/VAD behavior or investigating upstream/client issues. Tradeoff: much noisier logs. |

`whisper-proxy` also depends on two environment variables that are not flags:

| Variable | Default | What it does |
|----------|---------|--------------|
| `OPENAI_BASE_URL` | `https://api.openai.com/v1` | Base URL for the upstream OpenAI-compatible API |
| `OPENAI_API_KEY` | unset | Bearer token sent to the upstream API |

Practical starting points:

- Live microphone dictation: `--vad rms` with the defaults is a reasonable baseline.
- If you want fewer upstream requests: try `--min-chunk-size 2.0` together with `--vad rms`.
- If phrase endings flush too slowly: lower `--vad-min-silence-ms`.
- If background noise causes activity while nobody is speaking: raise `--vad-rms-threshold` and possibly `--vad-min-speech-ms`.

## Usage

Once the server is running, you can stream audio to it using `arecord` and `nc` (Netcat):

```bash
arecord -f S16_LE -c1 -r 16000 -t raw -D default | nc localhost 43007
```

You will see timestamped transcripts streamed back to your terminal in real-time:
```text
0 1200 Hello
1200 1850 world
```

### From emacs

The Emacs client lives in [emacs-client/](emacs-client).

- Load `emacs-client/strisper.el`
- Use `M-x strisper-record` to start recording without insertion
- Use `M-x strisper-record-at-point` to insert transcribed text at point
- Use `M-x strisper-stop` to stop the active recording process
- Customize `strisper-record-command` if your audio device, host, or port differ from the defaults

By default the command connects to `localhost:43007`, or `host.docker.internal`
when Emacs runs inside a Docker/Podman container.

## Development

Common entry points:

```bash
go test -v -race ./...
bash scripts/_80-test_emacs-elisp.sh
bash scripts/build-test-lint-all.sh
```

The last script matches the CI entry point and runs build, Go tests, lint, and
the Emacs ERT suite.

## Benchmarking and Reports

- Benchmark harness overview: [scripts/benchmark/README.md](scripts/benchmark/README.md)
- UX regression report and follow-up analysis: [design-docs-wip/03-REPORT.md](design-docs-wip/03-REPORT.md)
- Test data notes: [tests/README.md](tests/README.md)
