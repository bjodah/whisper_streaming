# strisper-wayland

**Wayland speech-to-text client for [whisper_streaming](https://github.com/bjodah/whisper_streaming).**

Records audio from your microphone, streams it over TCP to a running
`whisper_streaming` server, and injects each transcript as keystrokes directly
into the focused application — entirely on Wayland without an X11 bridge.

---

## Features

- Real-time transcription via `whisper_streaming` WebSocket/TCP server
- Wayland-native keystroke injection (ydotool or wtype, auto-detected)
- GNOME Shell extension with panel indicator and GSettings hotkey
- evdev-based global hotkey fallback for non-GNOME compositors (Sway, Hyprland, …)
- D-Bus interface (`io.github.bjodah.StrisperWayland`) for scripting
- 48 kHz / 44.1 kHz audio downsampled to 16 kHz automatically (rubato)
- Single statically-linked binary, minimal runtime dependencies

---

## Prerequisites

| Package | Purpose |
|---|---|
| `libasound2-dev`, `pkg-config` | ALSA audio (build-time) |
| `ydotool` + `ydotool.service` | Preferred keystroke injection on Wayland |
| `wtype` | Alternative injection (Sway / wlroots compositors) |
| Rust ≥ 1.70 | Build tool (`rustup install stable`) |

Install build dependencies on Ubuntu/Debian:

```bash
sudo apt install libasound2-dev pkg-config build-essential ydotool wtype
sudo systemctl enable --now ydotool
sudo usermod -aG input $USER   # log out and back in afterwards
```

---

## Quick Start

### 1. Build & install

```bash
cd wayland-client
./install.sh --gnome          # GNOME users
./install.sh                  # non-GNOME (no extension)
```

The binary is placed in `~/.local/bin/strisper-wayland`.

### 2. Start whisper_streaming server

```bash
# Example: GPU-accelerated server on localhost:43007
python whisper_online_server.py --backend faster-whisper \
    --model medium --language en --port 43007
```

### 3. Toggle recording

- **GNOME**: press `Ctrl+Shift+F9` (configurable via GSettings / extension prefs)
- **non-GNOME**: same key, handled via evdev
- **CLI / scripts**: `strisper-wayland toggle`  
  or `busctl --user call io.github.bjodah.StrisperWayland /io/github/bjodah/StrisperWayland io.github.bjodah.StrisperWayland ToggleRecording`

---

## Configuration

Default config path: `~/.config/strisper-wayland/config.toml`

Override with `--config /path/to/config.toml`.

```toml
[server]
host = "127.0.0.1"   # whisper_streaming server address
port = 43007

[audio]
device = ""          # empty = system default; e.g. "pulse" or "hw:0,0"

[inject]
method = "auto"      # "auto" | "ydotool" | "wtype"
delay_ms = 12        # milliseconds between key events

[hotkey]
key = "Ctrl+Shift+F9"   # used only on non-GNOME Wayland (evdev)
```

---

## GNOME Installation

```bash
./install.sh --gnome
gnome-extensions enable strisper@whisper-streaming
# Restart GNOME Shell: Alt+F2 → r → Enter  (X11) or log out/in (Wayland)
```

The extension adds a microphone icon to the panel. While recording the icon
turns red. Click it to open a menu with a **Toggle Recording** item.

### Changing the GNOME hotkey

```bash
gsettings set org.gnome.shell.extensions.strisper-wayland hotkey '<Control><Shift>F9'
```

---

## Non-GNOME Wayland (Sway, Hyprland, etc.)

On compositors other than GNOME, global hotkeys are captured via **evdev**
(reading directly from `/dev/input/event*`). This requires membership of the
`input` group:

```bash
sudo usermod -aG input $USER
# Log out and back in.
```

The hotkey is then read from `config.toml` (`[hotkey] key = "Ctrl+Shift+F9"`).

### evdev mode flags

| Flag | Behaviour |
|------|-----------|
| `--evdev auto` (default) | evdev on non-GNOME, D-Bus hotkey on GNOME |
| `--evdev on` | Force evdev even under GNOME (useful for debugging) |
| `--evdev off` | Disable evdev; hotkey via D-Bus / extension only |

---

## Autostart

### systemd user service

```bash
systemctl --user enable --now strisper-wayland
```

### Manual autostart (non-systemd)

Add `strisper-wayland &` to your compositor startup script.

---

## D-Bus Interface

Bus name: `io.github.bjodah.StrisperWayland`  
Object path: `/io/github/bjodah/StrisperWayland`

| Method / Signal / Property | Description |
|---|---|
| `StartRecording()` | Begin recording and streaming |
| `StopRecording()` | Stop recording |
| `ToggleRecording()` | Toggle state |
| `Recording` (property, bool) | Current recording state |
| `RecordingStateChanged(b)` (signal) | Emitted on state change |

Example:
```bash
busctl --user call io.github.bjodah.StrisperWayland \
    /io/github/bjodah/StrisperWayland \
    io.github.bjodah.StrisperWayland ToggleRecording
```

---

## Troubleshooting

### No audio captured

```bash
strisper-wayland --list-devices    # list available audio devices
strisper-wayland --config my.toml  # use specific device via config
```

### Keystroke injection not working (ydotool)

1. Check the service: `systemctl status ydotool`
2. Verify group membership: `groups | grep input`
3. Socket path: `ls -la /run/user/$UID/ydotool.sock`
4. Switch to wtype: set `method = "wtype"` in `[inject]`

### No global hotkey under wlroots compositors

evdev requires `/dev/input/event*` read permission:

```bash
sudo usermod -aG input $USER
# Then force evdev if GNOME_DESKTOP_SESSION_ID is set:
strisper-wayland --evdev on
```

### Connection refused (server)

Confirm `whisper_streaming` is running and listening:

```bash
nc -z 127.0.0.1 43007 && echo "server OK"
```

---

## Project Layout

```
wayland-client/
├── strisper-wayland/          # Rust crate
│   ├── Cargo.toml
│   ├── src/
│   │   ├── lib.rs             # Public module re-exports (for tests)
│   │   ├── main.rs            # CLI + async event loop
│   │   ├── audio.rs           # cpal capture
│   │   ├── config.rs          # TOML config
│   │   ├── dbus.rs            # zbus D-Bus service
│   │   ├── hotkey.rs          # evdev hotkey listener
│   │   ├── inject.rs          # ydotool / wtype injection
│   │   └── proxy.rs           # TCP proxy + rubato resampler
│   └── tests/
│       └── integration_test.rs
├── gnome-extension/
│   └── strisper@whisper-streaming/
│       ├── metadata.json
│       ├── extension.js
│       ├── stylesheet.css
│       └── schemas/
│           └── org.gnome.shell.extensions.strisper-wayland.gschema.xml
├── strisper-wayland.service   # systemd user unit
├── strisper-wayland.desktop   # XDG desktop entry
└── install.sh                 # Installer script
```

---

## License

Same as the parent project — see `../LICENSE`.
