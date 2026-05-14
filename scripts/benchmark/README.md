
# Benchmarking Scripts

| Script | Role |
| :--- | :--- |
| `concat-session.sh` | Concatenates WAV clips to create session WAV, manifest, merged reference text, and timings. |
| `run-session.sh` | Runs a single session against the proxy (wraps a Python full-duplex TCP client). |
| `score-run.sh` | Computes WER, latency, monotonicity, and coarse timing error. |
| `run-proxy.sh` | Launches Go or Python proxy with benchmark-friendly configuration. |
| `run-all.sh` | One-command orchestration: build → run → score → report. |

## Python Helpers

Located in `scripts/benchmark/helpers/`:

- **build_manifest.py**: Generates manifest and reference files.
- **session_client.py**: Full-duplex TCP transport with half-close support.
- **score_run.py**: Calculates WER (edit distance), latency metrics, and timing analysis.

## Quick Start

Starts proxy, builds a 5-clip session, runs the test, and scores the results:

```bash
./scripts/benchmark/run-all.sh -P -c 5
```

