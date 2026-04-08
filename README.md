# Whisper Streaming Proxy (Go Edition)

A highly optimized, dependency-free middleware proxy written in Go that allows streaming real-time transcription using the OpenAI Whisper API.

This proxy accepts raw streaming audio (`S16_LE`, 16000Hz, mono) over a TCP socket, dynamically packages chunks into WAV format in memory, streams them to an OpenAI-compatible API, performs overlapping hypothesis matching (LocalAgreement) to confirm stable text, and streams timestamped text back to the TCP client.

## Features
- **Zero C-Dependencies:** No need for `librosa`, `libsndfile`, or Python virtual environments.
- **Ultra-Low Memory:** Uses < 15MB of RAM.
- **True Portability:** Compiles to a single static binary.

## Build and Run

1. **Ensure you have Go 1.21+ installed.**
2. **Build the binary:**
   ```bash
   go build -o bin/whisper-proxy ./cmd/whisper-proxy
   ```
3. **Run the server:**
   ```bash
   export OPENAI_BASE_URL="http://localhost:8007/v1"
   export OPENAI_API_KEY="foobar"
   ./bin/whisper-proxy --port 43007 --language en
   ```

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
See "strisper" in [emacs-client/](emacs-client).
