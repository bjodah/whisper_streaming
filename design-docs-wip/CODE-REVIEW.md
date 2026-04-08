• 1. run-proxy.sh does not reliably leave the proxy running after the script exits. It backgrounds the child process and then returns without
     nohup/setsid/disown, so direct use can report success even though the proxy is already gone. I reproduced this with the Go proxy: run-proxy.sh
     printed a PID, but run-session.sh immediately hit ConnectionRefusedError, and the PID no longer existed. See scripts/benchmark/run-
     proxy.sh:88, scripts/benchmark/run-proxy.sh:108, scripts/benchmark/run-proxy.sh:123.
  2. The Python launcher ignores the requested port. run-proxy.sh -i python -p 43011 still starts whisper_online_server.py without --port, so it
     binds the Python default 43007. I confirmed this by starting the Go proxy on 43007 and then running the Python launcher with -p 43011; it
     failed with OSError: [Errno 98] Address already in use. See scripts/benchmark/run-proxy.sh:97.
  3. run-all.sh fails after a successful benchmark because it parses the run directory incorrectly. run-session.sh prints === Session run
     complete: /path ===, but the extraction leaves the trailing ===, so the directory existence check fails. I reproduced this: the session
     completed and artifacts were written, then run-all.sh exited with ERROR: Could not determine run directory from session output. See scripts/
     benchmark/run-all.sh:117.
  4. proxy-meta.json becomes invalid JSON when debug is enabled. ${debug:+true}${debug:-false} expands to trueyes when debug="yes". run-all.sh -P
     always passes -d, so the default metadata path is malformed in the common path. I confirmed this in /work/tests/benchmark/runs/review-go-
     proxy-debug/proxy-meta.json. See scripts/benchmark/run-proxy.sh:63.
  5. The duplicate-output metric misses an actual repeated-emission regression. detect_duplicates() only counts immediately repeated individual
     words, not repeated segments or repeated phrases. In my Python-proxy run, the final hypothesis was hello world repeated five times, but
     duplicate_word_count still came out 0. That makes the score report blind to one of the most relevant streaming failure modes. See scripts/
     benchmark/helpers/score_run.py:102, tests/benchmark/runs/review-python-run/events.jsonl, tests/benchmark/runs/review-python-run/summary.json.
  6. arrival_wall_time is malformed. The formatter can produce values like 2026-04-08T12:04:140.674Z, which is not a valid timestamp. That makes
     the machine-readable event log less trustworthy than it looks. See scripts/benchmark/helpers/session_client.py:66, tests/benchmark/runs/
     review-python-run/events.jsonl.

  Assumptions

  - I used a local mock OpenAI-compatible server at http://127.0.0.1:8007/v1 returning a fixed hello world transcript with word and segment timest
    amps. That kept the comparison deterministic and avoided external API cost.
  - Because of that mock, the WER numbers are not meaningful for model quality; the useful part of the comparison is transport/orchestration
    behavior and output shape.
  - I had to start the proxies manually for the cross-impl run because the delivered launcher/orchestrator scripts are not reliable enough yet.

  Comparison
  I exercised the delivered harness against both implementations with the same 1-clip session.

  Go proxy in /work:

  - run-session.sh succeeded against a manually started Go proxy.
  - It emitted 2 committed word events, first output at about 1973 ms.
  - Artifacts: tests/benchmark/runs/review-go-run/events.txt, tests/benchmark/runs/review-go-run/summary.json.

  Old Python proxy in /work-old-python-impl:

  - run-session.sh also succeeded when I started whisper_online_server.py manually on port 43008.
  - It emitted 5 repeated hello world segment events, first output at about 7373 ms.
  - Artifacts: tests/benchmark/runs/review-python-run/events.txt, tests/benchmark/runs/review-python-run/summary.json.

  What that comparison says:

  - The transport helper and session builder basically work.
  - The harness can drive both implementations.
  - The current scoring misses repeated-output regressions.
  - The wrapper scripts are not yet trustworthy enough for hands-off comparison.

  Missed From The Plan

  - The scoring path still does not consume proxy logs, so request count, request latency, trim frequency, and average upstream chunk size are
    missing even though the Go proxy logs expose them. See scripts/benchmark/score-run.sh:65, scripts/benchmark/helpers/score_run.py:222.
  - The orchestration relies on parsing human-readable stdout instead of passing explicit output paths or writing a machine-readable handoff file.
    That is why run-all.sh broke.

  Overall
  The consultant got the core shape mostly right: concat-session.sh, session_client.py, and the basic scoring loop are serviceable. The main probl
  ems are in orchestration and in the quality of the scoring metrics. As delivered, I would not trust run-proxy.sh/run-all.sh for unattended compa
  risons, and I would not trust duplicate_word_count to catch repeated-output failures.

  1. Fix run-proxy.sh and run-all.sh first: daemonization, Python port forwarding, and path handoff.
  2. Fix the scoring model next: detect repeated phrases/segments, not just adjacent identical tokens.
  3. Add log parsing so the harness actually reports request count, upstream latency, and trim behavior.
