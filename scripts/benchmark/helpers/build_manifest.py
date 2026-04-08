"""Build session manifest, reference transcript, and merged timings.

Called by concat-session.sh after WAV concatenation.
"""

import argparse
import json
import math
import os
import re
import subprocess


SAMPLE_RATE = 16000
CHANNELS = 1
BITS_PER_SAMPLE = 16

TIMINGS_RE = re.compile(
    r"^\[(\d+:\d+:\d+\.\d+)\s*-->\s*(\d+:\d+:\d+\.\d+)\]\s*(.*)"
)


def hms_to_sec(hms: str) -> float:
    parts = hms.split(":")
    return float(parts[0]) * 3600 + float(parts[1]) * 60 + float(parts[2])


def get_wav_duration(path: str) -> float:
    out = subprocess.check_output(
        [
            "ffprobe", "-v", "error",
            "-show_entries", "format=duration",
            "-of", "default=noprint_wrappers=1:nokey=1",
            path,
        ],
        text=True,
    )
    return float(out.strip())


def find_reference_text(clip_basename: str, clip_dir: str) -> str:
    """Look up reference text for a clip using LibriSpeech or LibriTTS conventions."""
    for fname in os.listdir(clip_dir):
        if fname.endswith(".trans.txt"):
            trans_path = os.path.join(clip_dir, fname)
            with open(trans_path) as f:
                for line in f:
                    if line.startswith(clip_basename + " "):
                        return line[len(clip_basename) + 1 :].strip()

    norm_path = os.path.join(clip_dir, clip_basename + ".normalized.txt")
    if os.path.isfile(norm_path):
        return open(norm_path).read().strip()

    orig_path = os.path.join(clip_dir, clip_basename + ".original.txt")
    if os.path.isfile(orig_path):
        return open(orig_path).read().strip()

    return ""


def parse_timings(timings_path: str, offset_sec: float) -> list[dict]:
    """Parse a .timings.txt file and shift timestamps by offset_sec."""
    entries = []
    with open(timings_path) as f:
        for line in f:
            m = TIMINGS_RE.match(line.strip())
            if not m:
                continue
            start = hms_to_sec(m.group(1)) + offset_sec
            end = hms_to_sec(m.group(2)) + offset_sec
            text = m.group(3).strip()
            entries.append({"start_sec": round(start, 3), "end_sec": round(end, 3), "text": text})
    return entries


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--session-id", required=True)
    parser.add_argument("--session-wav", required=True)
    parser.add_argument("--wav-list", required=True, help="File with one WAV path per line")
    parser.add_argument("--repo-root", required=True)
    parser.add_argument("--output-dir", required=True)
    args = parser.parse_args()

    with open(args.wav_list) as f:
        wav_files = [line.strip() for line in f if line.strip()]

    total_duration = get_wav_duration(args.session_wav)

    clips = []
    all_ref_parts = []
    all_timings = []
    cumulative_samples = 0

    for i, wav_path in enumerate(wav_files):
        wav_path = os.path.realpath(wav_path)
        clip_basename = os.path.splitext(os.path.basename(wav_path))[0]
        clip_dir = os.path.dirname(wav_path)

        duration_sec = get_wav_duration(wav_path)
        duration_samples = int(math.floor(duration_sec * SAMPLE_RATE))
        start_sample = cumulative_samples
        end_sample = cumulative_samples + duration_samples
        start_sec = start_sample / SAMPLE_RATE
        end_sec = end_sample / SAMPLE_RATE

        ref_text = find_reference_text(clip_basename, clip_dir)
        if ref_text:
            all_ref_parts.append(ref_text)

        timings_path = os.path.join(clip_dir, clip_basename + ".timings.txt")
        has_timings = os.path.isfile(timings_path)
        if has_timings:
            all_timings.extend(parse_timings(timings_path, start_sec))

        clips.append({
            "clip_index": i,
            "clip_id": clip_basename,
            "wav_path": os.path.relpath(wav_path, os.path.realpath(args.repo_root)),
            "start_sample": start_sample,
            "end_sample": end_sample,
            "start_offset_sec": round(start_sec, 6),
            "end_offset_sec": round(end_sec, 6),
            "duration_sec": round(duration_sec, 6),
            "reference_text": ref_text,
            "timings_path": os.path.relpath(timings_path, os.path.realpath(args.repo_root)) if has_timings else None,
        })

        cumulative_samples = end_sample

    ref_path = os.path.join(args.output_dir, "reference.txt")
    with open(ref_path, "w") as f:
        f.write(" ".join(all_ref_parts) + "\n")

    if all_timings:
        timings_out = os.path.join(args.output_dir, "reference-timings.txt")
        with open(timings_out, "w") as f:
            for t in all_timings:
                f.write(f"{t['start_sec']:.3f} {t['end_sec']:.3f} {t['text']}\n")

    manifest = {
        "session_id": args.session_id,
        "sample_rate": SAMPLE_RATE,
        "channels": CHANNELS,
        "bits_per_sample": BITS_PER_SAMPLE,
        "total_samples": cumulative_samples,
        "total_duration_sec": round(total_duration, 6),
        "session_wav": "session.wav",
        "reference_text_file": "reference.txt",
        "source_clips": clips,
    }
    manifest_path = os.path.join(args.output_dir, "manifest.json")
    with open(manifest_path, "w") as f:
        json.dump(manifest, f, indent=2)
        f.write("\n")


if __name__ == "__main__":
    main()
