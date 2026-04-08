"""Score a benchmark run by comparing transcript events against reference text.

Usage:
    python3 score_run.py --events-jsonl EVENTS.jsonl --reference REF.txt --output-dir REPORT_DIR
                         [--timings TIMINGS.txt] [--session-meta META.json] [--proxy-log LOG]

Outputs:
    summary.json  — machine-readable metrics
    summary.txt   — human-readable report
"""

import argparse
import json
import os
import re
import sys


def normalize_text(text: str) -> str:
    """Normalize text for comparison: lowercase, strip punctuation, collapse whitespace."""
    text = text.upper()  # LibriSpeech reference is uppercase
    text = text.lower()
    text = re.sub(r"[^\w\s]", "", text)  # remove punctuation
    text = re.sub(r"\s+", " ", text).strip()
    return text


def tokenize(text: str) -> list[str]:
    return normalize_text(text).split()


def compute_wer(ref_tokens: list[str], hyp_tokens: list[str]) -> dict:
    """Compute Word Error Rate using edit distance."""
    n = len(ref_tokens)
    m = len(hyp_tokens)

    # DP table
    d = [[0] * (m + 1) for _ in range(n + 1)]
    for i in range(n + 1):
        d[i][0] = i
    for j in range(m + 1):
        d[0][j] = j

    for i in range(1, n + 1):
        for j in range(1, m + 1):
            if ref_tokens[i - 1] == hyp_tokens[j - 1]:
                d[i][j] = d[i - 1][j - 1]
            else:
                d[i][j] = 1 + min(d[i - 1][j], d[i][j - 1], d[i - 1][j - 1])

    # Backtrace to count S, I, D
    i, j = n, m
    substitutions = insertions = deletions = 0
    while i > 0 or j > 0:
        if i > 0 and j > 0 and ref_tokens[i - 1] == hyp_tokens[j - 1]:
            i -= 1
            j -= 1
        elif i > 0 and j > 0 and d[i][j] == d[i - 1][j - 1] + 1:
            substitutions += 1
            i -= 1
            j -= 1
        elif j > 0 and d[i][j] == d[i][j - 1] + 1:
            insertions += 1
            j -= 1
        else:
            deletions += 1
            i -= 1

    total_errors = substitutions + insertions + deletions
    wer = total_errors / max(n, 1)

    return {
        "wer": round(wer, 4),
        "errors": total_errors,
        "substitutions": substitutions,
        "insertions": insertions,
        "deletions": deletions,
        "ref_words": n,
        "hyp_words": m,
    }


def check_monotonicity(events: list[dict]) -> dict:
    """Check that emitted timestamps are monotonically non-decreasing."""
    violations = 0
    prev_start = -1.0
    prev_end = -1.0
    for ev in events:
        s = ev.get("start_ms")
        e = ev.get("end_ms")
        if s is None or e is None:
            continue
        if s < prev_start - 0.5:  # allow tiny floating point slack
            violations += 1
        if e < s - 0.5:
            violations += 1
        prev_start = s
        prev_end = e
    return {"monotonicity_violations": violations}


def detect_duplicates(events: list[dict]) -> dict:
    """Detect repeated content in the transcript stream.

    Checks three levels:
    1. Adjacent identical words (original metric)
    2. Repeated event lines (exact raw_line duplicates)
    3. Repeated segments: sliding window over the word sequence to find
       repeated n-grams (n >= 3) that indicate phrase-level repetition.
    """
    words = []
    for ev in events:
        text = ev.get("text", "")
        words.extend(tokenize(text))

    # 1. Adjacent identical words
    adj_dup_count = 0
    for i in range(1, len(words)):
        if words[i] == words[i - 1]:
            adj_dup_count += 1

    # 2. Repeated event lines (exact raw_line match)
    raw_lines = [ev.get("raw_line", "") for ev in events if ev.get("raw_line", "").strip()]
    repeated_lines = 0
    seen_lines: dict[str, int] = {}
    for line in raw_lines:
        seen_lines[line] = seen_lines.get(line, 0) + 1
    for count in seen_lines.values():
        if count > 1:
            repeated_lines += count - 1

    # 3. Repeated phrases (n-gram detection, n=3..8)
    repeated_phrase_words = 0
    if len(words) >= 6:
        for ngram_size in range(3, min(9, len(words) // 2 + 1)):
            ngrams: dict[tuple, list[int]] = {}
            for i in range(len(words) - ngram_size + 1):
                gram = tuple(words[i : i + ngram_size])
                ngrams.setdefault(gram, []).append(i)
            for gram, positions in ngrams.items():
                if len(positions) < 2:
                    continue
                # Only count non-overlapping repeats
                last_end = -1
                repeat_count = 0
                for pos in positions:
                    if pos >= last_end:
                        if last_end >= 0:
                            repeat_count += 1
                        last_end = pos + ngram_size
                repeated_phrase_words = max(repeated_phrase_words, repeat_count * ngram_size)

    return {
        "duplicate_word_count": adj_dup_count,
        "repeated_event_lines": repeated_lines,
        "repeated_phrase_words": repeated_phrase_words,
    }


def compute_latency_metrics(events: list[dict], audio_duration_sec: float | None) -> dict:
    """Compute latency metrics from event arrival times."""
    if not events:
        return {
            "time_to_first_event_ms": None,
            "time_to_first_word_ms": None,
            "mean_inter_event_ms": None,
        }

    first_event_ms = events[0]["arrival_monotonic_ms"]
    first_word_ms = None
    for ev in events:
        if ev.get("text", "").strip():
            first_word_ms = ev["arrival_monotonic_ms"]
            break

    inter_event_times = []
    for i in range(1, len(events)):
        gap = events[i]["arrival_monotonic_ms"] - events[i - 1]["arrival_monotonic_ms"]
        inter_event_times.append(gap)

    mean_inter = None
    if inter_event_times:
        mean_inter = round(sum(inter_event_times) / len(inter_event_times), 1)

    return {
        "time_to_first_event_ms": first_event_ms,
        "time_to_first_word_ms": first_word_ms,
        "mean_inter_event_ms": mean_inter,
    }


def compute_coarse_timing_error(events: list[dict], timings: list[dict]) -> dict | None:
    """Compare emitted word timestamps against coarse reference timings.

    This is a rough sanity check, not precise word-level evaluation.
    """
    if not timings or not events:
        return None

    # Build a simple text-time mapping from reference timings
    ref_entries = []
    for t in timings:
        for word in tokenize(t["text"]):
            mid_sec = (t["start_sec"] + t["end_sec"]) / 2.0
            ref_entries.append((word, mid_sec))

    # Build from events
    hyp_entries = []
    for ev in events:
        if ev.get("start_ms") is None:
            continue
        for word in tokenize(ev.get("text", "")):
            mid_ms = (ev["start_ms"] + ev["end_ms"]) / 2.0
            hyp_entries.append((word, mid_ms / 1000.0))

    if not ref_entries or not hyp_entries:
        return None

    # Align by order and compute error for matched words
    errors = []
    ri = hi = 0
    while ri < len(ref_entries) and hi < len(hyp_entries):
        rw, rt = ref_entries[ri]
        hw, ht = hyp_entries[hi]
        if rw == hw:
            errors.append(abs(ht - rt))
            ri += 1
            hi += 1
        else:
            # Try to skip ahead in hypothesis
            ri += 1

    if not errors:
        return {"note": "no matching words for timing comparison", "matched_words": 0}

    errors.sort()
    return {
        "matched_words": len(errors),
        "mean_timing_error_sec": round(sum(errors) / len(errors), 3),
        "median_timing_error_sec": round(errors[len(errors) // 2], 3),
        "p90_timing_error_sec": round(errors[int(len(errors) * 0.9)], 3),
        "max_timing_error_sec": round(errors[-1], 3),
        "note": "coarse phrase-level timing comparison, not word-level ground truth",
    }


def parse_timings_file(path: str) -> list[dict]:
    """Parse merged reference-timings.txt: '<start_sec> <end_sec> <text>'."""
    entries = []
    with open(path) as f:
        for line in f:
            parts = line.strip().split(None, 2)
            if len(parts) >= 3:
                try:
                    entries.append({
                        "start_sec": float(parts[0]),
                        "end_sec": float(parts[1]),
                        "text": parts[2],
                    })
                except ValueError:
                    pass
    return entries


# Go proxy slog key=value parser
SLOG_KV_RE = re.compile(r'(\w+)=("(?:[^"\\]|\\.)*"|\S+)')


def parse_proxy_log(path: str) -> dict | None:
    """Parse Go proxy structured log for request-level metrics.

    Extracts: request count, latencies, trim events, chunk sizes.
    Returns None if the log format is not recognized.
    """
    if not os.path.isfile(path):
        return None

    requests = []
    trim_count = 0
    parse_failures = 0

    with open(path) as f:
        for line in f:
            if "transcribe request" not in line:
                continue

            kvs = {}
            for m in SLOG_KV_RE.finditer(line):
                key = m.group(1)
                val = m.group(2).strip('"')
                kvs[key] = val

            if "latency_ms" not in kvs or "status" not in kvs:
                parse_failures += 1
                continue

            try:
                req = {
                    "latency_ms": int(kvs["latency_ms"]),
                    "status": kvs["status"],
                    "sent_sec": float(kvs.get("sent_sec", 0)),
                    "word_count": int(kvs.get("word_count", 0)),
                }
                requests.append(req)
            except (ValueError, KeyError):
                parse_failures += 1
                continue

            if kvs.get("trimmed") == "true":
                trim_count += 1

    if not requests and parse_failures == 0:
        return None  # No transcribe request lines found (maybe not a Go proxy log)

    if parse_failures > 0 and not requests:
        return {"error": f"log format not recognized ({parse_failures} parse failures)", "request_count": 0}

    success_reqs = [r for r in requests if r["status"] == "success"]
    latencies = [r["latency_ms"] for r in success_reqs]
    chunk_sizes = [r["sent_sec"] for r in success_reqs if r["sent_sec"] > 0]

    result: dict = {
        "request_count": len(requests),
        "success_count": len(success_reqs),
        "error_count": len(requests) - len(success_reqs),
        "trim_count": trim_count,
    }

    if latencies:
        latencies.sort()
        result["mean_upstream_latency_ms"] = round(sum(latencies) / len(latencies), 1)
        result["median_upstream_latency_ms"] = latencies[len(latencies) // 2]
        result["p90_upstream_latency_ms"] = latencies[int(len(latencies) * 0.9)]
        result["max_upstream_latency_ms"] = latencies[-1]

    if chunk_sizes:
        result["mean_chunk_sec"] = round(sum(chunk_sizes) / len(chunk_sizes), 2)

    if parse_failures > 0:
        result["parse_failures"] = parse_failures

    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--events-jsonl", required=True, help="Path to events.jsonl")
    parser.add_argument("--reference", required=True, help="Path to reference.txt")
    parser.add_argument("--output-dir", required=True, help="Directory for report output")
    parser.add_argument("--timings", default=None, help="Path to reference-timings.txt")
    parser.add_argument("--session-meta", default=None, help="Path to session-meta.json")
    parser.add_argument("--proxy-log", default=None, help="Path to proxy stdout log for request metrics")
    args = parser.parse_args()

    os.makedirs(args.output_dir, exist_ok=True)

    # Load events
    events = []
    with open(args.events_jsonl) as f:
        for line in f:
            line = line.strip()
            if line:
                events.append(json.loads(line))

    # Load reference
    with open(args.reference) as f:
        reference_text = f.read().strip()

    # Load session meta
    session_meta = {}
    if args.session_meta and os.path.isfile(args.session_meta):
        with open(args.session_meta) as f:
            session_meta = json.load(f)

    audio_duration_sec = session_meta.get("audio_duration_sec")
    total_elapsed_ms = session_meta.get("total_elapsed_ms")

    # Build hypothesis transcript
    hyp_parts = []
    for ev in events:
        text = ev.get("text", "").strip()
        if text:
            hyp_parts.append(text)
    hypothesis_text = " ".join(hyp_parts)

    # Compute WER
    ref_tokens = tokenize(reference_text)
    hyp_tokens = tokenize(hypothesis_text)
    wer_result = compute_wer(ref_tokens, hyp_tokens)

    # Monotonicity
    mono_result = check_monotonicity(events)

    # Duplicates
    dup_result = detect_duplicates(events)

    # Latency
    latency_result = compute_latency_metrics(events, audio_duration_sec)

    # Coarse timing
    timing_result = None
    if args.timings and os.path.isfile(args.timings):
        timings = parse_timings_file(args.timings)
        timing_result = compute_coarse_timing_error(events, timings)

    # Proxy log metrics
    proxy_metrics = None
    if args.proxy_log:
        proxy_metrics = parse_proxy_log(args.proxy_log)

    # Build summary
    summary = {
        "event_count": len(events),
        "audio_duration_sec": audio_duration_sec,
        "total_elapsed_ms": total_elapsed_ms,
        "hypothesis_text": hypothesis_text,
        "reference_text_preview": reference_text[:200] + ("..." if len(reference_text) > 200 else ""),
        "wer": wer_result,
        "monotonicity": mono_result,
        "duplicates": dup_result,
        "latency": latency_result,
    }
    if timing_result is not None:
        summary["coarse_timing"] = timing_result
    if proxy_metrics is not None:
        summary["proxy_metrics"] = proxy_metrics

    # Write summary.json
    json_path = os.path.join(args.output_dir, "summary.json")
    with open(json_path, "w") as f:
        json.dump(summary, f, indent=2)
        f.write("\n")

    # Write summary.txt
    txt_path = os.path.join(args.output_dir, "summary.txt")
    with open(txt_path, "w") as f:
        f.write("=" * 60 + "\n")
        f.write("BENCHMARK SCORING REPORT\n")
        f.write("=" * 60 + "\n\n")

        if audio_duration_sec is not None:
            f.write(f"Audio duration:       {audio_duration_sec:.1f}s\n")
        if total_elapsed_ms is not None:
            f.write(f"Total elapsed:        {total_elapsed_ms / 1000:.1f}s\n")
        f.write(f"Events received:      {len(events)}\n\n")

        f.write("--- Transcript Quality ---\n")
        f.write(f"  WER:                {wer_result['wer']:.2%}\n")
        f.write(f"  Ref words:          {wer_result['ref_words']}\n")
        f.write(f"  Hyp words:          {wer_result['hyp_words']}\n")
        f.write(f"  Substitutions:      {wer_result['substitutions']}\n")
        f.write(f"  Insertions:         {wer_result['insertions']}\n")
        f.write(f"  Deletions:          {wer_result['deletions']}\n\n")

        f.write("--- Streaming Behavior ---\n")
        f.write(f"  Monotonicity viols: {mono_result['monotonicity_violations']}\n")
        f.write(f"  Dup adjacent words: {dup_result['duplicate_word_count']}\n")
        f.write(f"  Repeated evt lines: {dup_result['repeated_event_lines']}\n")
        f.write(f"  Repeated phrase wds:{dup_result['repeated_phrase_words']}\n\n")

        f.write("--- Latency ---\n")
        ttfe = latency_result.get("time_to_first_event_ms")
        ttfw = latency_result.get("time_to_first_word_ms")
        mie = latency_result.get("mean_inter_event_ms")
        f.write(f"  First event:        {ttfe}ms\n" if ttfe is not None else "  First event:        N/A\n")
        f.write(f"  First word:         {ttfw}ms\n" if ttfw is not None else "  First word:         N/A\n")
        f.write(f"  Mean inter-event:   {mie}ms\n" if mie is not None else "  Mean inter-event:   N/A\n")

        if timing_result is not None:
            f.write("\n--- Coarse Timing Error ---\n")
            if "matched_words" in timing_result:
                f.write(f"  Matched words:      {timing_result['matched_words']}\n")
            if "mean_timing_error_sec" in timing_result:
                f.write(f"  Mean error:         {timing_result['mean_timing_error_sec']:.3f}s\n")
                f.write(f"  Median error:       {timing_result['median_timing_error_sec']:.3f}s\n")
                f.write(f"  P90 error:          {timing_result['p90_timing_error_sec']:.3f}s\n")
                f.write(f"  Max error:          {timing_result['max_timing_error_sec']:.3f}s\n")
            if "note" in timing_result:
                f.write(f"  Note: {timing_result['note']}\n")

        if proxy_metrics is not None and "error" not in proxy_metrics:
            f.write("\n--- Proxy Request Metrics ---\n")
            f.write(f"  Request count:      {proxy_metrics.get('request_count', 'N/A')}\n")
            f.write(f"  Success / Error:    {proxy_metrics.get('success_count', '?')}/{proxy_metrics.get('error_count', '?')}\n")
            f.write(f"  Trim events:        {proxy_metrics.get('trim_count', 'N/A')}\n")
            if "mean_upstream_latency_ms" in proxy_metrics:
                f.write(f"  Mean latency:       {proxy_metrics['mean_upstream_latency_ms']}ms\n")
                f.write(f"  Median latency:     {proxy_metrics['median_upstream_latency_ms']}ms\n")
                f.write(f"  P90 latency:        {proxy_metrics['p90_upstream_latency_ms']}ms\n")
                f.write(f"  Max latency:        {proxy_metrics['max_upstream_latency_ms']}ms\n")
            if "mean_chunk_sec" in proxy_metrics:
                f.write(f"  Mean chunk size:    {proxy_metrics['mean_chunk_sec']}s\n")

        f.write("\n--- Final Transcript ---\n")
        f.write(hypothesis_text[:2000] + ("\n...(truncated)" if len(hypothesis_text) > 2000 else "") + "\n")

    print(f"Score report: {args.output_dir}")
    print(f"  WER: {wer_result['wer']:.2%} ({wer_result['errors']} errors / {wer_result['ref_words']} ref words)")
    if latency_result.get("time_to_first_word_ms") is not None:
        print(f"  First word: {latency_result['time_to_first_word_ms']}ms")
    print(f"  Monotonicity violations: {mono_result['monotonicity_violations']}")
    if dup_result["repeated_event_lines"] > 0:
        print(f"  Repeated event lines: {dup_result['repeated_event_lines']}")
    if dup_result["repeated_phrase_words"] > 0:
        print(f"  Repeated phrase words: {dup_result['repeated_phrase_words']}")


if __name__ == "__main__":
    main()
