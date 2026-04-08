"""Compare two benchmark run summaries side-by-side.

Usage:
    python3 compare_runs.py SUMMARY_A.json SUMMARY_B.json

Produces a human-readable comparison table and a machine-readable comparison.json.
"""

import json
import os
import sys


def safe_get(d: dict, *keys, default=None):
    """Nested dict get."""
    for k in keys:
        if isinstance(d, dict):
            d = d.get(k, default)
        else:
            return default
    return d


def fmt_val(v, fmt=None):
    if v is None:
        return "N/A"
    if fmt == "pct":
        return f"{v:.2%}"
    if fmt == "ms":
        return f"{v}ms"
    if fmt == "sec":
        return f"{v:.2f}s"
    if fmt == "int":
        return str(int(v))
    return str(v)


def delta_str(a, b, fmt=None, lower_better=True):
    if a is None or b is None:
        return ""
    diff = b - a
    if diff == 0:
        return "="
    arrow = "▲" if diff > 0 else "▼"
    # For lower-is-better metrics, up is bad
    if lower_better:
        quality = "worse" if diff > 0 else "better"
    else:
        quality = "better" if diff > 0 else "worse"

    if fmt == "pct":
        return f"{arrow} {abs(diff):.2%} ({quality})"
    if fmt == "ms":
        return f"{arrow} {abs(diff):.0f}ms ({quality})"
    if fmt == "sec":
        return f"{arrow} {abs(diff):.2f}s ({quality})"
    return f"{arrow} {abs(diff):.1f} ({quality})"


METRICS = [
    # (label, keys_path, format, lower_is_better)
    ("Implementation",       ["implementation"],                   None,  None),
    ("Run ID",               ["run_id"],                           None,  None),
    ("Audio Duration",       ["audio_duration_sec"],               "sec", None),
    ("Events",               ["event_count"],                      "int", False),
    ("WER",                  ["wer", "wer"],                       "pct", True),
    ("Ref Words",            ["wer", "ref_words"],                 "int", None),
    ("Hyp Words",            ["wer", "hyp_words"],                 "int", None),
    ("Substitutions",        ["wer", "substitutions"],             "int", True),
    ("Insertions",           ["wer", "insertions"],                "int", True),
    ("Deletions",            ["wer", "deletions"],                 "int", True),
    ("First Event (ms)",     ["latency", "time_to_first_event_ms"],"ms",  True),
    ("First Word (ms)",      ["latency", "time_to_first_word_ms"], "ms",  True),
    ("Mean Inter-Event (ms)",["latency", "mean_inter_event_ms"],   "ms",  True),
    ("Monotonicity Viols",   ["monotonicity", "monotonicity_violations"], "int", True),
    ("Dup Adjacent Words",   ["duplicates", "duplicate_word_count"],      "int", True),
    ("Repeated Event Lines", ["duplicates", "repeated_event_lines"],      "int", True),
    ("Repeated Phrase Words", ["duplicates", "repeated_phrase_words"],    "int", True),
    ("Short Phrase Rpt Words",["duplicates", "repeated_short_phrase_words"],"int", True),
    ("Repeated Norm Events", ["duplicates", "repeated_normalized_events"],"int", True),
    ("Top Repeated Text",    ["duplicates", "top_repeated_event_text"],   None, None),
    ("Top Repeated Count",   ["duplicates", "top_repeated_event_count"],  "int", True),
    ("Request Count",        ["proxy_metrics", "request_count"],          "int", None),
    ("Success / Error",      None,                                        None, None),  # special
    ("Trim Count",           ["proxy_metrics", "trim_count"],             "int", None),
    ("Mean Upstream Lat",    ["proxy_metrics", "mean_upstream_latency_ms"],"ms", True),
    ("Mean Chunk Size",      ["proxy_metrics", "mean_chunk_sec"],         "sec", None),
]


def main():
    if len(sys.argv) < 3:
        print("Usage: compare_runs.py SUMMARY_A.json SUMMARY_B.json", file=sys.stderr)
        sys.exit(1)

    with open(sys.argv[1]) as f:
        a = json.load(f)
    with open(sys.argv[2]) as f:
        b = json.load(f)

    label_a = safe_get(a, "implementation", default="A") or "A"
    label_b = safe_get(b, "implementation", default="B") or "B"

    col_w = 22
    val_w = 18
    delta_w = 26

    header = f"{'Metric':<{col_w}} {label_a:>{val_w}} {label_b:>{val_w}} {'Delta':>{delta_w}}"
    sep = "=" * len(header)

    print(sep)
    print("BENCHMARK COMPARISON")
    print(sep)
    print(header)
    print("-" * len(header))

    comparison_data = {}

    for label, keys, fmt, lower_better in METRICS:
        if label == "Success / Error":
            sc_a = safe_get(a, "proxy_metrics", "success_count")
            ec_a = safe_get(a, "proxy_metrics", "error_count")
            sc_b = safe_get(b, "proxy_metrics", "success_count")
            ec_b = safe_get(b, "proxy_metrics", "error_count")
            va = f"{sc_a}/{ec_a}" if sc_a is not None else "N/A"
            vb = f"{sc_b}/{ec_b}" if sc_b is not None else "N/A"
            print(f"{label:<{col_w}} {va:>{val_w}} {vb:>{val_w}} {'':>{delta_w}}")
            continue

        val_a = safe_get(a, *keys) if keys else None
        val_b = safe_get(b, *keys) if keys else None

        sa = fmt_val(val_a, fmt)
        sb = fmt_val(val_b, fmt)

        if lower_better is not None and isinstance(val_a, (int, float)) and isinstance(val_b, (int, float)):
            ds = delta_str(val_a, val_b, fmt, lower_better)
        else:
            ds = ""

        print(f"{label:<{col_w}} {sa:>{val_w}} {sb:>{val_w}} {ds:>{delta_w}}")

        if keys:
            flat_key = ".".join(keys)
            comparison_data[flat_key] = {"a": val_a, "b": val_b}

    print(sep)

    # Write comparison.json next to run B
    out_dir = os.path.dirname(sys.argv[2])
    comp_path = os.path.join(out_dir, "comparison.json")
    comp = {
        "run_a": sys.argv[1],
        "run_b": sys.argv[2],
        "label_a": label_a,
        "label_b": label_b,
        "metrics": comparison_data,
    }
    with open(comp_path, "w") as f:
        json.dump(comp, f, indent=2)
        f.write("\n")
    print(f"\nComparison saved: {comp_path}")


if __name__ == "__main__":
    main()
