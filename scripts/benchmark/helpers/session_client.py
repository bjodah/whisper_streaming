"""Full-duplex TCP session client for whisper-proxy benchmarking.

Streams raw PCM audio over a TCP connection at configurable pace while
concurrently reading timestamped transcript lines on the same connection.

Usage:
    python3 session_client.py --wav SESSION.wav --host localhost --port 43007 --output-dir RUN_DIR

Outputs:
    events.jsonl  — one JSON object per received transcript line
    events.txt    — human-readable transcript event log
    session-meta.json — run metadata
    sender.log    — send-side transport log
"""

import argparse
import json
import os
import socket
import struct
import subprocess
import threading
import time
import wave


def get_monotonic_ms():
    return int(time.monotonic() * 1000)


def get_wall_time():
    now = time.time()
    msec = int((now % 1) * 1000)
    return time.strftime("%Y-%m-%dT%H:%M:%S", time.gmtime(now)) + f".{msec:03d}Z"


def read_pcm_from_wav(wav_path: str) -> tuple[bytes, int, int, int]:
    """Read raw PCM from a WAV file. Returns (pcm_bytes, sample_rate, channels, sample_width)."""
    with wave.open(wav_path, "rb") as wf:
        sr = wf.getframerate()
        ch = wf.getnchannels()
        sw = wf.getsampwidth()
        pcm = wf.readframes(wf.getnframes())
    return pcm, sr, ch, sw


def receiver_thread(sock: socket.socket, events: list, lock: threading.Lock,
                    start_mono_ms: int, done_event: threading.Event,
                    sender_log: list, sender_lock: threading.Lock):
    """Read newline-delimited transcript lines from the socket."""
    buf = b""
    event_index = 0
    try:
        while True:
            try:
                data = sock.recv(4096)
            except OSError:
                break
            if not data:
                break
            buf += data
            while b"\n" in buf:
                line, buf = buf.split(b"\n", 1)
                raw_line = line.decode("utf-8", errors="replace").strip()
                if not raw_line:
                    continue
                arrival_mono = get_monotonic_ms()
                now = time.time()
                msec = int((now % 1) * 1000)
                arrival_wall = time.strftime("%Y-%m-%dT%H:%M:%S", time.gmtime(now)) + f".{msec:03d}Z"

                # Parse "<start_ms> <end_ms> <text>"
                parts = raw_line.split(None, 2)
                start_ms = end_ms = None
                text = raw_line
                if len(parts) >= 3:
                    try:
                        start_ms = float(parts[0])
                        end_ms = float(parts[1])
                        text = parts[2]
                    except ValueError:
                        pass

                event = {
                    "event_index": event_index,
                    "arrival_monotonic_ms": arrival_mono - start_mono_ms,
                    "arrival_wall_time": arrival_wall,
                    "start_ms": start_ms,
                    "end_ms": end_ms,
                    "text": text,
                    "raw_line": raw_line,
                }
                with lock:
                    events.append(event)
                event_index += 1
    finally:
        done_event.set()


def sender_thread(sock: socket.socket, pcm_data: bytes, sample_rate: int,
                  channels: int, sample_width: int, frame_duration_ms: int,
                  speed: float, sender_log: list, sender_lock: threading.Lock,
                  start_mono_ms: int):
    """Send PCM data in fixed-size frames at real-time pace."""
    bytes_per_sample = channels * sample_width
    frame_samples = int(sample_rate * frame_duration_ms / 1000)
    frame_bytes = frame_samples * bytes_per_sample
    sleep_sec = (frame_duration_ms / 1000.0) / speed

    offset = 0
    frame_count = 0
    total_bytes = len(pcm_data)

    while offset < total_bytes:
        chunk = pcm_data[offset : offset + frame_bytes]
        try:
            sock.sendall(chunk)
        except OSError as e:
            with sender_lock:
                sender_log.append(f"[{get_monotonic_ms() - start_mono_ms}ms] send error at offset {offset}: {e}")
            break

        offset += len(chunk)
        frame_count += 1

        if frame_count % 500 == 0:
            elapsed = get_monotonic_ms() - start_mono_ms
            with sender_lock:
                sender_log.append(f"[{elapsed}ms] sent {offset}/{total_bytes} bytes ({frame_count} frames)")

        time.sleep(sleep_sec)

    # Half-close: signal end-of-audio while keeping read side open
    try:
        sock.shutdown(socket.SHUT_WR)
    except OSError:
        pass

    elapsed = get_monotonic_ms() - start_mono_ms
    with sender_lock:
        sender_log.append(f"[{elapsed}ms] send complete: {offset} bytes, {frame_count} frames")


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--wav", required=True, help="Session WAV file")
    parser.add_argument("--host", default="localhost", help="Proxy host (default: localhost)")
    parser.add_argument("--port", type=int, default=43007, help="Proxy TCP port (default: 43007)")
    parser.add_argument("--output-dir", required=True, help="Directory for output files")
    parser.add_argument("--frame-ms", type=int, default=40, help="Frame duration in ms (default: 40)")
    parser.add_argument("--speed", type=float, default=1.0, help="Playback speed multiplier (default: 1.0)")
    parser.add_argument("--recv-timeout", type=float, default=15.0,
                        help="Seconds to wait for trailing transcript after audio ends (default: 15)")
    args = parser.parse_args()

    os.makedirs(args.output_dir, exist_ok=True)

    pcm_data, sr, ch, sw = read_pcm_from_wav(args.wav)
    audio_duration_sec = len(pcm_data) / (sr * ch * sw)

    print(f"Session: {args.wav}")
    print(f"  Audio: {audio_duration_sec:.1f}s, {sr}Hz, {ch}ch, {sw*8}bit")
    print(f"  Target: {args.host}:{args.port}")
    print(f"  Frame: {args.frame_ms}ms, speed: {args.speed}x")

    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.settimeout(args.recv_timeout)
    sock.connect((args.host, args.port))

    events = []
    events_lock = threading.Lock()
    sender_log = []
    sender_lock = threading.Lock()
    recv_done = threading.Event()

    start_mono_ms = get_monotonic_ms()
    start_wall = time.strftime("%Y-%m-%dT%H:%M:%S", time.gmtime()) + "Z"

    recv_t = threading.Thread(
        target=receiver_thread,
        args=(sock, events, events_lock, start_mono_ms, recv_done, sender_log, sender_lock),
        daemon=True,
    )
    send_t = threading.Thread(
        target=sender_thread,
        args=(sock, pcm_data, sr, ch, sw, args.frame_ms, args.speed,
              sender_log, sender_lock, start_mono_ms),
    )

    recv_t.start()
    send_t.start()
    send_t.join()

    # Wait for trailing transcript events after audio ends
    recv_done.wait(timeout=args.recv_timeout)
    # Give a brief extra window for any final data
    time.sleep(1.0)

    try:
        sock.close()
    except OSError:
        pass

    end_mono_ms = get_monotonic_ms()
    total_elapsed_ms = end_mono_ms - start_mono_ms

    print(f"  Elapsed: {total_elapsed_ms / 1000:.1f}s")
    print(f"  Events received: {len(events)}")

    # Write events.jsonl
    jsonl_path = os.path.join(args.output_dir, "events.jsonl")
    with open(jsonl_path, "w") as f:
        for ev in events:
            f.write(json.dumps(ev) + "\n")

    # Write events.txt
    txt_path = os.path.join(args.output_dir, "events.txt")
    with open(txt_path, "w") as f:
        for ev in events:
            ts = f"[{ev['arrival_monotonic_ms']}ms]"
            if ev["start_ms"] is not None:
                f.write(f"{ts} {ev['start_ms']:.0f} {ev['end_ms']:.0f} {ev['text']}\n")
            else:
                f.write(f"{ts} {ev['raw_line']}\n")

    # Write sender.log
    log_path = os.path.join(args.output_dir, "sender.log")
    with open(log_path, "w") as f:
        for entry in sender_log:
            f.write(entry + "\n")

    # Write session-meta.json
    meta = {
        "session_wav": os.path.basename(args.wav),
        "host": args.host,
        "port": args.port,
        "frame_duration_ms": args.frame_ms,
        "speed": args.speed,
        "audio_duration_sec": round(audio_duration_sec, 6),
        "total_elapsed_ms": total_elapsed_ms,
        "event_count": len(events),
        "start_wall_time": start_wall,
        "recv_timeout_sec": args.recv_timeout,
    }
    meta_path = os.path.join(args.output_dir, "session-meta.json")
    with open(meta_path, "w") as f:
        json.dump(meta, f, indent=2)
        f.write("\n")

    print(f"  Output: {args.output_dir}")


if __name__ == "__main__":
    main()
