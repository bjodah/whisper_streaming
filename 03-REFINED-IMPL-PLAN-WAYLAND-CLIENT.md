# Refined Implementation Plan: `strisper-wayland` for Ubuntu 26.04 / GNOME 50

This document replaces the parts of [02-IMPL-PLAN-WAYLAND-CLIENT.md](/home/ai-bot-bjodah/vc/whisper_streaming/02-IMPL-PLAN-WAYLAND-CLIENT.md) that are too optimistic, distro-ambiguous, or no longer accurate for the target environment.

The target machine for this review is:

- Ubuntu `26.04 LTS`
- GNOME Shell `50.1`
- Wayland session
- `gsettings get org.gnome.shell disable-extension-version-validation` returned `false`

That last point matters: a GNOME extension that only declares support through shell `48` should be treated as unsupported on this desktop.

## 1. Shortcomings Found in `02-IMPL-PLAN-WAYLAND-CLIENT.md`

These are the specific issues that justify a new plan instead of a small patch.

### 1.1 GNOME extension metadata is behind the actual target shell

The existing plan hard-codes:

```json
"shell-version": ["45", "46", "47", "48"]
```

That does not match GNOME `50.1`, which is the desktop we are targeting. On this machine, version validation is enabled, so the extension plan must explicitly account for GNOME `50`.

### 1.2 The Debian/Ubuntu package guidance is wrong or incomplete

The old installer text says Debian/Ubuntu users should install `libglib2.0-dev` for `glib-compile-schemas`.

On this Ubuntu 26.04 machine:

- `glib-compile-schemas` is already provided by `libglib2.0-bin`
- `pkg-config` was missing
- `libasound2-dev` was missing
- `ydotool` and `wtype` were missing

The Rust stack proposed in the old plan did not build cleanly until `pkg-config` and `libasound2-dev` were installed.

### 1.3 The audio capture plan is too narrow for real Linux devices

The old plan assumes:

- input is effectively `f32`
- the device can be forced to `1` channel
- only `48 kHz` needs resampling

The actual default input device on this machine reported:

- sample rate: `44100`
- sample format: `F32`
- channels: `2`

So the real implementation must:

- branch on `cpal::SampleFormat`
- accept the device's native channel count
- downmix to mono explicitly
- resample any non-`16000` rate, not just `48000`

### 1.4 Hotkey configurability is only half-designed

The prompt requires a configurable global shortcut.

The old plan splits shortcut configuration into two unrelated stores:

- GNOME: GSettings in the extension schema
- non-GNOME: TOML `hotkey.key`

That is acceptable, but the old plan over-promises by saying the extension shortcut will appear in GNOME Settings custom shortcuts. The document does not actually define:

- a `prefs.js` extension preferences UI
- a dedicated settings panel
- a concrete `gsettings` workflow for users

The refined plan must describe how the shortcut is changed in practice.

### 1.5 `ydotool` service handling is wrong for Ubuntu 26.04

The old plan looks for `ydotoold.service`.

On this system, the package ships:

- binary: `/usr/bin/ydotool`
- daemon: `/usr/bin/ydotoold`
- user unit: `ydotool.service`

The installer must enable and start `ydotool.service`, not `ydotoold.service`.

### 1.6 The "single static binary" claim is inaccurate

With the Rust audio approach proposed here, the binary is not "no dynamic dependencies beyond libc".

A stub built with the planned dependency set links to `libasound.so.2`. That is acceptable, but the plan should state it plainly.

### 1.7 The D-Bus contract contains a dead signal

The old plan defines `TranscriptionReceived`, but the described control flow never emits it. Either:

- remove it from v1, or
- add a real fan-out path that both injects text and emits the signal

For the first implementation, removing it is simpler.

### 1.8 The extension "Quit" action is wrong when systemd restart is enabled

The old extension menu uses:

```javascript
GLib.spawn_command_line_async('pkill -x strisper-wayland');
```

That conflicts with the proposed `Restart=on-failure` service model. Killing the process is not the same as stopping the service. The refined plan should either:

- remove the quit action in v1, or
- replace it with `systemctl --user stop strisper-wayland.service`

### 1.9 The CLI documentation contains an invalid example

The old README outline says:

```text
Pass `--no-evdev false`
```

That is not how a boolean presence flag works in `clap`. If `--no-evdev` exists, it disables evdev. If omitted, evdev remains enabled.

### 1.10 The old plan does not clearly separate GNOME from non-GNOME injection strategy

For Ubuntu GNOME:

- `ydotool` is the practical text injection path
- `wtype` is not a GNOME solution

For wlroots compositors:

- `wtype` is often the better default because it avoids `uinput` permission work

The method-selection rules should reflect that.

## 2. Verified Build and Runtime Prerequisites

These are the packages and tools that were actually needed or validated on this machine.

### 2.1 Required Ubuntu packages

For development and local installation:

```bash
sudo apt-get update
sudo apt-get install -y \
  cargo rustc \
  pkg-config libasound2-dev \
  libglib2.0-bin \
  ydotool wtype
```

Notes:

- `cargo` and `rustc` were already installed here, but they belong in the documented package list.
- `pkg-config` and `libasound2-dev` are required for `cpal` on Linux.
- `libglib2.0-bin` provides `glib-compile-schemas`.
- `ydotool` is required on GNOME.
- `wtype` is optional for GNOME, but worth installing for non-GNOME Wayland testing.

### 2.2 Tools already present on the reviewed desktop

Verified commands:

- `gnome-shell --version` -> `GNOME Shell 50.1`
- `glib-compile-schemas`
- `gnome-extensions`
- `busctl`
- `gdbus`
- `gjs`

### 2.3 `ydotool` packaging details on Ubuntu 26.04

Relevant facts observed locally:

- user service: `ydotool.service`
- daemon binary: `ydotoold`
- device node: `/dev/uinput`
- current user is **not** in the `input` group on this machine

The packaged README still instructs the user to join `input`:

```bash
sudo usermod -aG input "$USER"
```

That means the refined installation instructions must keep the logout/login warning. Even if the daemon starts on some machines without immediate failure, the plan should not assume that `uinput` access is already correct.

## 3. Independent Validation Performed

This review was not purely static. The following checks were run against the target desktop.

### 3.1 Rust dependency stack

A disposable Cargo project was created with the dependency set proposed by the old plan:

- `tokio`
- `zbus`
- `cpal`
- `rubato`
- `evdev`
- `serde`
- `toml`
- `clap`
- `anyhow`
- `tracing`
- `tracing-subscriber`

Results:

- `cargo check` succeeded
- a small `cargo run` stub succeeded
- a D-Bus service stub using `zbus::connection::Builder::session()` succeeded
- an audio stub using `cpal` compiled and opened the default input device

### 3.2 Audio reality check

The default input device reported:

- name: `pipewire`
- sample rate: `44100`
- format: `F32`
- channels: `2`

This is the strongest reason to rewrite the audio section of the old plan.

### 3.3 Runtime linkage check

`ldd` on the compiled Rust stub showed a dependency on:

- `libasound.so.2`

So the refined plan does not claim a fully static Linux binary.

### 3.4 GNOME extension packaging

A minimal throwaway extension directory was created locally with:

- `metadata.json`
- `extension.js`
- GSettings schema XML

Results:

- `glib-compile-schemas` succeeded
- `gnome-extensions pack ...` succeeded

So the extension packaging toolchain is available on the target desktop.

## 4. Refined Architecture Decisions

### 4.1 Language and scope

Keep Rust for the client implementation.

Reason:

- the dependency stack is viable on Ubuntu 26.04
- async D-Bus and TCP fit Rust well
- audio capture and Linux device access are well-covered

### 4.2 Split responsibilities by platform

Use this exact model:

- GNOME Wayland:
  - hotkey handled by GNOME Shell extension
  - text injection handled by `ydotool`
- wlroots compositors:
  - hotkey handled by evdev fallback
  - text injection handled by `wtype` if available, else `ydotool`
- other Wayland desktops:
  - hotkey handled by evdev fallback
  - text injection handled by `ydotool`

This makes GNOME first-class without forcing GNOME-specific assumptions onto all compositors.

### 4.3 Keep the first version intentionally small

Version 1 should include:

- toggle recording
- configurable proxy host/port
- configurable microphone device
- configurable non-GNOME hotkey
- GNOME extension with default shortcut
- text injection into the focused app

Version 1 should not include:

- AT-SPI insertion
- tray app
- waveform UI
- clipboard fallback
- preferences window inside the Rust app

## 5. Updated Directory Layout

Keep the same top-level layout, but add `prefs.js` as optional future work, not a v1 requirement.

```text
wayland-client/
├── strisper-wayland/
│   ├── Cargo.toml
│   └── src/
│       ├── main.rs
│       ├── config.rs
│       ├── audio.rs
│       ├── proxy.rs
│       ├── inject.rs
│       ├── hotkey.rs
│       └── dbus.rs
├── gnome-extension/
│   └── strisper@whisper-streaming/
│       ├── metadata.json
│       ├── extension.js
│       ├── stylesheet.css
│       └── schemas/
│           └── org.gnome.shell.extensions.strisper-wayland.gschema.xml
├── strisper-wayland.service
├── strisper-wayland.desktop
├── install.sh
└── README.md
```

Do not add an introspection XML file in v1 unless there is a specific packaging need. `busctl` and `gdbus` can still work without shipping one manually.

## 6. File-by-File Refined Plan

### 6.1 `strisper-wayland/Cargo.toml`

Dependencies:

```toml
[dependencies]
anyhow = "1"
clap = { version = "4", features = ["derive"] }
cpal = "0.15"
evdev = "0.12"
rubato = "0.15"
serde = { version = "1", features = ["derive"] }
tokio = { version = "1", features = ["full"] }
toml = "0.8"
tracing = "0.1"
tracing-subscriber = { version = "0.3", features = ["env-filter"] }
zbus = { version = "4", features = ["tokio"] }
```

Do not add more crates until the core path works end-to-end.

### 6.2 `src/config.rs`

Store only application-owned settings here:

- proxy host
- proxy port
- audio device hint
- injection mode
- injection delay
- non-GNOME evdev hotkey

Do not pretend this file owns the GNOME keybinding. The extension owns that via GSettings.

Suggested TOML shape:

```toml
[server]
host = "127.0.0.1"
port = 43007

[audio]
device = ""

[inject]
method = "auto"
delay_ms = 12

[hotkey]
key = "Ctrl+Shift+F9"
```

Document clearly:

- on GNOME, `hotkey.key` is ignored
- on non-GNOME Wayland, `hotkey.key` is used by evdev

### 6.3 `src/audio.rs`

This file needs the largest correction relative to the old plan.

Requirements:

1. Query `default_input_config()` and inspect:
   - sample rate
   - channel count
   - sample format
2. Build the stream using the device's native channel count.
3. Branch on `cpal::SampleFormat`:
   - `F32`
   - `I16`
   - `U16`
4. Convert all input to a temporary mono `Vec<f32>`:
   - if channels == 1, use the frame as-is
   - if channels > 1, average each frame to mono
5. Resample from native rate to `16000` whenever the native rate is not `16000`.
6. Convert the post-resample mono stream to signed little-endian `i16`.

Do not hard-code `channels = 1` in the requested config unless you have already confirmed the device supports that exact format.

Implementation note:

- if `supported.channels() == 2`, iterate the callback slice as stereo frames and average left/right
- if `supported.channels() > 2`, average all channels in each frame

### 6.4 `src/proxy.rs`

The core design from the old plan is fine:

- one task writes PCM to the TCP stream
- one task reads transcript lines

Keep only the text payload after parsing:

```text
<start_ms> <end_ms> <text>
```

Return:

- `pcm_tx`
- `text_rx`

That part of the plan does not need architectural change.

### 6.5 `src/inject.rs`

Change the method-selection logic.

Rules:

1. If the current desktop is GNOME, prefer `ydotool` and do not auto-select `wtype`.
2. If the current compositor looks wlroots-based and `wtype` exists, prefer `wtype`.
3. If the requested method is explicitly configured, obey it.
4. If no usable injector is found, emit a loud startup warning.

Desktop detection can use:

- `XDG_CURRENT_DESKTOP`
- `DESKTOP_SESSION`

Keep stdin-based `ydotool type -f -`.

Do not attempt AT-SPI in v1.

### 6.6 `src/hotkey.rs`

Keep evdev as the non-GNOME fallback only.

The Rust module should:

- parse `Ctrl+Shift+F9`
- enumerate keyboard devices
- watch `EV_KEY` events
- send a toggle event when the main key goes down while all required modifiers are held

But `main.rs` should not start this listener on GNOME unless the user explicitly forces it for debugging.

### 6.7 `src/dbus.rs`

Keep the interface minimal:

- `ToggleRecording`
- `StartRecording`
- `StopRecording`
- `IsRecording`
- `RecordingStateChanged`

Remove `TranscriptionReceived` from v1. It is not needed by the GNOME extension and the old plan never routed it correctly.

Bus details:

- bus name: `io.github.bjodah.StrisperWayland`
- object path: `/io/github/bjodah/StrisperWayland`
- interface: `io.github.bjodah.StrisperWayland`

### 6.8 `src/main.rs`

Refine the startup policy.

At startup:

1. parse CLI
2. load config
3. register D-Bus service
4. detect desktop
5. start evdev listener only if:
   - not GNOME, and
   - `--no-evdev` was not passed

On session start:

- start audio capture
- connect to proxy
- forward PCM
- spawn text injection loop
- emit `RecordingStateChanged(true)`

On stop:

- drop session handles
- let proxy writer close cleanly
- emit `RecordingStateChanged(false)`

Do not print emoji status lines in the production plan. Use `tracing` instead.

## 7. GNOME Extension Plan

### 7.1 `metadata.json`

For the current target desktop, declare GNOME `50` support.

Start with:

```json
{
  "name": "Strisper Wayland",
  "description": "Global shortcut and indicator for the Strisper Wayland client",
  "uuid": "strisper@whisper-streaming",
  "version": 1,
  "shell-version": ["50"],
  "url": "https://github.com/bjodah/whisper_streaming",
  "settings-schema": "org.gnome.shell.extensions.strisper-wayland"
}
```

If later testing confirms GNOME `49` also works, add it deliberately.

### 7.2 `schemas/...gschema.xml`

Keep the default keybinding:

```xml
<default>['&lt;Control&gt;&lt;Shift&gt;F9']</default>
```

But document the actual configuration path:

```bash
gsettings set org.gnome.shell.extensions.strisper-wayland \
  toggle-recording "['<Control><Shift>F9']"
```

Do not claim this is a GNOME Settings custom shortcut unless a proper preferences integration is added and tested.

### 7.3 `extension.js`

Responsibilities:

- create a panel indicator
- register the keybinding with `Main.wm.addKeybinding(...)`
- connect to the D-Bus service
- reflect recording state
- hide the indicator if the D-Bus name is absent

Do not add a "Quit" menu item that uses `pkill`.

Use one of these approaches instead:

- v1: no quit action at all
- v2: "Stop Service" action that runs `systemctl --user stop strisper-wayland.service`

### 7.4 `stylesheet.css`

Minimal file is fine.

## 8. Service and Installer Plan

### 8.1 `strisper-wayland.service`

Keep it as a user service.

Recommended unit:

```ini
[Unit]
Description=Strisper Wayland speech-to-text client
After=graphical-session.target
PartOf=graphical-session.target

[Service]
Type=simple
ExecStart=%h/.local/bin/strisper-wayland
Restart=on-failure
RestartSec=5

[Install]
WantedBy=graphical-session.target
```

### 8.2 `strisper-wayland.desktop`

Treat this as optional fallback documentation, not the primary startup path.

Primary startup should be the systemd user service.

### 8.3 `install.sh`

The installer should be Ubuntu-aware and explicit.

Required behavior:

1. verify `cargo`
2. verify `pkg-config`
3. verify `glib-compile-schemas`
4. warn if `ydotool` is missing on GNOME
5. build the Rust binary
6. install binary to `~/.local/bin/`
7. install GNOME extension files
8. compile extension schemas
9. install the user service
10. if `ydotool.service` exists, enable and start it
11. if user is not in `input`, print exact remediation

For Ubuntu docs, the script should print this exact dependency hint:

```bash
sudo apt-get install -y \
  cargo rustc \
  pkg-config libasound2-dev \
  libglib2.0-bin \
  ydotool wtype
```

For the `ydotool` unit, check:

```bash
systemctl --user list-unit-files ydotool.service
```

not `ydotoold.service`.

## 9. README Requirements

The new `wayland-client/README.md` should include these exact sections.

### 9.1 Prerequisites

- Ubuntu 26.04 or another recent Wayland desktop
- GNOME 50 for the extension path
- running `whisper-proxy`
- Rust toolchain
- package dependencies

### 9.2 GNOME install

Include:

```bash
sudo apt-get install -y \
  cargo rustc \
  pkg-config libasound2-dev \
  libglib2.0-bin \
  ydotool wtype
```

Then:

```bash
./install.sh
gnome-extensions enable strisper@whisper-streaming
```

### 9.3 GNOME hotkey configuration

Document `gsettings` rather than vague prose.

### 9.4 Non-GNOME Wayland

State clearly:

- the GNOME extension is not used
- evdev fallback is used
- `hotkey.key` applies here
- `wtype` is preferred on wlroots if present

### 9.5 Troubleshooting

Must include:

```bash
systemctl --user status strisper-wayland.service
systemctl --user status ydotool.service
gsettings get org.gnome.shell.extensions.strisper-wayland toggle-recording
busctl --user tree io.github.bjodah.StrisperWayland
```

## 10. Revised Implementation Order

Implement in this order.

1. Create `wayland-client/strisper-wayland/` and add `Cargo.toml`.
2. Implement `config.rs`.
3. Implement `proxy.rs` and test against the existing Go server.
4. Implement `audio.rs` with real sample-format and channel handling.
5. Implement `inject.rs` with GNOME-aware method selection.
6. Implement `dbus.rs`.
7. Implement `main.rs` desktop detection and lifecycle wiring.
8. Implement `hotkey.rs` for non-GNOME fallback.
9. Create the GNOME extension with shell `50` metadata.
10. Write `install.sh`.
11. Write `README.md`.
12. Perform end-to-end GNOME testing.

## 11. Mandatory Validation Checklist for the Junior Developer

Do not call the feature complete until all of these pass.

### 11.1 Rust/toolchain checks

```bash
cargo check
cargo test
```

### 11.2 Audio path checks

- microphone opens successfully
- stereo microphones are accepted
- `44100 -> 16000` resampling works
- `48000 -> 16000` resampling works

### 11.3 Proxy checks

- can connect to `whisper-proxy`
- can send PCM
- can parse returned transcript lines

### 11.4 GNOME extension checks

```bash
glib-compile-schemas gnome-extension/strisper@whisper-streaming/schemas
gnome-extensions pack gnome-extension/strisper@whisper-streaming --force --out-dir /tmp/strisper-ext
```

Then manually verify:

- extension enables on GNOME 50
- panel indicator appears when the service is available
- indicator hides when the D-Bus service is absent
- `Ctrl+Shift+F9` toggles recording

### 11.5 Injection checks

- on GNOME, `ydotool` path works
- if injection fails, verify `ydotool.service`
- if needed, add user to `input` and log out/back in

### 11.6 Non-GNOME checks

- evdev fallback works when GNOME extension is unavailable
- `wtype` path works on a wlroots compositor

## 12. Final Recommendation

Implement the Wayland client in Rust, keep GNOME integration via a shell extension, but update the plan to match GNOME 50 and real Linux audio behavior.

The old plan is directionally good, but it is not build-verified enough for a junior developer to execute safely on Ubuntu 26.04 without hitting avoidable issues in:

- package installation
- GNOME extension compatibility
- `ydotool` service naming
- audio channel/sample-rate handling
- shortcut configurability semantics
