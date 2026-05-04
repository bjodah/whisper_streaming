# Final Implementation Plan: `strisper-wayland`

This document is the authoritative plan for the Wayland client described in
[01-PROMPT.md](/home/ai-bot-bjodah/vc/whisper_streaming/01-PROMPT.md).
It supersedes [03-REFINED-IMPL-PLAN-WAYLAND-CLIENT.md](/home/ai-bot-bjodah/vc/whisper_streaming/03-REFINED-IMPL-PLAN-WAYLAND-CLIENT.md)
and incorporates the parts of [04-CRITIQUE-OF-REFINED-IMPL-PLAN-WAYLAND-CLIENT.md](/home/ai-bot-bjodah/vc/whisper_streaming/04-CRITIQUE-OF-REFINED-IMPL-PLAN-WAYLAND-CLIENT.md)
that survive independent verification on this machine.

The 03 document remains correct on packaging, GNOME 50 metadata, audio
realities, `ydotool` service naming, D-Bus shape, and overall directory
layout. **All of those instructions stand.** The changes below are
targeted: concurrency rules, resampler buffering, signal emission, and
flag wording. They are needed because, as 04 correctly observed, the 03
plan is silent on three boundary concerns that will bite a junior
developer at runtime.

## 0. Verification Receipts

Before writing this document I built a scratch Cargo project at
`/tmp/strisper-verify/verify_stack` containing four small binaries
against the exact dependency set 03 prescribes (`cpal 0.15`, `rubato
0.15`, `zbus 4.4`, `tokio 1`, `clap 4`). All four compile. The interesting
runtime observations:

- `rubato::FftFixedIn::process` with `chunk_in = 1024` and a 1023-frame
  input returns `Err("Insufficient buffer size 1023 ... expected 1024")`.
  With a 1025-frame input it **silently consumes only the first 1024
  frames and discards the rest** — i.e. it does not return an error and
  does not panic. This matters: the consultant said "panic"; the actual
  failure mode for FftFixedIn is "Err on undersized, silent truncation
  on oversized." The prescription (buffer to exact size) is correct
  either way.
- `zbus 4.4`'s `ObjectServer::interface::<_, T>(path).await?` returns an
  `InterfaceRef` whose `.signal_context()` yields a `&SignalContext<'_>`
  that can be handed to a background `tokio` task and used to emit a
  `#[zbus(signal)]` from outside any method handler. Confirmed by
  registering the service on the session bus and emitting a signal from
  a `tokio::spawn` driven by an mpsc channel.
- `cpal 0.15`'s `build_input_stream` callback type is
  `FnMut(&[T], &InputCallbackInfo) + Send + 'static`. The closure is
  synchronous; there is no way to `.await` inside it. Confirmed by
  reading `~/.cargo/registry/src/.../cpal-0.15.3/src/traits.rs:134`.
- `clap 4` `#[arg(long)] no_evdev: bool` produces `no_evdev = true` when
  `--no-evdev` is on the command line and `false` when it is absent.
  The "double negative" is real but is not a bug.

## 1. Adjudication of the Consultant's Critique (04)

### 1.1 cpal callback / tokio boundary — **AGREE**

The 03 plan tells the implementer to "send PCM to the proxy" without
saying which thread does it. 04 is right that a junior will reach for
`.await` inside the cpal callback and discover, painfully, that the
closure is sync. The remedy (mpsc bridge from sync audio thread to
async tokio task) is correct and is the only reasonable pattern for
this stack. Section 4 of this document writes that rule down.

### 1.2 rubato chunk-size discipline — **AGREE WITH A CORRECTION**

The recommendation is right: collect cpal callback slices into a buffer
and feed `input_frames_next()` frames at a time. The diagnosis "the
application will panic" is *imprecise* — the actual failure modes are
`Err` for undersized inputs and silent truncation for oversized — but
the cure is the same. Section 4 of this document spells the buffering
loop out and additionally points the implementer at
`Resampler::process_partial` for end-of-stream flushing.

### 1.3 zbus signal emission outside method calls — **AGREE**

This is the single largest gap in 03. Saying "emit
`RecordingStateChanged(true)` on session start" without explaining
where the `SignalContext` comes from is a near-guarantee that the
implementer will write a method handler that toggles state and assume
it can fire the signal from elsewhere later — and then get stuck. The
verified pattern using `ObjectServer::interface` and
`InterfaceRef::signal_context()` is added to section 5 below.

### 1.4 clap presence-flag wording — **PARTIALLY AGREE**

04 calls the double-negative `!args.no_evdev` a "double-negative
cognitive load." It is. But the consultant's proposed rename to
`--force-evdev` actually changes the *semantics*: 03 defines two distinct
behaviours that need distinct flags or a single tri-state.

03 wants both:

- on **non-GNOME**, evdev is on by default; the user can turn it off
  (`--no-evdev`).
- on **GNOME**, evdev is off by default; the user can turn it on for
  debugging (this case is mentioned in 03 §6.6 but never given a flag).

A single boolean cannot express both. Section 6 of this document
replaces the lone `--no-evdev` with a tri-state `--evdev=auto|on|off`
that defaults to `auto` and folds both 03 cases into one flag. This
preserves 03's intent and removes the awkward negation.

### 1.5 What 04 missed

Two issues 04 did not raise that are on a par with the ones it did:

- **Signal subscription on the GNOME extension side.** The extension
  needs to listen for `RecordingStateChanged` to update the indicator,
  and 03 §7.3 only says "reflect recording state." On GNOME 50, the
  modern path is `Gio.DBusProxy.makeProxyWrapper` with
  `connectSignal('RecordingStateChanged', ...)`. Section 7 below adds
  this.
- **Cleanup on stream drop.** When recording stops, the cpal stream
  must be dropped *before* the tokio sender is dropped, otherwise the
  audio thread can outlive the receiver and emit one final
  send-to-closed-channel error. Section 4 adds the drop order.

## 2. What Does *Not* Change from 03

These sections of 03 are correct as written and are inherited by this
plan unchanged:

- §1 (the entire shortcomings list)
- §2 (verified Ubuntu packages, `libasound2-dev`, `libglib2.0-bin`,
  `ydotool`, `wtype`)
- §3 (independent validation receipts)
- §4 (architecture decisions, platform split, v1 scope)
- §5 (directory layout)
- §6.1, §6.2, §6.4, §6.5 (Cargo.toml, config, proxy, inject)
- §6.6 (evdev hotkey logic — only the flag wording in §6.8 changes)
- §7.1, §7.2, §7.4 (extension metadata, schema, stylesheet)
- §8 (service unit, desktop file, installer ordering)
- §9 (README sections)
- §10 (implementation order)
- §11 (validation checklist)

The remainder of this document supplies the targeted replacements for
§6.3 (`audio.rs`), §6.7 (`dbus.rs`), §6.8 (`main.rs` flags), and §7.3
(extension signal subscription). It also adds a new §4 ("Concurrency and
Boundary Rules") that 03 should have included.

## 3. New Section: Concurrency and Boundary Rules

These rules are non-negotiable. Read them before writing any of
`audio.rs`, `proxy.rs`, or `dbus.rs`.

### 3.1 Three threading domains

The client has exactly three threading domains. Code from one must not
call `.await` or blocking primitives belonging to another:

| Domain | Lives where | Allowed primitives |
|---|---|---|
| **Audio callback** | OS audio thread spawned by `cpal`. Sync. | `tokio::sync::mpsc::UnboundedSender::send` (non-blocking), atomics, simple stack work. |
| **Async runtime** | `tokio` worker threads. | `.await`, `tokio::sync::*`, `zbus`, TCP. |
| **Evdev listener** | One blocking `tokio::task::spawn_blocking` thread, only on non-GNOME. | Blocking reads on `/dev/input/event*`, `tokio::sync::mpsc::UnboundedSender::send` to bridge events back. |

The bridge between **audio callback** and **async runtime** is one
`tokio::sync::mpsc::unbounded_channel::<Vec<f32>>()`. The bridge between
**evdev listener** and **async runtime** is a separate
`tokio::sync::mpsc::unbounded_channel::<HotkeyEvent>()`.

### 3.2 What the cpal callback may do

Only this:

```rust
let cb = move |data: &[f32], _info: &cpal::InputCallbackInfo| {
    let _ = pcm_tx.send(data.to_vec());
};
```

It must not allocate large buffers, must not call into rubato, must not
parse anything, must not log at info level, and absolutely must not
`.await`. All resampling, format conversion, channel downmix, and TCP
I/O happens on the receiving tokio task.

### 3.3 Drop order at stop

When the user stops recording:

1. Drop the `cpal::Stream`. This stops the audio thread and is the
   producer side of `pcm_tx`.
2. The receiving tokio task observes `pcm_rx.recv() == None` and
   flushes any remaining frames through `Resampler::process_partial`,
   then closes the TCP stream half it owns.
3. Only then is `RecordingStateChanged(false)` emitted.

Inverting steps 1 and 3 (stream still running while signal already says
"stopped") will be visibly racy in the GNOME indicator.

## 4. Replacement for §6.3 — `src/audio.rs`

The shape of the file is:

```rust
pub struct AudioSession {
    _stream: cpal::Stream,           // dropped to stop the audio thread
}

pub fn start(
    device_hint: Option<&str>,
    pcm_tx: tokio::sync::mpsc::UnboundedSender<Vec<f32>>,
) -> anyhow::Result<AudioSession> { ... }
```

Inside `start`:

1. Pick the input device. Use `default_input_device()` when
   `device_hint` is `None`; otherwise iterate `host.input_devices()` and
   match by `name()`.
2. Read `device.default_input_config()?`. Capture three values:
   `sample_rate.0` as `u32`, `channels` as `u16`, `sample_format()` as
   `cpal::SampleFormat`. **Do not request `channels = 1`.** Build the
   stream with the device's native channel count.
3. Build the stream with a `match` on `SampleFormat`:
   - `F32` → callback receives `&[f32]`, send `data.to_vec()`.
   - `I16` → callback converts each sample with
     `(*s as f32) / i16::MAX as f32` into a fresh `Vec<f32>`.
   - `U16` → callback converts with
     `((*s as f32) - 32768.0) / 32768.0`.
   In every arm, the very last thing the callback does is
   `let _ = pcm_tx.send(buf);`. Nothing else.
4. Call `stream.play()?` and return `AudioSession { _stream: stream }`.

A *separate* tokio task — owned by `proxy.rs` and described in §5 below
— consumes from the receiver end of `pcm_tx`, downmixes, resamples, and
writes PCM to TCP. The audio module knows nothing about TCP.

### 4.1 Downmix and resample loop (lives in `proxy.rs`, not `audio.rs`)

Pseudocode for the consumer:

```rust
let mut accum: Vec<f32> = Vec::with_capacity(8192);
let mut resampler: Option<rubato::FftFixedIn<f32>> = None;
let chunk_in = 1024usize;

while let Some(buf) = pcm_rx.recv().await {
    // 1. Downmix to mono in place.
    let mono: Vec<f32> = if channels == 1 {
        buf
    } else {
        buf.chunks_exact(channels as usize)
            .map(|frame| frame.iter().sum::<f32>() / channels as f32)
            .collect()
    };

    // 2. Resample only if native rate != 16_000.
    if native_rate != 16_000 {
        let r = resampler.get_or_insert_with(|| {
            rubato::FftFixedIn::<f32>::new(
                native_rate as usize, 16_000, chunk_in, 2, 1
            ).expect("resampler init")
        });

        accum.extend_from_slice(&mono);
        let need = r.input_frames_next();
        while accum.len() >= need {
            let head: Vec<f32> = accum.drain(..need).collect();
            let out = r.process(&[head], None)?;
            write_pcm_i16_le(&mut tcp, &out[0]).await?;
        }
    } else {
        write_pcm_i16_le(&mut tcp, &mono).await?;
    }
}

// On end-of-stream: flush whatever is left through process_partial.
if let Some(mut r) = resampler.take() {
    if !accum.is_empty() {
        let out = r.process_partial(Some(&[accum]), None)?;
        write_pcm_i16_le(&mut tcp, &out[0]).await?;
    }
}
```

`write_pcm_i16_le` converts each `f32` sample with `(s.clamp(-1.0,
1.0) * i16::MAX as f32) as i16`, writes little-endian, and uses
`tokio::io::AsyncWriteExt::write_all`.

### 4.2 Why `FftFixedIn` and not `FftFixedInOut`

`FftFixedIn` lets the output length vary but the input length is fixed.
That is the right shape when you are bridging from a producer (cpal)
that delivers variable-length buffers but a consumer (TCP) that does
not care about output framing. `FftFixedInOut` forces both sides to be
fixed and is harder to integrate.

## 5. Replacement for §6.7 — `src/dbus.rs`

The interface stays exactly as 03 specified. What 03 omitted is *how*
the rest of the application emits `RecordingStateChanged` from outside
a method handler.

### 5.1 Interface struct and trait

```rust
use std::sync::atomic::{AtomicBool, Ordering};
use tokio::sync::mpsc;
use zbus::object_server::SignalContext;
use zbus::{interface, connection::Builder, Connection};

pub enum Command {
    Start,
    Stop,
    Toggle,
}

pub struct Strisper {
    pub recording: AtomicBool,
    pub cmd_tx: mpsc::UnboundedSender<Command>,
}

#[interface(name = "io.github.bjodah.StrisperWayland")]
impl Strisper {
    async fn toggle_recording(&self) -> bool {
        let _ = self.cmd_tx.send(Command::Toggle);
        self.recording.load(Ordering::SeqCst)
    }

    async fn start_recording(&self) {
        let _ = self.cmd_tx.send(Command::Start);
    }

    async fn stop_recording(&self) {
        let _ = self.cmd_tx.send(Command::Stop);
    }

    #[zbus(property)]
    async fn is_recording(&self) -> bool {
        self.recording.load(Ordering::SeqCst)
    }

    #[zbus(signal)]
    async fn recording_state_changed(
        ctx: &SignalContext<'_>,
        recording: bool,
    ) -> zbus::Result<()>;
}
```

Note that method handlers do *not* mutate state directly. They drop a
`Command` on a channel; the central event loop in `main.rs` does the
real work. This decouples D-Bus from audio/proxy lifecycle.

### 5.2 Bus registration and SignalContext extraction

```rust
pub async fn start(
    cmd_tx: mpsc::UnboundedSender<Command>,
) -> anyhow::Result<(Connection, zbus::object_server::InterfaceRef<Strisper>)> {
    let iface = Strisper {
        recording: AtomicBool::new(false),
        cmd_tx,
    };

    let conn = Builder::session()?
        .name("io.github.bjodah.StrisperWayland")?
        .serve_at("/io/github/bjodah/StrisperWayland", iface)?
        .build()
        .await?;

    let iface_ref = conn
        .object_server()
        .interface::<_, Strisper>("/io/github/bjodah/StrisperWayland")
        .await?;

    Ok((conn, iface_ref))
}
```

The returned `InterfaceRef<Strisper>` is the bridge. Hold it for the
lifetime of the process.

### 5.3 Emitting the signal from outside a method call

When the central loop changes recording state — because the user
pressed the GNOME hotkey, ran `busctl call`, or hit the evdev
fallback — it does this:

```rust
async fn set_recording(
    iface_ref: &zbus::object_server::InterfaceRef<Strisper>,
    new_state: bool,
) -> zbus::Result<()> {
    let iface = iface_ref.get().await;
    iface.recording.store(new_state, Ordering::SeqCst);
    Strisper::recording_state_changed(iface_ref.signal_context(), new_state).await
}
```

This was confirmed working against the live session bus during
verification.

## 6. Replacement for §6.8 — flag wording in `main.rs`

Replace the lone `--no-evdev` boolean with a tri-state:

```rust
#[derive(clap::ValueEnum, Clone, Debug, Default)]
pub enum EvdevMode {
    /// Run evdev listener iff desktop is not GNOME (default).
    #[default]
    Auto,
    /// Always run the evdev listener (debugging on GNOME).
    On,
    /// Never run the evdev listener.
    Off,
}

#[derive(clap::Parser, Debug)]
pub struct Args {
    #[arg(long, value_enum, default_value_t = EvdevMode::Auto)]
    pub evdev: EvdevMode,
    // ...
}
```

Startup logic in `main.rs`:

```rust
let want_evdev = match args.evdev {
    EvdevMode::On => true,
    EvdevMode::Off => false,
    EvdevMode::Auto => !is_gnome(),
};
```

This eliminates the double-negative and gives the GNOME-debug case the
flag 03 §6.6 implied but never specified. README §9.2 should add a
short example for `--evdev on` in a "force evdev for debugging on
GNOME" troubleshooting note.

## 7. Replacement for §7.3 — extension signal subscription

The extension must subscribe to `RecordingStateChanged` so the panel
indicator reflects the running state instead of polling. On GNOME 50,
`Gio.DBusProxy.makeProxyWrapper` is the right tool:

```javascript
import Gio from 'gi://Gio';

const Iface = '<node>\
  <interface name="io.github.bjodah.StrisperWayland">\
    <method name="ToggleRecording"><arg type="b" direction="out"/></method>\
    <method name="StartRecording"/>\
    <method name="StopRecording"/>\
    <property name="IsRecording" type="b" access="read"/>\
    <signal name="RecordingStateChanged"><arg type="b"/></signal>\
  </interface>\
</node>';

const StrisperProxy = Gio.DBusProxy.makeProxyWrapper(Iface);

class IndicatorController {
    enable() {
        this._proxy = StrisperProxy(
            Gio.DBus.session,
            'io.github.bjodah.StrisperWayland',
            '/io/github/bjodah/StrisperWayland',
        );
        this._sigId = this._proxy.connectSignal(
            'RecordingStateChanged',
            (_p, _sender, [recording]) => this._setRecording(recording),
        );
        this._watchId = Gio.bus_watch_name(
            Gio.BusType.SESSION,
            'io.github.bjodah.StrisperWayland',
            Gio.BusNameWatcherFlags.NONE,
            () => this._indicator.show(),
            () => this._indicator.hide(),
        );
    }
    disable() {
        if (this._sigId) this._proxy.disconnectSignal(this._sigId);
        if (this._watchId) Gio.bus_unwatch_name(this._watchId);
        this._proxy = null;
    }
}
```

`Gio.bus_watch_name` is what implements 03 §7.3's "hide the indicator
if the D-Bus name is absent" line. 03 did not specify the API.

## 8. Updated Implementation Order

Insert two new steps into 03 §10. The full ordered list:

1. Create `wayland-client/strisper-wayland/` and add `Cargo.toml`.
2. Implement `config.rs`.
3. Implement `proxy.rs` skeleton (TCP only, no audio yet) and test
   against the existing Go server with synthetic PCM.
4. Implement `audio.rs` with sample-format and channel handling, **and
   the cpal-callback / tokio-mpsc bridge documented in §3.2**.
5. Wire the §4.1 downmix-and-resample loop into `proxy.rs`.
6. Implement `inject.rs` with GNOME-aware method selection.
7. Implement `dbus.rs` **including the InterfaceRef / SignalContext
   pattern in §5**.
8. Implement `main.rs` with the §3.3 stop ordering and the §6 flag.
9. Implement `hotkey.rs` for non-GNOME fallback.
10. Create the GNOME extension with shell `50` metadata, **including
    the §7 signal subscription**.
11. Write `install.sh`.
12. Write `README.md`.
13. Perform end-to-end GNOME testing per 03 §11.

## 9. Verdict on the Critique

| Point | Verdict | Action |
|---|---|---|
| 04 §1: cpal/tokio boundary | Agree, verified | New §3 + §4 here |
| 04 §2: rubato chunk sizes | Agree (failure-mode wording too strong) | New §4.1 here |
| 04 §3: zbus signal context | Agree, verified on session bus | New §5 here |
| 04 §4: clap flag rename | Partially agree (semantics subtler) | §6 here uses tri-state |

Two additional issues 04 missed are now covered: extension-side signal
subscription (§7) and stream-drop ordering (§3.3).

The 03 plan's structural decisions — Rust, GNOME 50 metadata, ydotool
service naming, package list, v1 scope — all stand. The 04 critique is
correct on the three concurrency-adjacent gaps and is right that
without those additions a junior developer would hit cryptic runtime
failures. With this document inlining those rules and verified code
patterns, the plan is implementable.
