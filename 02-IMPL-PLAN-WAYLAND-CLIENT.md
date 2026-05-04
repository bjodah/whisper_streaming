# Implementation Plan: `strisper-wayland` — Linux/Wayland/GNOME Client

> **Audience:** Junior developer implementing the Wayland client for the existing
> `whisper-proxy` Go server.  Follow the sections in order.
>
> **Reference material in this repo:**
> - `emacs-client/strisper.el` — simplest existing client; shows the wire protocol
> - `dotnet-windows-client/StrisperClient/Form1.cs` — richer client with audio capture
> - `/work/TalkType/` — competing Python product; use only as inspiration for
>   Wayland/GNOME-specific patterns (evdev, ydotool, D-Bus, GNOME extension)
> - `README.md` (root) — server protocol, runtime flags

---

## 1. Background and Goals

The `whisper-proxy` TCP server accepts raw PCM audio (`S16_LE`, 16 000 Hz, mono)
and replies with newline-terminated transcription lines in the format:

```
<start_ms> <end_ms> <transcribed text>\n
```

All existing clients follow the same basic loop:

1. Open a TCP connection to the server.
2. Stream raw PCM bytes from the microphone into the TCP socket.
3. Read text lines back from the socket.
4. "Type" those lines into whatever window has keyboard focus.

The new `strisper-wayland` client must do all of that on a Wayland desktop,
while adding a configurable global shortcut key (default `Ctrl+Shift+F9`) that
**toggles** recording on and off — analogous to how the Windows client registers
global hotkeys via `RegisterHotKey`.

### Why Rust?

- **Single static binary** — no Python virtualenv, no .NET runtime, no dynamic
  library dependencies beyond libc.  The same zero-dependency philosophy as the
  Go server.
- **Excellent Linux ecosystem** — `evdev`, `zbus`, `cpal` crates cover every
  system-level concern we have.
- **Memory safety and fearless concurrency** — audio capture, TCP I/O, D-Bus, and
  hotkey listening all run concurrently; Rust's ownership model prevents the
  data-race bugs that plague multi-threaded C.
- Not Go, because Go's audio and evdev ecosystem is thin and the binary would
  require CGo for audio anyway.

### Why a GNOME Shell Extension for the Hotkey?

On Wayland, applications cannot observe other applications' key presses — that
is a deliberate security property of the protocol.  There are two escape hatches:

1. **evdev grab** — open `/dev/input/eventN` directly and call `EVIOCGRAB`.  This
   works on all compositors but requires the user to be in the `input` group and
   disables the key for every other app while the device is grabbed.
2. **GNOME Shell keybinding API** — only available inside GNOME extensions (which
   run inside the compositor process).  The extension registers a keybinding with
   GNOME Settings Daemon; GNOME then owns the interception and calls back into
   the extension.  No special permissions needed; the key still reaches other apps
   normally between recordings.

We use option 2 as the **primary** mechanism because:
- No permission headaches for the user.
- Works even when the compositor is busy (e.g., alt-tabbing).
- The keybinding is visible in GNOME's keyboard settings.

We implement option 1 as a **fallback** for users on non-GNOME compositors
(Sway, Hyprland, KDE Plasma, etc.).

---

## 2. Directory Layout

All files live under `wayland-client/` in this repo.

```
wayland-client/
├── strisper-wayland/                       # Rust crate
│   ├── Cargo.toml
│   └── src/
│       ├── main.rs                         # Entry point; wires all modules
│       ├── config.rs                       # TOML config load/save
│       ├── audio.rs                        # Mic capture via cpal
│       ├── proxy.rs                        # TCP client to whisper-proxy
│       ├── inject.rs                       # Text injection (ydotool / wtype)
│       ├── hotkey.rs                       # Fallback hotkey via evdev
│       └── dbus.rs                         # D-Bus service (zbus)
│
├── gnome-extension/
│   └── strisper@whisper-streaming/         # GNOME Shell extension UUID
│       ├── metadata.json
│       ├── extension.js
│       ├── stylesheet.css
│       └── schemas/
│           └── org.gnome.shell.extensions.strisper-wayland.gschema.xml
│
├── io.github.bjodah.StrisperWayland.xml   # D-Bus introspection XML
├── strisper-wayland.service               # systemd user service
├── strisper-wayland.desktop               # XDG autostart entry
├── install.sh                             # One-shot install script
└── README.md                              # User-facing documentation
```

---

## 3. Data Flow and Module Relationships

```
GNOME Extension (extension.js)
    │  global keybinding pressed
    │  D-Bus call: ToggleRecording()
    ▼
dbus.rs  ──cmd_tx──►  main.rs  (tokio task: command loop)
    ▲                     │
    │ signals              │ spawns on recording start:
    │ RecordingStateChanged│
    │ TranscriptionReceived│        ┌──────────────────────┐
    │                      │        │                      │
    │                      ▼        ▼                      │
    │               audio.rs    proxy.rs               inject.rs
    │               (cpal mic)  (TCP client)           (ydotool)
    │                  PCM bytes ──────────────────►         ▲
    │                            ◄── text lines ─────────────┘
    │                                      │
    └──────────────────────────────────────┘
                    (text lines forwarded as D-Bus signals)

hotkey.rs  ──cmd_tx──►  main.rs   (evdev fallback, non-GNOME only)
```

**Key insight:** `audio.rs` and `proxy.rs` are decoupled by a
`tokio::sync::mpsc` channel.  `proxy.rs` writes PCM to the server and reads
text lines back; it forwards those lines through a second channel to
`inject.rs`.  All three tasks are spawned together when recording starts and
cancelled together when it stops.

---

## 4. External Runtime Dependencies

| Tool | Purpose | Required? |
|------|---------|-----------|
| `ydotoold` daemon | Receives injection commands from `ydotool` | Yes, for text injection on GNOME/wlroots |
| `ydotool` | Types text via uinput kernel interface | Primary injection method |
| `wtype` | Types text via Wayland virtual keyboard protocol | Fallback if `ydotool` absent |
| `arecord` | *Not* needed — we use `cpal` directly | No |
| `glib-compile-schemas` | Compile GSettings schema for the GNOME extension | At install time only |
| `cargo` | Build the Rust binary | At build time only |

**Note on `ydotool`/uinput permissions:** `ydotool` requires
`/dev/uinput` access (see TalkType's `uinput_helper.py` for reference).
The install script adds the user to the `input` group and installs a udev rule.
This is identical to what TalkType requires and is explained in detail in
`README.md`.

---

## 5. File-by-File Implementation

### 5.1 `strisper-wayland/Cargo.toml`

**Purpose:** Rust crate manifest.  Declares the binary target and all
third-party crates.

**Rationale for each dependency:**
- `tokio` — async runtime.  `audio.rs`, `proxy.rs`, `inject.rs`, `dbus.rs`
  all block on I/O; Tokio lets them run concurrently on a thread pool without
  manual thread management.
- `zbus` — pure-Rust async D-Bus library.  Chosen over `dbus-rs` because it is
  fully async (compatible with Tokio) and has a clean derive-macro API.
- `cpal` — cross-platform audio I/O.  Supports PipeWire (via ALSA bridge),
  PulseAudio, and bare ALSA.  Eliminates the `arecord` subprocess dependency
  while covering all common Linux audio setups.
- `rubato` — high-quality audio resampler.  Many USB microphones and some ALSA
  devices only expose 48 000 Hz; we must resample to 16 000 Hz to match the
  server's expectation.
- `evdev` — reads raw Linux input events.  Used only by `hotkey.rs` (the
  non-GNOME fallback).
- `serde` + `toml` — structured config file (TOML) parsed into typed Rust
  structs.
- `clap` — command-line argument parsing with `--derive` macros.
- `anyhow` — ergonomic error propagation.
- `tracing` + `tracing-subscriber` — structured logging (respects `RUST_LOG`).

```toml
[package]
name = "strisper-wayland"
version = "0.1.0"
edition = "2021"
description = "Wayland/GNOME speech-to-text client for whisper-proxy"

[[bin]]
name = "strisper-wayland"
path = "src/main.rs"

[dependencies]
tokio = { version = "1", features = ["full"] }
zbus = { version = "4", features = ["tokio"] }
cpal = "0.15"
rubato = "0.15"
evdev = "0.12"
serde = { version = "1", features = ["derive"] }
toml = "0.8"
clap = { version = "4", features = ["derive"] }
anyhow = "1"
tracing = "0.1"
tracing-subscriber = { version = "0.3", features = ["env-filter"] }
```

---

### 5.2 `strisper-wayland/src/config.rs`

**Purpose:** Load configuration from `~/.config/strisper-wayland/config.toml`,
write a default file if none exists, and expose a `Config` struct to the rest of
the app.  CLI flags (parsed in `main.rs`) override config-file values after
loading.

**Key design:** Every sub-section has `#[serde(default)]` so that missing TOML
keys fall back to the `Default` implementation rather than causing a parse error.
This lets users omit sections they don't need and ensures forward compatibility
when new settings are added.

```rust
// src/config.rs
use serde::{Deserialize, Serialize};
use std::path::PathBuf;
use anyhow::Result;

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(default)]
pub struct Config {
    pub server: ServerConfig,
    pub hotkey: HotkeyConfig,
    pub audio: AudioConfig,
    pub inject: InjectConfig,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(default)]
pub struct ServerConfig {
    pub host: String,   // default: "localhost"
    pub port: u16,      // default: 43007
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(default)]
pub struct HotkeyConfig {
    /// Used by the evdev fallback only.  The GNOME extension reads its own
    /// GSettings key instead (see schemas/org.gnome.shell.extensions…).
    pub key: String,    // default: "Ctrl+Shift+F9"
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(default)]
pub struct AudioConfig {
    /// Substring matched against CPAL device names.  Empty = system default.
    pub device: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(default)]
pub struct InjectConfig {
    /// "auto" | "ydotool" | "wtype"
    pub method: String,
    /// Milliseconds between synthetic keystrokes (ydotool -d flag).
    /// Higher = more reliable on slow systems; 12 is a good default.
    pub delay_ms: u32,
}

impl Default for Config {
    fn default() -> Self {
        Config {
            server:  ServerConfig::default(),
            hotkey:  HotkeyConfig::default(),
            audio:   AudioConfig::default(),
            inject:  InjectConfig::default(),
        }
    }
}
impl Default for ServerConfig {
    fn default() -> Self { Self { host: "localhost".into(), port: 43007 } }
}
impl Default for HotkeyConfig {
    fn default() -> Self { Self { key: "Ctrl+Shift+F9".into() } }
}
impl Default for AudioConfig {
    fn default() -> Self { Self { device: String::new() } }
}
impl Default for InjectConfig {
    fn default() -> Self { Self { method: "auto".into(), delay_ms: 12 } }
}

pub fn config_path() -> PathBuf {
    dirs::config_dir()
        .unwrap_or_else(|| PathBuf::from("~/.config"))
        .join("strisper-wayland")
        .join("config.toml")
}

pub fn load() -> Result<Config> {
    let path = config_path();
    if !path.exists() {
        // Write defaults so the user has something to edit.
        let dir = path.parent().unwrap();
        std::fs::create_dir_all(dir)?;
        let defaults = Config::default();
        let text = toml::to_string_pretty(&defaults)?;
        std::fs::write(&path, text)?;
        tracing::info!("Created default config at {}", path.display());
        return Ok(defaults);
    }
    let text = std::fs::read_to_string(&path)?;
    let cfg: Config = toml::from_str(&text)?;
    Ok(cfg)
}
```

**Note:** Add `dirs = "5"` to `Cargo.toml` dependencies to resolve
`~/.config` portably.

---

### 5.3 `strisper-wayland/src/audio.rs`

**Purpose:** Open the system microphone via `cpal`, capture samples at 16 000 Hz
mono, convert to S16LE bytes (the format expected by `whisper-proxy`), and send
chunks through an `mpsc` channel to `proxy.rs`.

**Why not spawn `arecord`?** The Emacs client uses `arecord | nc` because Emacs
Lisp has no audio API.  We are in Rust; using `cpal` keeps the binary fully
self-contained, avoids process lifecycle complexity, and gives us direct control
over the sample format and device selection.

**The cpal → S16LE conversion:**  cpal defaults to `f32` samples in the range
`[-1.0, 1.0]`.  Multiply by `i16::MAX as f32` and clamp to obtain the signed
16-bit PCM the server expects.  If the device only supports 48 000 Hz natively,
use `rubato` to resample from 48 000 to 16 000 Hz before sending.

```rust
// src/audio.rs
use cpal::traits::{DeviceTrait, HostTrait, StreamTrait};
use rubato::{Resampler, SincFixedIn, SincInterpolationType, SincInterpolationParameters, WindowFunction};
use tokio::sync::mpsc;
use anyhow::{Context, Result};

const TARGET_SAMPLE_RATE: u32 = 16_000;
const CHANNELS: u16 = 1;

/// A handle that stops audio capture when dropped.
pub struct AudioHandle {
    // cpal streams stop when dropped.
    _stream: cpal::Stream,
}

/// Start microphone capture.
///
/// `device_hint`: substring matched against device names; empty = system default.
///
/// Returns (handle, receiver).  Drop the handle to stop capture.
/// The receiver yields `Vec<u8>` of raw S16LE bytes ready to write to the proxy.
pub fn start(device_hint: &str) -> Result<(AudioHandle, mpsc::Receiver<Vec<u8>>)> {
    let host = cpal::default_host();

    // Select device: prefer the hint match, fall back to default.
    let device = if device_hint.is_empty() {
        host.default_input_device()
            .context("no default input device")?
    } else {
        host.input_devices()?
            .find(|d| d.name().map(|n| n.contains(device_hint)).unwrap_or(false))
            .context("no input device matching hint")?
    };

    tracing::info!("Using audio device: {}", device.name().unwrap_or_default());

    // Prefer 16 kHz; fall back to device default (often 48 kHz).
    let supported = device.default_input_config()?;
    let native_sr = supported.sample_rate().0;
    let needs_resample = native_sr != TARGET_SAMPLE_RATE;

    let config = cpal::StreamConfig {
        channels: CHANNELS,
        sample_rate: cpal::SampleRate(native_sr),
        buffer_size: cpal::BufferSize::Default,
    };

    let (tx, rx) = mpsc::channel::<Vec<u8>>(64);

    // If resampling is needed, initialise a rubato resampler.
    // rubato works on f64 chunks; we use SincFixedIn for real-time use.
    let maybe_resampler = if needs_resample {
        let params = SincInterpolationParameters {
            sinc_len: 128,
            f_cutoff: 0.95,
            interpolation: SincInterpolationType::Linear,
            oversampling_factor: 128,
            window: WindowFunction::BlackmanHarris2,
        };
        Some(std::sync::Mutex::new(
            SincFixedIn::<f64>::new(
                TARGET_SAMPLE_RATE as f64 / native_sr as f64,
                2.0,
                params,
                512,
                1,
            )?,
        ))
    } else {
        None
    };
    let maybe_resampler = std::sync::Arc::new(maybe_resampler);

    let stream = device.build_input_stream(
        &config,
        move |data: &[f32], _| {
            // 1. Mix to mono if stereo (shouldn't happen — we request 1 channel).
            let mono: Vec<f32> = data.to_vec();

            // 2. Optionally resample.
            let pcm_f32: Vec<f32> = if let Some(ref rs) = *maybe_resampler {
                let f64_in: Vec<f64> = mono.iter().map(|&s| s as f64).collect();
                let mut guard = rs.lock().unwrap();
                match guard.process(&[f64_in], None) {
                    Ok(out) => out[0].iter().map(|&s| s as f32).collect(),
                    Err(_) => return,
                }
            } else {
                mono
            };

            // 3. Convert f32 → S16LE bytes.
            let mut bytes = Vec::with_capacity(pcm_f32.len() * 2);
            for s in pcm_f32 {
                let i = (s * i16::MAX as f32).clamp(i16::MIN as f32, i16::MAX as f32) as i16;
                bytes.extend_from_slice(&i.to_le_bytes());
            }

            // 4. Send to proxy task (drop if the channel is full — better than blocking).
            let _ = tx.try_send(bytes);
        },
        |err| tracing::error!("audio stream error: {err}"),
        None,
    )?;

    stream.play()?;
    Ok((AudioHandle { _stream: stream }, rx))
}
```

---

### 5.4 `strisper-wayland/src/proxy.rs`

**Purpose:** Open a `TcpStream` to `whisper-proxy`, forward PCM bytes from the
audio channel, and read back text lines.  Expose one function that returns the
handle plus two channels (one for writing PCM in, one for reading text out).

**Wire protocol (from the server and Emacs client):**
- Connect TCP to `<host>:<port>`.
- Write raw PCM bytes; no framing, no header — exactly what `arecord | nc` does.
- Read newline-terminated lines `"<start_ms> <end_ms> <text>\n"`.
- The connection stays open for the full recording session.
- Close the TCP write half to signal end-of-audio; the server will flush its
  final hypothesis and close the connection.

```rust
// src/proxy.rs
use tokio::net::TcpStream;
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};
use tokio::sync::mpsc;
use anyhow::Result;

/// Connect to whisper-proxy and return:
///   - `pcm_tx`: send raw S16LE bytes here (from audio.rs)
///   - `text_rx`: receive transcribed text lines from here (to inject.rs)
pub async fn connect(
    host: &str,
    port: u16,
) -> Result<(mpsc::Sender<Vec<u8>>, mpsc::Receiver<String>)> {
    let addr = format!("{host}:{port}");
    let stream = TcpStream::connect(&addr).await?;
    tracing::info!("Connected to whisper-proxy at {addr}");

    let (reader, mut writer) = tokio::io::split(stream);

    // Channel for PCM audio → TCP write half.
    let (pcm_tx, mut pcm_rx) = mpsc::channel::<Vec<u8>>(128);
    // Channel for text lines ← TCP read half.
    let (text_tx, text_rx) = mpsc::channel::<String>(64);

    // Task A: PCM consumer — writes audio bytes to the server.
    tokio::spawn(async move {
        while let Some(chunk) = pcm_rx.recv().await {
            if writer.write_all(&chunk).await.is_err() {
                break;
            }
        }
        // When the pcm_rx channel closes (audio task dropped), shut down the
        // write half so the server knows the session is over.
        let _ = writer.shutdown().await;
    });

    // Task B: Text reader — reads transcription lines from the server.
    tokio::spawn(async move {
        let mut lines = BufReader::new(reader).lines();
        while let Ok(Some(line)) = lines.next_line().await {
            let trimmed = line.trim().to_string();
            if trimmed.is_empty() { continue; }
            // Parse "<start_ms> <end_ms> <text>" — extract just the text.
            let text = parse_text_line(&trimmed);
            if !text.is_empty() {
                let _ = text_tx.send(text).await;
            }
        }
    });

    Ok((pcm_tx, text_rx))
}

/// Extract the transcription text from a server line.
///
/// The server emits lines like "1234 5678 Hello, world."
/// We skip the two timestamp tokens and return the rest.
fn parse_text_line(line: &str) -> String {
    let mut parts = line.splitn(3, ' ');
    parts.next(); // start_ms
    parts.next(); // end_ms
    parts.next().unwrap_or("").trim().to_string()
}
```

---

### 5.5 `strisper-wayland/src/inject.rs`

**Purpose:** Receive text strings and type them into whatever Wayland window
currently has keyboard focus, using `ydotool` (primary) or `wtype` (fallback).

**Why `ydotool`?**  On Wayland, no application can send keystrokes to another
application — this is by design.  `ydotool` works around this by writing
synthetic input events directly to the kernel via `/dev/uinput`, which then
looks like a real keyboard to the compositor.  This is the same mechanism
TalkType uses (see `app.py:_type_text_raw`).

**Why `wtype` as fallback?**  `wtype` uses the
`zwp_virtual_keyboard_v1` Wayland protocol, which is supported by wlroots
compositors (Sway, Hyprland) but **not** by GNOME (which requires `ydotool`).

**Why pipe text via stdin rather than a shell argument?**  Passing arbitrary
text as a command argument can break on special characters (`!`, `"`, etc.).
`ydotool type -f -` reads from stdin, which is safe for all Unicode text.

**Auto-spacing:** The server's transcription lines arrive without leading spaces
between utterances.  If the previous utterance did not end with punctuation, we
prepend a single space (matching the Emacs client's `string-replace "  " " "`
normalization and TalkType's `auto_space` option).

```rust
// src/inject.rs
use anyhow::Result;
use std::process::Stdio;
use tokio::process::Command;
use tokio::sync::mpsc;
use tokio::io::AsyncWriteExt;

#[derive(Debug, Clone, Copy, PartialEq)]
pub enum Method { Auto, Ydotool, Wtype }

impl Method {
    pub fn from_str(s: &str) -> Self {
        match s.to_lowercase().as_str() {
            "ydotool" => Self::Ydotool,
            "wtype"   => Self::Wtype,
            _         => Self::Auto,
        }
    }
}

/// Detect which tool is available.
fn resolve_method(preferred: Method) -> Method {
    match preferred {
        Method::Ydotool => Method::Ydotool,
        Method::Wtype   => Method::Wtype,
        Method::Auto    => {
            // Prefer ydotool; fall back to wtype.
            if which("ydotool") { Method::Ydotool }
            else if which("wtype") { Method::Wtype }
            else { Method::Auto } // will fail gracefully at injection time
        }
    }
}

fn which(tool: &str) -> bool {
    std::process::Command::new("which")
        .arg(tool)
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
        .map(|s| s.success())
        .unwrap_or(false)
}

/// Run the injection loop: read text from `rx`, inject each line.
///
/// Prepends a space between utterances that don't end with punctuation
/// (auto-spacing).
pub async fn run_injection_loop(
    mut rx: mpsc::Receiver<String>,
    method: Method,
    delay_ms: u32,
) {
    let resolved = resolve_method(method);
    let mut last_ended_with_punct = true; // true = don't prepend space at start

    while let Some(text) = rx.recv().await {
        if text.is_empty() { continue; }

        let inject_text = if last_ended_with_punct {
            text.clone()
        } else {
            format!(" {text}")
        };

        last_ended_with_punct = text
            .trim_end()
            .chars()
            .last()
            .map(|c| ".!?,;:".contains(c))
            .unwrap_or(false);

        if let Err(e) = inject(&inject_text, resolved, delay_ms).await {
            tracing::error!("text injection failed: {e}");
        }
    }
}

async fn inject(text: &str, method: Method, delay_ms: u32) -> Result<()> {
    match method {
        Method::Ydotool => inject_ydotool(text, delay_ms).await,
        Method::Wtype   => inject_wtype(text).await,
        Method::Auto    => {
            tracing::warn!("no injection tool found (ydotool/wtype)");
            Ok(())
        }
    }
}

async fn inject_ydotool(text: &str, delay_ms: u32) -> Result<()> {
    // ydotool type -d <delay> -H <delay> -f -
    //   -d  delay between key-down and key-up events (ms)
    //   -H  delay between consecutive keys (ms)
    //   -f -  read text from stdin (safe with special chars)
    let delay = delay_ms.clamp(5, 50).to_string();
    let mut child = Command::new("ydotool")
        .args(["type", "-d", &delay, "-H", &delay, "-f", "-"])
        .stdin(Stdio::piped())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()?;

    if let Some(mut stdin) = child.stdin.take() {
        stdin.write_all(text.as_bytes()).await?;
    }
    child.wait().await?;
    Ok(())
}

async fn inject_wtype(text: &str) -> Result<()> {
    // wtype -- <text>   (-- prevents interpretation of leading dashes)
    Command::new("wtype")
        .arg("--")
        .arg(text)
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
        .await?;
    Ok(())
}
```

---

### 5.6 `strisper-wayland/src/hotkey.rs`

**Purpose:** Fallback global-hotkey implementation using the Linux `evdev`
subsystem, for users who are not on GNOME.  Requires the user to be in the
`input` group.

**How it works:** We enumerate all `/dev/input/event*` keyboards, read events
from them in a tokio task, track which modifier keys are currently held, and
fire the toggle command when the configured key combination is detected.  The
keyboard is **not** grabbed (unlike TalkType's hold-to-talk mode), so the
hotkey still reaches the focused application — but we detect it first and act
on it.

**Parsing the key combo:** Accept strings like `"Ctrl+Shift+F9"`.  Split on
`+`, classify each part as a modifier (Ctrl, Shift, Alt, Super) or the main
key (F1–F12, a–z), and map to `evdev::Key` constants.

```rust
// src/hotkey.rs
use evdev::{Device, EventType, Key};
use tokio::sync::mpsc;
use std::collections::HashSet;
use anyhow::{anyhow, Result};

/// Parse "Ctrl+Shift+F9" into (set of modifier Keys, main Key).
pub fn parse_combo(combo: &str) -> Result<(HashSet<Key>, Key)> {
    let parts: Vec<&str> = combo.split('+').map(str::trim).collect();
    if parts.len() < 2 {
        return Err(anyhow!("hotkey must include at least one modifier, e.g. Ctrl+F9"));
    }

    let mut modifiers = HashSet::new();
    let main_str = parts.last().unwrap();

    for part in &parts[..parts.len() - 1] {
        match part.to_uppercase().as_str() {
            "CTRL"  | "CONTROL" => { modifiers.insert(Key::KEY_LEFTCTRL);
                                      modifiers.insert(Key::KEY_RIGHTCTRL); }
            "SHIFT"             => { modifiers.insert(Key::KEY_LEFTSHIFT);
                                      modifiers.insert(Key::KEY_RIGHTSHIFT); }
            "ALT"               => { modifiers.insert(Key::KEY_LEFTALT);
                                      modifiers.insert(Key::KEY_RIGHTALT); }
            "SUPER" | "META"    => { modifiers.insert(Key::KEY_LEFTMETA);
                                      modifiers.insert(Key::KEY_RIGHTMETA); }
            other => return Err(anyhow!("unknown modifier: {other}")),
        }
    }

    let main_key = parse_key(main_str)
        .ok_or_else(|| anyhow!("unknown key: {main_str}"))?;

    Ok((modifiers, main_key))
}

fn parse_key(name: &str) -> Option<Key> {
    let upper = name.to_uppercase();
    // F1–F12
    if let Some(n) = upper.strip_prefix('F') {
        if let Ok(num) = n.parse::<u8>() {
            return match num {
                1  => Some(Key::KEY_F1),  2  => Some(Key::KEY_F2),
                3  => Some(Key::KEY_F3),  4  => Some(Key::KEY_F4),
                5  => Some(Key::KEY_F5),  6  => Some(Key::KEY_F6),
                7  => Some(Key::KEY_F7),  8  => Some(Key::KEY_F8),
                9  => Some(Key::KEY_F9),  10 => Some(Key::KEY_F10),
                11 => Some(Key::KEY_F11), 12 => Some(Key::KEY_F12),
                _ => None,
            };
        }
    }
    // Single letter a–z
    if upper.len() == 1 {
        let c = upper.chars().next().unwrap();
        if ('A'..='Z').contains(&c) {
            let key_name = format!("KEY_{c}");
            // evdev::Key has string parsing via the Key::from_str alternative —
            // for brevity, build a lookup from the known set.
            return key_from_letter(c);
        }
    }
    None
}

fn key_from_letter(c: char) -> Option<Key> {
    match c {
        'A' => Some(Key::KEY_A), 'B' => Some(Key::KEY_B), 'C' => Some(Key::KEY_C),
        'D' => Some(Key::KEY_D), 'E' => Some(Key::KEY_E), 'F' => Some(Key::KEY_F),
        'G' => Some(Key::KEY_G), 'H' => Some(Key::KEY_H), 'I' => Some(Key::KEY_I),
        'J' => Some(Key::KEY_J), 'K' => Some(Key::KEY_K), 'L' => Some(Key::KEY_L),
        'M' => Some(Key::KEY_M), 'N' => Some(Key::KEY_N), 'O' => Some(Key::KEY_O),
        'P' => Some(Key::KEY_P), 'Q' => Some(Key::KEY_Q), 'R' => Some(Key::KEY_R),
        'S' => Some(Key::KEY_S), 'T' => Some(Key::KEY_T), 'U' => Some(Key::KEY_U),
        'V' => Some(Key::KEY_V), 'W' => Some(Key::KEY_W), 'X' => Some(Key::KEY_X),
        'Y' => Some(Key::KEY_Y), 'Z' => Some(Key::KEY_Z),
        _ => None,
    }
}

/// Start a background evdev hotkey listener.
///
/// Returns a receiver that fires `()` each time the key combo is pressed.
/// Opens all keyboard devices it can find in /dev/input/.
pub async fn start_listener(combo: &str) -> Result<mpsc::Receiver<()>> {
    let (required_mods, main_key) = parse_combo(combo)?;
    let (tx, rx) = mpsc::channel::<()>(4);

    // Find all keyboard devices.
    let devices: Vec<Device> = evdev::enumerate()
        .filter_map(|(_, dev)| {
            // A keyboard has EV_KEY capability and KEY_SPACE (avoids mice etc.)
            if dev.supported_keys()
                  .map(|k| k.contains(Key::KEY_SPACE))
                  .unwrap_or(false)
            {
                Some(dev)
            } else {
                None
            }
        })
        .collect();

    if devices.is_empty() {
        tracing::warn!(
            "evdev hotkey: no keyboard devices found. \
             Ensure you are in the 'input' group: sudo usermod -aG input $USER"
        );
    }

    for device in devices {
        let tx = tx.clone();
        let required_mods = required_mods.clone();

        // Convert to async via a blocking thread (evdev is synchronous).
        tokio::task::spawn_blocking(move || {
            let mut device = device;
            let mut held: HashSet<Key> = HashSet::new();

            loop {
                match device.fetch_events() {
                    Ok(events) => {
                        for ev in events {
                            if ev.event_type() != EventType::KEY { continue; }
                            let key = Key::new(ev.code());
                            match ev.value() {
                                1 /* key down */ => { held.insert(key); }
                                0 /* key up   */ => { held.remove(&key); }
                                _ => {}
                            }
                            // Fire when the main key is pressed with all modifiers held.
                            if ev.value() == 1 && key == main_key {
                                let mods_ok = required_mods.iter().any(|m| held.contains(m));
                                // Actually check each logical modifier pair:
                                // at least one key from each modifier must be held.
                                // (required_mods contains both left and right variants)
                                let mods_ok = {
                                    // Group by modifier identity.
                                    // Simplification: we inserted both left+right for each
                                    // modifier, so "at least one of these is held" is correct.
                                    // But we need ALL modifiers, not just any one.
                                    // Use a more careful check:
                                    let ctrl_ok  = !required_mods.contains(&Key::KEY_LEFTCTRL)
                                        || held.contains(&Key::KEY_LEFTCTRL)
                                        || held.contains(&Key::KEY_RIGHTCTRL);
                                    let shift_ok = !required_mods.contains(&Key::KEY_LEFTSHIFT)
                                        || held.contains(&Key::KEY_LEFTSHIFT)
                                        || held.contains(&Key::KEY_RIGHTSHIFT);
                                    let alt_ok   = !required_mods.contains(&Key::KEY_LEFTALT)
                                        || held.contains(&Key::KEY_LEFTALT)
                                        || held.contains(&Key::KEY_RIGHTALT);
                                    let super_ok = !required_mods.contains(&Key::KEY_LEFTMETA)
                                        || held.contains(&Key::KEY_LEFTMETA)
                                        || held.contains(&Key::KEY_RIGHTMETA);
                                    ctrl_ok && shift_ok && alt_ok && super_ok
                                };
                                if mods_ok {
                                    let _ = tx.blocking_send(());
                                }
                            }
                        }
                    }
                    Err(e) => {
                        tracing::warn!("evdev read error: {e}");
                        break;
                    }
                }
            }
        });
    }

    Ok(rx)
}
```

---

### 5.7 `strisper-wayland/src/dbus.rs`

**Purpose:** Expose a D-Bus service on the session bus so the GNOME Shell
extension (and other tools, e.g. `busctl`) can call `ToggleRecording()` and
receive `RecordingStateChanged` signals.

**Why zbus?**  It is the de-facto async D-Bus library for Rust, uses
derive macros to reduce boilerplate, and integrates natively with Tokio.

**Interface name:** `io.github.bjodah.StrisperWayland`
**Bus name:**       `io.github.bjodah.StrisperWayland`
**Object path:**    `/io/github/bjodah/StrisperWayland`

The interface is intentionally minimal: just enough for the GNOME extension to
toggle recording and display the panel indicator state.

```rust
// src/dbus.rs
use std::sync::Arc;
use tokio::sync::{mpsc, RwLock};
use zbus::{interface, SignalContext};
use anyhow::Result;

#[derive(Debug, Default)]
pub struct AppState {
    pub is_recording: bool,
}

/// Command sent from the D-Bus interface to the main recording loop.
#[derive(Debug)]
pub enum Command {
    Toggle,
    Start,
    Stop,
}

pub struct StrisperInterface {
    pub state:  Arc<RwLock<AppState>>,
    pub cmd_tx: mpsc::Sender<Command>,
}

#[interface(name = "io.github.bjodah.StrisperWayland")]
impl StrisperInterface {
    async fn toggle_recording(&self) -> zbus::fdo::Result<()> {
        self.cmd_tx.send(Command::Toggle).await.ok();
        Ok(())
    }

    async fn start_recording(&self) -> zbus::fdo::Result<()> {
        self.cmd_tx.send(Command::Start).await.ok();
        Ok(())
    }

    async fn stop_recording(&self) -> zbus::fdo::Result<()> {
        self.cmd_tx.send(Command::Stop).await.ok();
        Ok(())
    }

    async fn is_recording(&self) -> bool {
        self.state.read().await.is_recording
    }

    // ── Signals ──────────────────────────────────────────────────────────────

    #[zbus(signal)]
    pub async fn recording_state_changed(
        ctx: &SignalContext<'_>,
        is_recording: bool,
    ) -> zbus::Result<()>;

    #[zbus(signal)]
    pub async fn transcription_received(
        ctx: &SignalContext<'_>,
        text: &str,
    ) -> zbus::Result<()>;
}

/// Register the service on the session bus.
///
/// Returns the `SignalContext` the caller needs to emit signals, plus a
/// background join handle (keep it alive for the lifetime of the process).
pub async fn register(
    state: Arc<RwLock<AppState>>,
    cmd_tx: mpsc::Sender<Command>,
) -> Result<(zbus::Connection, zbus::object_server::InterfaceRef<StrisperInterface>)> {
    let iface = StrisperInterface { state, cmd_tx };

    let conn = zbus::connection::Builder::session()?
        .name("io.github.bjodah.StrisperWayland")?
        .serve_at("/io/github/bjodah/StrisperWayland", iface)?
        .build()
        .await?;

    let iface_ref = conn
        .object_server()
        .interface::<_, StrisperInterface>("/io/github/bjodah/StrisperWayland")
        .await?;

    Ok((conn, iface_ref))
}
```

---

### 5.8 `strisper-wayland/src/main.rs`

**Purpose:** Entry point.  Parses CLI arguments, loads config, starts all
background tasks, and runs the main command loop that toggles recording on/off.

**CLI flags** (all optional; override corresponding config-file values):

| Flag | Config key | Description |
|------|-----------|-------------|
| `--host` | `server.host` | whisper-proxy hostname |
| `--port` | `server.port` | whisper-proxy port |
| `--hotkey` | `hotkey.key` | evdev fallback hotkey combo |
| `--device` | `audio.device` | mic device substring |
| `--inject` | `inject.method` | `auto` \| `ydotool` \| `wtype` |
| `--delay` | `inject.delay_ms` | keystroke delay in ms |
| `--no-evdev` | — | disable evdev even on non-GNOME |

**Main loop logic:**

```
initial state: not recording
commands arrive via two channels:
  - dbus_cmd_rx  (from the GNOME extension / busctl)
  - hotkey_rx    (from evdev, if enabled)

on Command::Toggle:
  if not recording → start_session()
  if recording     → stop_session()

start_session():
  1. spawn audio.rs::start()        → pcm channel
  2. spawn proxy.rs::connect()      → pcm_tx, text_rx
  3. wire: audio → pcm_tx
  4. spawn inject.rs::run_injection_loop(text_rx)
  5. emit D-Bus RecordingStateChanged(true)
  6. set state.is_recording = true

stop_session():
  1. drop audio handle (stops mic, closes pcm channel)
  2. proxy tasks drain naturally when pcm channel closes
  3. inject task drains text_rx then exits
  4. emit D-Bus RecordingStateChanged(false)
  5. set state.is_recording = false
```

```rust
// src/main.rs
mod audio; mod config; mod dbus; mod hotkey; mod inject; mod proxy;

use std::sync::Arc;
use tokio::sync::{mpsc, RwLock};
use clap::Parser;
use anyhow::Result;

#[derive(Parser, Debug)]
#[command(author, version, about = "Wayland speech-to-text client for whisper-proxy")]
struct Cli {
    #[arg(long)] host:     Option<String>,
    #[arg(long)] port:     Option<u16>,
    #[arg(long)] hotkey:   Option<String>,
    #[arg(long)] device:   Option<String>,
    #[arg(long)] inject:   Option<String>,
    #[arg(long)] delay:    Option<u32>,
    #[arg(long)] no_evdev: bool,
}

#[tokio::main]
async fn main() -> Result<()> {
    tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env()
                .unwrap_or_else(|_| "strisper_wayland=info".parse().unwrap())
        )
        .init();

    let cli = Cli::parse();
    let mut cfg = config::load()?;

    // CLI overrides.
    if let Some(h)  = cli.host   { cfg.server.host        = h; }
    if let Some(p)  = cli.port   { cfg.server.port        = p; }
    if let Some(k)  = cli.hotkey { cfg.hotkey.key         = k; }
    if let Some(d)  = cli.device { cfg.audio.device       = d; }
    if let Some(m)  = cli.inject { cfg.inject.method      = m; }
    if let Some(dl) = cli.delay  { cfg.inject.delay_ms    = dl; }

    let state  = Arc::new(RwLock::new(dbus::AppState::default()));
    let (cmd_tx, mut cmd_rx) = mpsc::channel::<dbus::Command>(8);

    // Start D-Bus service.
    let (_conn, iface_ref) = dbus::register(state.clone(), cmd_tx.clone()).await?;
    tracing::info!("D-Bus service registered: io.github.bjodah.StrisperWayland");

    // Start evdev fallback hotkey (unless disabled or on GNOME).
    let use_evdev = !cli.no_evdev;
    if use_evdev {
        let tx = cmd_tx.clone();
        let combo = cfg.hotkey.key.clone();
        match hotkey::start_listener(&combo).await {
            Ok(mut hotkey_rx) => {
                tokio::spawn(async move {
                    while hotkey_rx.recv().await.is_some() {
                        let _ = tx.send(dbus::Command::Toggle).await;
                    }
                });
                tracing::info!("evdev hotkey active: {combo}");
            }
            Err(e) => {
                tracing::warn!("evdev hotkey unavailable ({e}); use the GNOME extension instead");
            }
        }
    }

    // Recording session handles (None when not recording).
    let mut session: Option<SessionHandles> = None;
    let inject_method = inject::Method::from_str(&cfg.inject.method);
    let delay_ms      = cfg.inject.delay_ms;
    let host          = cfg.server.host.clone();
    let port          = cfg.server.port;
    let audio_device  = cfg.audio.device.clone();

    println!("strisper-wayland running. Press {hotkey} to toggle recording.",
        hotkey = cfg.hotkey.key);

    loop {
        let cmd = cmd_rx.recv().await;
        match cmd {
            Some(dbus::Command::Toggle) => {
                if session.is_none() {
                    session = Some(start_session(&host, port, &audio_device,
                                                 inject_method, delay_ms).await?);
                    set_recording(&state, &iface_ref, true).await;
                } else {
                    session = None; // drops all handles → tasks wind down
                    set_recording(&state, &iface_ref, false).await;
                }
            }
            Some(dbus::Command::Start) if session.is_none() => {
                session = Some(start_session(&host, port, &audio_device,
                                             inject_method, delay_ms).await?);
                set_recording(&state, &iface_ref, true).await;
            }
            Some(dbus::Command::Stop) if session.is_some() => {
                session = None;
                set_recording(&state, &iface_ref, false).await;
            }
            None => break, // channel closed → shutdown
            _ => {} // Start when already recording, Stop when not, etc.
        }
    }

    Ok(())
}

struct SessionHandles {
    _audio: audio::AudioHandle,
    // The proxy and inject tasks run to completion when channels close.
}

async fn start_session(
    host: &str, port: u16, audio_device: &str,
    inject_method: inject::Method, delay_ms: u32,
) -> Result<SessionHandles> {
    let (audio_handle, pcm_rx) = audio::start(audio_device)?;
    let (pcm_tx, text_rx)      = proxy::connect(host, port).await?;

    // Forward audio chunks from the mic to the proxy writer task.
    tokio::spawn(async move {
        let mut pcm_rx = pcm_rx;
        while let Some(chunk) = pcm_rx.recv().await {
            if pcm_tx.send(chunk).await.is_err() { break; }
        }
    });

    // Inject text from the proxy into the focused window.
    tokio::spawn(inject::run_injection_loop(text_rx, inject_method, delay_ms));

    Ok(SessionHandles { _audio: audio_handle })
}

async fn set_recording(
    state:     &Arc<RwLock<dbus::AppState>>,
    iface_ref: &zbus::object_server::InterfaceRef<dbus::StrisperInterface>,
    recording: bool,
) {
    state.write().await.is_recording = recording;
    let ctx = iface_ref.signal_context();
    let _ = dbus::StrisperInterface::recording_state_changed(ctx, recording).await;
    let emoji = if recording { "🎙️  Recording…" } else { "⏹  Stopped" };
    println!("{emoji}");
}
```

---

## 6. GNOME Shell Extension

### 6.1 `gnome-extension/strisper@whisper-streaming/metadata.json`

**Purpose:** Identifies the extension to GNOME Shell.  The `uuid` field must
match the directory name exactly.  The `settings-schema` field tells GNOME where
to look for the GSettings schema that stores the keybinding.

```json
{
  "name": "Strisper Wayland",
  "description": "Global shortcut and panel indicator for strisper-wayland speech-to-text",
  "uuid": "strisper@whisper-streaming",
  "version": 1,
  "shell-version": ["45", "46", "47", "48"],
  "url": "https://github.com/bjodah/whisper_streaming",
  "settings-schema": "org.gnome.shell.extensions.strisper-wayland"
}
```

---

### 6.2 `gnome-extension/strisper@whisper-streaming/schemas/org.gnome.shell.extensions.strisper-wayland.gschema.xml`

**Purpose:** Declares the GSettings key that stores the keybinding.  GNOME's
`Main.wm.addKeybinding()` API requires the keybinding to live in GSettings —
it cannot accept a hard-coded string at runtime.  This is what makes the
shortcut appear in GNOME's keyboard settings and lets the user change it there.

**Default value:** `['<Control><Shift>F9']`  (GNOME's keybinding notation uses
angle-bracket modifier names and no `+` signs).

```xml
<?xml version="1.0" encoding="UTF-8"?>
<schemalist>
  <schema id="org.gnome.shell.extensions.strisper-wayland"
          path="/org/gnome/shell/extensions/strisper-wayland/">

    <key name="toggle-recording" type="as">
      <default>['&lt;Control&gt;&lt;Shift&gt;F9']</default>
      <summary>Toggle recording keybinding</summary>
      <description>
        Global keyboard shortcut to start/stop speech-to-text recording.
        Change in GNOME Settings → Keyboard → Custom Shortcuts.
      </description>
    </key>

  </schema>
</schemalist>
```

The schema must be compiled before the extension is loaded:

```bash
glib-compile-schemas gnome-extension/strisper@whisper-streaming/schemas/
```

The `install.sh` script does this automatically.

---

### 6.3 `gnome-extension/strisper@whisper-streaming/extension.js`

**Purpose:** The extension's main logic.  Runs inside the GNOME Shell process.

**What it does:**
1. Creates a panel button (microphone icon) in the top bar.
2. Registers the `toggle-recording` GSettings keybinding with
   `Main.wm.addKeybinding()`.  When pressed, calls `ToggleRecording()` on the
   `strisper-wayland` D-Bus service.
3. Connects to the D-Bus service and listens for `RecordingStateChanged` signals
   to update the panel icon (red when recording).
4. Watches the D-Bus name so the indicator hides when `strisper-wayland` is not
   running.

**D-Bus interaction:** Uses `Gio.DBusProxy.makeProxyWrapper()` — the same
pattern as TalkType's `extension.js` — which auto-generates async remote method
calls (e.g. `this._proxy.ToggleRecordingRemote()`).

```javascript
// extension.js
import GLib from 'gi://GLib';
import GObject from 'gi://GObject';
import St from 'gi://St';
import Gio from 'gi://Gio';
import Meta from 'gi://Meta';
import Shell from 'gi://Shell';
import * as Main from 'resource:///org/gnome/shell/ui/main.js';
import * as PanelMenu from 'resource:///org/gnome/shell/ui/panelMenu.js';
import * as PopupMenu from 'resource:///org/gnome/shell/ui/popupMenu.js';
import { Extension } from 'resource:///org/gnome/shell/extensions/extension.js';

// D-Bus interface descriptor (must match dbus.rs).
const IFACE_XML = `
<node>
  <interface name="io.github.bjodah.StrisperWayland">
    <method name="ToggleRecording"/>
    <method name="StartRecording"/>
    <method name="StopRecording"/>
    <method name="IsRecording">
      <arg type="b" direction="out" name="recording"/>
    </method>
    <signal name="RecordingStateChanged">
      <arg type="b" name="is_recording"/>
    </signal>
    <signal name="TranscriptionReceived">
      <arg type="s" name="text"/>
    </signal>
  </interface>
</node>`;

const StrisperProxy = Gio.DBusProxy.makeProxyWrapper(IFACE_XML);

// Panel indicator widget.
const StrisperIndicator = GObject.registerClass(
class StrisperIndicator extends PanelMenu.Button {
    _init(extension) {
        super._init(0.0, 'Strisper Wayland');
        this._ext = extension;
        this._isRecording = false;

        // Microphone icon.
        this._icon = new St.Icon({
            icon_name: 'audio-input-microphone-symbolic',
            style_class: 'system-status-icon',
        });
        this.add_child(this._icon);

        // Simple menu: toggle + quit.
        let toggleItem = new PopupMenu.PopupMenuItem('Toggle Recording');
        toggleItem.connect('activate', () => this._toggle());
        this.menu.addMenuItem(toggleItem);

        this.menu.addMenuItem(new PopupMenu.PopupSeparatorMenuItem());

        let quitItem = new PopupMenu.PopupMenuItem('Quit strisper-wayland');
        quitItem.connect('activate', () => {
            // Kill the process; systemd will restart it if configured.
            GLib.spawn_command_line_async('pkill -x strisper-wayland');
        });
        this.menu.addMenuItem(quitItem);

        this._connectDBus();
    }

    _connectDBus() {
        try {
            this._proxy = new StrisperProxy(
                Gio.DBus.session,
                'io.github.bjodah.StrisperWayland',
                '/io/github/bjodah/StrisperWayland'
            );

            this._proxy.connectSignal('RecordingStateChanged',
                (_proxy, _sender, [isRecording]) => {
                    this._isRecording = isRecording;
                    this._updateIcon();
                }
            );
        } catch (e) {
            console.error('Strisper: D-Bus connect failed:', e);
        }

        // Watch for the service appearing / disappearing.
        this._watcherId = Gio.DBus.session.watch_name(
            'io.github.bjodah.StrisperWayland',
            Gio.BusNameWatcherFlags.NONE,
            () => { this.show();  this._updateIcon(); },   // appeared
            () => { this.hide();  this._isRecording = false; } // vanished
        );

        // Start hidden; show when service appears.
        this.hide();
    }

    _toggle() {
        if (this._proxy)
            this._proxy.ToggleRecordingRemote();
    }

    _updateIcon() {
        this._icon.style = this._isRecording ? 'color: #ff4444;' : '';
    }

    destroy() {
        if (this._watcherId)
            Gio.DBus.session.unwatch_name(this._watcherId);
        super.destroy();
    }
});

// Extension class (GNOME 45+ ESM style).
export default class StrisperExtension extends Extension {
    enable() {
        this._indicator = new StrisperIndicator(this);
        Main.panel.addToStatusArea(this.uuid, this._indicator);

        // Register the global keybinding from GSettings.
        // The key 'toggle-recording' must exist in the extension's schema.
        Main.wm.addKeybinding(
            'toggle-recording',
            this.getSettings(),
            Meta.KeyBindingFlags.IGNORE_AUTOREPEAT,
            Shell.ActionMode.ALL,
            () => this._indicator._toggle()
        );
    }

    disable() {
        Main.wm.removeKeybinding('toggle-recording');
        if (this._indicator) {
            this._indicator.destroy();
            this._indicator = null;
        }
    }
}
```

---

### 6.4 `gnome-extension/strisper@whisper-streaming/stylesheet.css`

Minimal CSS.  The recording state is communicated via inline `style` in
`extension.js` for simplicity; this file is present because GNOME expects it.

```css
/* strisper-wayland GNOME Shell extension stylesheet */

/* No custom classes needed: icon colour is set via inline style. */
```

---

## 7. Supporting Files

### 7.1 `io.github.bjodah.StrisperWayland.xml`

**Purpose:** D-Bus introspection document.  Install to
`/usr/share/dbus-1/interfaces/` or `~/.local/share/dbus-1/interfaces/` so
that tools like `busctl` and `d-feet` display the interface correctly.  The
content must match `dbus.rs` exactly.

```xml
<!DOCTYPE node PUBLIC
  "-//freedesktop//DTD D-BUS Object Introspection 1.0//EN"
  "http://www.freedesktop.org/standards/dbus/1.0/introspect.dtd">
<node>
  <interface name="io.github.bjodah.StrisperWayland">

    <method name="ToggleRecording"/>
    <method name="StartRecording"/>
    <method name="StopRecording"/>

    <method name="IsRecording">
      <arg type="b" direction="out" name="recording"/>
    </method>

    <signal name="RecordingStateChanged">
      <arg type="b" name="is_recording"/>
    </signal>

    <signal name="TranscriptionReceived">
      <arg type="s" name="text"/>
    </signal>

  </interface>
</node>
```

---

### 7.2 `strisper-wayland.service`

**Purpose:** systemd user service so `strisper-wayland` starts automatically
with the GNOME session and restarts if it crashes.

**Why user service (not system service)?**  The app requires access to the
user's D-Bus session, audio devices (PipeWire/PulseAudio), and display.  System
services run before the user session and cannot access these.  User services
installed under `~/.config/systemd/user/` are started by systemd after the user
logs in and have full session access.

```ini
[Unit]
Description=Strisper Wayland speech-to-text client
Documentation=https://github.com/bjodah/whisper_streaming
# Wait for the graphical session so D-Bus session bus and audio are ready.
After=graphical-session.target

[Service]
Type=simple
ExecStart=%h/.local/bin/strisper-wayland
# Restart on failure with a 5-second cooldown.
Restart=on-failure
RestartSec=5
# Log to the journal (view with: journalctl --user -u strisper-wayland -f)
StandardOutput=journal
StandardError=journal

[Install]
# Activated when the user's graphical session reaches its target.
WantedBy=graphical-session.target
```

---

### 7.3 `strisper-wayland.desktop`

**Purpose:** XDG autostart entry.  An alternative to the systemd service for
users who prefer not to use systemd.  GNOME reads `~/.config/autostart/` and
launches matching `.desktop` entries when the session starts.

```ini
[Desktop Entry]
Type=Application
Name=Strisper Wayland
Comment=Speech-to-text client for whisper-proxy
Exec=strisper-wayland
Icon=audio-input-microphone
Categories=Utility;Accessibility;AudioVideo;
StartupNotify=false
NoDisplay=false
X-GNOME-Autostart-enabled=true
```

---

### 7.4 `install.sh`

**Purpose:** One-shot installer for developers and end-users.  Run from the
`wayland-client/` directory after cloning.

**What it does (in order):**

1. Checks that required build tools are present (`cargo`, `glib-compile-schemas`).
2. Checks that at least one injection tool is present (`ydotool` or `wtype`).
3. Builds the Rust binary in release mode.
4. Installs the binary to `~/.local/bin/`.
5. Installs and compiles the GNOME extension.
6. Installs the systemd user service.
7. Optionally installs the autostart `.desktop` file.
8. Enables the systemd service (`--now` starts it immediately).
9. Prints post-install instructions.

```bash
#!/usr/bin/env bash
# install.sh — install strisper-wayland (run from wayland-client/)
set -euo pipefail

BINARY_NAME="strisper-wayland"
EXTENSION_UUID="strisper@whisper-streaming"
EXTENSION_SRC="gnome-extension/${EXTENSION_UUID}"
EXTENSION_DST="${HOME}/.local/share/gnome-shell/extensions/${EXTENSION_UUID}"
SERVICE_NAME="${BINARY_NAME}.service"
SYSTEMD_USER_DIR="${HOME}/.config/systemd/user"
BIN_DIR="${HOME}/.local/bin"

# ── Helpers ──────────────────────────────────────────────────────────────────
need() { command -v "$1" &>/dev/null || { echo "ERROR: '$1' not found.  $2"; exit 1; }; }
info() { echo "[strisper] $*"; }

# ── Dependency checks ─────────────────────────────────────────────────────────
info "Checking dependencies..."
need cargo               "Install Rust: https://rustup.rs/"
need glib-compile-schemas "Install glib2-devel (Fedora) or libglib2.0-dev (Debian/Ubuntu)"

if ! command -v ydotool &>/dev/null && ! command -v wtype &>/dev/null; then
    echo "WARNING: Neither 'ydotool' nor 'wtype' found."
    echo "  Text injection will not work until you install one of them."
    echo "  Recommended: sudo apt install ydotool   or   sudo pacman -S ydotool"
fi

# ── Build ─────────────────────────────────────────────────────────────────────
info "Building Rust binary (this may take a few minutes on first run)..."
(cd strisper-wayland && cargo build --release)
info "Build complete."

# ── Install binary ────────────────────────────────────────────────────────────
mkdir -p "${BIN_DIR}"
install -m 755 "strisper-wayland/target/release/${BINARY_NAME}" "${BIN_DIR}/${BINARY_NAME}"
info "Installed binary to ${BIN_DIR}/${BINARY_NAME}"

# Make sure ~/.local/bin is on PATH.
if ! echo "$PATH" | tr ':' '\n' | grep -qx "${BIN_DIR}"; then
    echo "NOTE: Add ~/.local/bin to PATH:"
    echo "  echo 'export PATH=\"\$HOME/.local/bin:\$PATH\"' >> ~/.bashrc"
fi

# ── uinput / ydotool permissions ──────────────────────────────────────────────
if command -v ydotool &>/dev/null; then
    info "Setting up uinput permissions for ydotool..."

    # Install udev rule so the 'input' group can access /dev/uinput.
    UDEV_RULE='/etc/udev/rules.d/99-strisper-uinput.rules'
    RULE_CONTENT='KERNEL=="uinput", GROUP="input", MODE="0660", OPTIONS+="static_node=uinput"'
    if [ ! -f "${UDEV_RULE}" ]; then
        echo "${RULE_CONTENT}" | sudo tee "${UDEV_RULE}" > /dev/null
        sudo udevadm control --reload-rules
        sudo udevadm trigger
        info "Installed udev rule: ${UDEV_RULE}"
    fi

    # Add user to 'input' group.
    if ! id -nG | grep -qw input; then
        sudo usermod -aG input "${USER}"
        info "Added ${USER} to the 'input' group."
        echo "  You MUST log out and back in for this to take effect."
    fi

    # Enable ydotoold user service if available.
    if systemctl --user list-unit-files ydotoold.service &>/dev/null; then
        systemctl --user enable --now ydotoold.service || true
        info "ydotoold service enabled."
    else
        echo "NOTE: ydotoold service not found.  Start it manually or add it to autostart."
        echo "  ydotoold --socket-path \"\${XDG_RUNTIME_DIR}/.ydotool_socket\" &"
    fi
fi

# ── GNOME extension ───────────────────────────────────────────────────────────
info "Installing GNOME Shell extension..."
mkdir -p "${EXTENSION_DST}"
cp -r "${EXTENSION_SRC}/." "${EXTENSION_DST}/"

# Compile GSettings schema.
glib-compile-schemas "${EXTENSION_DST}/schemas/"
info "GSettings schema compiled."

# ── systemd user service ──────────────────────────────────────────────────────
info "Installing systemd user service..."
mkdir -p "${SYSTEMD_USER_DIR}"
cp "${SERVICE_NAME}" "${SYSTEMD_USER_DIR}/${SERVICE_NAME}"
systemctl --user daemon-reload
systemctl --user enable --now "${SERVICE_NAME}"
info "Service enabled and started."

# ── Post-install instructions ─────────────────────────────────────────────────
echo ""
echo "════════════════════════════════════════════════════════"
echo " strisper-wayland installed successfully!"
echo "════════════════════════════════════════════════════════"
echo ""
echo " 1. Enable the GNOME Shell extension:"
echo "      gnome-extensions enable ${EXTENSION_UUID}"
echo "    OR open 'Extensions' app and enable 'Strisper Wayland'."
echo ""
echo " 2. Make sure whisper-proxy is running:"
echo "      ./bin/whisper-proxy --port 43007 --upstream-base-url <url>"
echo ""
echo " 3. Press Ctrl+Shift+F9 to toggle recording."
echo "    To change the shortcut: GNOME Settings → Keyboard → Keyboard Shortcuts."
echo ""
echo " 4. Check service logs:"
echo "      journalctl --user -u strisper-wayland -f"
echo ""
if ! id -nG | grep -qw input; then
    echo " ⚠  You were added to the 'input' group.  Log out and back in."
    echo ""
fi
```

Make it executable: `chmod +x install.sh`.

---

### 7.5 `README.md` (in `wayland-client/`)

The README should cover:

1. **Prerequisites** — OS requirements (Linux, Wayland session, GNOME 45+),
   build tools (Rust toolchain), runtime tools (`ydotool`/`wtype`).
2. **Quick install** — `./install.sh` and the two post-install steps.
3. **Configuration** — Annotated `~/.config/strisper-wayland/config.toml`
   example.
4. **Non-GNOME Wayland** — How to use the evdev fallback:
   - Add yourself to the `input` group.
   - Pass `--no-evdev false` (evdev is enabled by default).
   - The panel indicator is GNOME-only; use `busctl` to toggle on other DEs.
5. **Troubleshooting** — Common problems (no audio, no text injection, hotkey
   not working) with diagnostic commands.
6. **Architecture** — Brief description of the module relationship (mirrors
   section 3 of this plan).

---

## 8. Step-by-Step Implementation Order

Follow these steps in order.  Each step is independently testable before moving
to the next.

### Step 1 — Scaffold the Rust crate

```bash
cd wayland-client
cargo new strisper-wayland
```

Replace the generated `Cargo.toml` with the one in §5.1.  Run `cargo check` to
verify all crate names resolve.

### Step 2 — Implement `config.rs`

Copy the code from §5.2.  Add `dirs = "5"` to `Cargo.toml`.  Test:

```bash
cargo run -- --help
# Should print usage without error.
# ~/.config/strisper-wayland/config.toml should be created with defaults.
```

### Step 3 — Implement `proxy.rs` and test against the server

Copy the code from §5.4.  Write a temporary `main.rs` that:
1. Calls `proxy::connect("localhost", 43007)`.
2. Opens `/dev/stdin` and reads raw bytes, forwarding them to `pcm_tx`.
3. Prints text lines from `text_rx`.

Test it with:

```bash
arecord -f S16_LE -c1 -r 16000 -t raw | cargo run
```

You should see transcription lines appearing in the terminal — exactly what the
Emacs client produces.  This validates the wire protocol before adding audio
capture complexity.

### Step 4 — Implement `audio.rs`

Copy the code from §5.3.  Integrate with `main.rs`: replace the `arecord |
stdin` hack with `audio::start()`.  Run without arguments — speech should
appear in the terminal.

### Step 5 — Implement `inject.rs`

Copy the code from §5.5.  Test in isolation:

```bash
# Focus a text editor, then run:
busctl --user call io.github.bjodah.StrisperWayland \
    /io/github/bjodah/StrisperWayland \
    io.github.bjodah.StrisperWayland ToggleRecording
```

Or test `ydotool` directly:
```bash
echo "hello world" | ydotool type -d 12 -H 12 -f -
```

### Step 6 — Implement `dbus.rs`

Copy the code from §5.7.  Wire into `main.rs` (§5.8).  Test with `busctl`:

```bash
busctl --user call io.github.bjodah.StrisperWayland \
    /io/github/bjodah/StrisperWayland \
    io.github.bjodah.StrisperWayland ToggleRecording
# Should start recording; calling again should stop it.
```

Monitor signals:

```bash
busctl --user monitor io.github.bjodah.StrisperWayland
```

### Step 7 — Implement `hotkey.rs` (optional, non-GNOME)

Copy the code from §5.6.  Add the listener call to `main.rs`.  Test by pressing
`Ctrl+Shift+F9` in a terminal (if you are in the `input` group).

### Step 8 — Create the GNOME Extension

1. Create all files described in §6.
2. Compile the schema: `glib-compile-schemas gnome-extension/strisper@whisper-streaming/schemas/`
3. Install the extension:
   ```bash
   cp -r gnome-extension/strisper@whisper-streaming \
       ~/.local/share/gnome-shell/extensions/
   gnome-extensions enable strisper@whisper-streaming
   ```
4. Restart GNOME Shell: `Alt+F2`, type `r`, Enter (X11 only) — or log out/in on
   Wayland.
5. The microphone icon should appear in the top bar.  Press `Ctrl+Shift+F9` — it
   should turn red.

### Step 9 — Write `install.sh` and test end-to-end

Copy the script from §7.4.  Run it on a clean system (or a VM) and verify all
steps succeed.

### Step 10 — Write `README.md`

Document the information described in §7.5.

---

## 9. Testing Checklist

| Test | How |
|------|-----|
| Config created on first run | Delete `~/.config/strisper-wayland/config.toml`; run binary; file should appear |
| TCP protocol correct | `arecord … | nc localhost 43007` produces lines; our binary should produce the same |
| Audio resampling | Disconnect default mic; configure a USB mic that only supports 48 kHz; speech should still be transcribed |
| Text injection (ydotool) | Open gedit, toggle recording, speak → text appears |
| Text injection (wtype) | On Sway/Hyprland, same test with `--inject wtype` |
| Auto-spacing | Two utterances separated by a pause → single space between them |
| D-Bus toggle | `busctl call … ToggleRecording` starts/stops recording |
| D-Bus signal | `busctl monitor` shows `RecordingStateChanged` signal on toggle |
| GNOME extension keybinding | `Ctrl+Shift+F9` triggers toggle |
| GNOME extension icon | Icon turns red on recording start, returns to normal on stop |
| GNOME extension hide/show | Kill `strisper-wayland`; icon should disappear. Start it; icon should reappear |
| systemd service autostart | Log out, log back in; `systemctl --user status strisper-wayland` should be active |
| evdev fallback | On Sway, `--no-evdev false`; key press toggles recording |

---

## 10. Known Limitations and Future Work

- **Text injection on GNOME < 45:** `wtype` does not work on GNOME
  (GNOME does not implement `zwp_virtual_keyboard_v1`).  `ydotool` with uinput
  is the only option.  Document this prominently.
- **No AT-SPI:** TalkType uses AT-SPI to detect the focused widget and insert
  text via accessibility APIs (faster than ydotool on some apps).  This is a
  significant complexity; leave it as future work.
- **No recording indicator:** TalkType shows a floating on-screen timer.
  A minimal version could use `notify-send` for desktop notifications.  A rich
  version would need a GTK4 window.  Leave as future work.
- **No auto-punctuation / smart quotes:** TalkType does normalisation
  (`normalize.py`).  The server already handles VAD; punctuation normalisation
  can be added to `inject.rs` later.
- **KDE global shortcut:** KDE has its own keybinding system
  (`org.kde.kglobalaccel`).  A KDE Plasma widget (analogous to the GNOME
  extension) would be needed.  For now, evdev is the only hotkey mechanism on
  KDE.
