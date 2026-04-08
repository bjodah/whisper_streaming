# Benchmarking scripts

  ┌─────────────────────┬───────────────────────────────────────────────────────────────────────────────────┐
  │ Script              │ Role                                                                              │
  ├─────────────────────┼───────────────────────────────────────────────────────────────────────────────────┤
  │ concat-session.sh   │ Concatenates WAV clips → session WAV + manifest + merged reference text & timings │
  ├─────────────────────┼───────────────────────────────────────────────────────────────────────────────────┤
  │ run-session.sh      │ Runs one session against proxy (wraps Python full-duplex TCP client)              │
  ├─────────────────────┼───────────────────────────────────────────────────────────────────────────────────┤
  │ score-run.sh        │ Computes WER, latency, monotonicity, coarse timing error                          │
  ├─────────────────────┼───────────────────────────────────────────────────────────────────────────────────┤
  │ run-proxy.sh        │ Launches Go or Python proxy with benchmark-friendly config                        │
  ├─────────────────────┼───────────────────────────────────────────────────────────────────────────────────┤
  │ run-all.sh          │ One-command orchestration: build → run → score → report                           │
  └─────────────────────┴───────────────────────────────────────────────────────────────────────────────────┘

  Python helpers (scripts/benchmark/helpers/):

   - build_manifest.py — manifest/reference generation
   - session_client.py — full-duplex TCP transport with half-close
   - score_run.py — WER (edit distance), latency metrics, timing analysis

  Quick start: ./scripts/benchmark/run-all.sh -P -c 5 (starts proxy, builds 5-clip session, runs, scores).
