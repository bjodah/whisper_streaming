"""Regression tests for the benchmark scoring module.

Tests:
  - Repeated exact event lines are detected
  - Repeated short phrases (2-grams) with different timestamps are detected
  - Normalized event-text repetition is detected
  - Missing proxy log does not crash
  - Malformed proxy log is handled gracefully
  - Proxy log metrics are parsed correctly
"""

import json
import os
import sys
import tempfile

# Ensure the helpers module is importable
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "..", "scripts", "benchmark", "helpers"))

from score_run import (
    compute_wer,
    detect_duplicates,
    check_monotonicity,
    compute_latency_metrics,
    parse_proxy_log,
    tokenize,
    normalize_text,
)

PASS = 0
FAIL = 0


def check(name, condition, detail=""):
    global PASS, FAIL
    if condition:
        PASS += 1
        print(f"  PASS: {name}")
    else:
        FAIL += 1
        print(f"  FAIL: {name} — {detail}")


def make_event(text, start_ms=0, end_ms=1000, arrival_ms=0, raw_line=None):
    return {
        "event_index": 0,
        "arrival_monotonic_ms": arrival_ms,
        "arrival_wall_time": "2024-01-01T00:00:00.000Z",
        "start_ms": start_ms,
        "end_ms": end_ms,
        "text": text,
        "raw_line": raw_line or f"{start_ms} {end_ms} {text}",
    }


# ─── Test: Repeated exact event lines ───

def test_repeated_exact_lines():
    print("\n--- Repeated Exact Lines ---")
    events = [
        make_event("hello world", 0, 1000, 100, raw_line="0 1000 hello world"),
        make_event("hello world", 0, 1000, 200, raw_line="0 1000 hello world"),
        make_event("hello world", 0, 1000, 300, raw_line="0 1000 hello world"),
        make_event("goodbye", 1000, 2000, 400, raw_line="1000 2000 goodbye"),
    ]
    result = detect_duplicates(events)
    check("repeated_event_lines >= 2", result["repeated_event_lines"] >= 2,
          f"got {result['repeated_event_lines']}")
    check("repeated_normalized_events >= 2", result["repeated_normalized_events"] >= 2,
          f"got {result['repeated_normalized_events']}")
    check("top_repeated is hello world", "hello world" in result["top_repeated_event_text"],
          f"got '{result['top_repeated_event_text']}'")
    check("top_repeated_count == 3", result["top_repeated_event_count"] == 3,
          f"got {result['top_repeated_event_count']}")


# ─── Test: Repeated short phrases (2-gram) ───

def test_repeated_short_phrases():
    print("\n--- Repeated Short Phrases (2-gram) ---")
    events = [
        make_event("hello world", 0, 500, 100),
        make_event("this is fine", 500, 1000, 200),
        make_event("hello world", 1000, 1500, 300),
        make_event("the end", 1500, 2000, 400),
    ]
    result = detect_duplicates(events)
    check("repeated_short_phrase_words > 0", result["repeated_short_phrase_words"] > 0,
          f"got {result['repeated_short_phrase_words']}")
    check("repeated_phrase_words > 0", result["repeated_phrase_words"] > 0,
          f"got {result['repeated_phrase_words']}")


# ─── Test: Repeated short phrases with different timestamps ───

def test_repeated_short_different_timestamps():
    print("\n--- Repeated Short Phrases, Different Timestamps ---")
    events = [
        make_event("good morning", 0, 500, 100),
        make_event("how are you", 500, 1000, 200),
        make_event("good morning", 2000, 2500, 300),
        make_event("have a nice day", 2500, 3000, 400),
    ]
    result = detect_duplicates(events)
    check("repeated_normalized_events >= 1", result["repeated_normalized_events"] >= 1,
          f"got {result['repeated_normalized_events']}")
    check("repeated_short_phrase_words >= 2", result["repeated_short_phrase_words"] >= 2,
          f"got {result['repeated_short_phrase_words']}")


# ─── Test: No false positives on unique content ───

def test_no_false_positives():
    print("\n--- No False Positives ---")
    events = [
        make_event("the quick brown fox", 0, 1000, 100),
        make_event("jumps over the lazy dog", 1000, 2000, 200),
        make_event("and runs away", 2000, 3000, 300),
    ]
    result = detect_duplicates(events)
    check("repeated_event_lines == 0", result["repeated_event_lines"] == 0,
          f"got {result['repeated_event_lines']}")
    check("repeated_normalized_events == 0", result["repeated_normalized_events"] == 0,
          f"got {result['repeated_normalized_events']}")
    check("repeated_short_phrase_words == 0", result["repeated_short_phrase_words"] == 0,
          f"got {result['repeated_short_phrase_words']}")


# ─── Test: Missing proxy log ───

def test_missing_proxy_log():
    print("\n--- Missing Proxy Log ---")
    result = parse_proxy_log("/nonexistent/path/proxy-stdout.log")
    check("returns None for missing file", result is None, f"got {result}")


# ─── Test: Malformed proxy log ───

def test_malformed_proxy_log():
    print("\n--- Malformed Proxy Log ---")
    with tempfile.NamedTemporaryFile(mode="w", suffix=".log", delete=False) as f:
        f.write("this is garbage\n")
        f.write("transcribe request but no valid fields\n")
        f.write("transcribe request latency_ms=abc status=nope\n")
        path = f.name
    try:
        result = parse_proxy_log(path)
        check("does not crash", True)
        if result is not None:
            check("has error or zero requests",
                  result.get("error") or result.get("request_count", 0) == 0,
                  f"got {result}")
        else:
            check("returns None for unrecognized format", True)
    finally:
        os.unlink(path)


# ─── Test: Valid proxy log parsing ───

def test_valid_proxy_log():
    print("\n--- Valid Proxy Log ---")
    with tempfile.NamedTemporaryFile(mode="w", suffix=".log", delete=False) as f:
        f.write('time=2024-01-01T00:00:00Z level=INFO msg="transcribe request" latency_ms=150 status=success sent_sec=1.5 word_count=10 trimmed=false\n')
        f.write('time=2024-01-01T00:00:01Z level=INFO msg="transcribe request" latency_ms=200 status=success sent_sec=2.0 word_count=15 trimmed=true\n')
        f.write('time=2024-01-01T00:00:02Z level=INFO msg="transcribe request" latency_ms=100 status=error sent_sec=0.5 word_count=0 trimmed=false\n')
        path = f.name
    try:
        result = parse_proxy_log(path)
        check("result is not None", result is not None)
        check("request_count == 3", result.get("request_count") == 3,
              f"got {result.get('request_count')}")
        check("success_count == 2", result.get("success_count") == 2,
              f"got {result.get('success_count')}")
        check("error_count == 1", result.get("error_count") == 1,
              f"got {result.get('error_count')}")
        check("trim_count == 1", result.get("trim_count") == 1,
              f"got {result.get('trim_count')}")
        check("mean_upstream_latency_ms is reasonable",
              130 <= result.get("mean_upstream_latency_ms", 0) <= 200,
              f"got {result.get('mean_upstream_latency_ms')}")
    finally:
        os.unlink(path)


# ─── Test: WER computation ───

def test_wer():
    print("\n--- WER Computation ---")
    ref = tokenize("the quick brown fox jumps over the lazy dog")
    hyp = tokenize("the quick brown fox jumps over the lazy dog")
    result = compute_wer(ref, hyp)
    check("perfect match WER == 0", result["wer"] == 0.0, f"got {result['wer']}")

    hyp2 = tokenize("a quick brown cat jumps over the lazy dog")
    result2 = compute_wer(ref, hyp2)
    check("WER > 0 for mismatches", result2["wer"] > 0, f"got {result2['wer']}")
    check("substitutions >= 1", result2["substitutions"] >= 1,
          f"got {result2['substitutions']}")


# ─── Test: Monotonicity ───

def test_monotonicity():
    print("\n--- Monotonicity ---")
    events = [
        make_event("a", 0, 500),
        make_event("b", 500, 1000),
        make_event("c", 300, 800),  # violation: start < previous start
    ]
    result = check_monotonicity(events)
    check("violations >= 1", result["monotonicity_violations"] >= 1,
          f"got {result['monotonicity_violations']}")


# ─── Test: Latency metrics ───

def test_latency_metrics():
    print("\n--- Latency Metrics ---")
    events = [
        make_event("", arrival_ms=100),
        make_event("hello", arrival_ms=500),
        make_event("world", arrival_ms=900),
    ]
    result = compute_latency_metrics(events, 5.0)
    check("first_event_ms == 100", result["time_to_first_event_ms"] == 100,
          f"got {result['time_to_first_event_ms']}")
    check("first_word_ms == 500", result["time_to_first_word_ms"] == 500,
          f"got {result['time_to_first_word_ms']}")
    check("mean_inter_event_ms is 400", result["mean_inter_event_ms"] == 400.0,
          f"got {result['mean_inter_event_ms']}")

    result_empty = compute_latency_metrics([], None)
    check("empty events returns None values", result_empty["time_to_first_event_ms"] is None)


# ─── Run all ───

def main():
    global PASS, FAIL

    print("=" * 60)
    print("BENCHMARK SCORER REGRESSION TESTS")
    print("=" * 60)

    test_repeated_exact_lines()
    test_repeated_short_phrases()
    test_repeated_short_different_timestamps()
    test_no_false_positives()
    test_missing_proxy_log()
    test_malformed_proxy_log()
    test_valid_proxy_log()
    test_wer()
    test_monotonicity()
    test_latency_metrics()

    print("\n" + "=" * 60)
    print(f"RESULTS: {PASS} passed, {FAIL} failed")
    print("=" * 60)

    sys.exit(1 if FAIL > 0 else 0)


if __name__ == "__main__":
    main()
