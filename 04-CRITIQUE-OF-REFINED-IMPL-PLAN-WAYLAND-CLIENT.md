# Critique of Refined Implementation Plan: `strisper-wayland`

The refined implementation plan defined in `03-REFINED-IMPL-PLAN-WAYLAND-CLIENT.md` provides a much stronger foundation for Ubuntu 26.04 / GNOME 50 than the initial plan. I have independently verified that the proposed Rust dependency stack (including `zbus` 4, `cpal` 0.15, `rubato` 0.15, and `tokio`) compiles correctly and that the required libraries are compatible.

However, from the perspective of a junior engineer attempting to implement this plan, there are several significant architectural pitfalls related to threading, async boundaries, and library-specific constraints that are either glossed over or omitted. If not addressed, these will result in compile errors, runtime panics, or logic bugs.

Below are the identified shortcomings and recommendations for improvement.

## 1. Asynchronous Boundaries: `cpal` vs. `tokio`

**The Shortcoming:**
The plan correctly suggests using `cpal` for audio capture and `tokio` for async D-Bus/TCP operations. However, it fails to mention that `cpal` stream callbacks run on a dedicated OS-level audio thread which is strictly synchronous and performance-sensitive.
A junior engineer following the plan might attempt to write PCM data directly to the TCP socket (or `tokio` channels) from inside the `cpal` callback by using `.await` or blocking locks. This will cause compilation failures (since the callback isn't `async`) or audio dropouts/stutters (if they block the audio thread).

**Recommendation:**
Update section `6.3 src/audio.rs` and `6.4 src/proxy.rs` to explicitly state:
*   The `cpal` audio callback must **never** block.
*   The junior engineer should use a non-blocking channel (e.g., `tokio::sync::mpsc::unbounded_channel()` or `crossbeam::channel`) to pass the mono `f32` slices from the synchronous `cpal` thread to an asynchronous `tokio` task.
*   All resampling (`rubato`) and TCP communication should happen within the `tokio` task, not the audio callback thread.

## 2. Strict Chunk Sizes for Resampling (`rubato`)

**The Shortcoming:**
The plan says: "Resample from native rate to 16000 whenever the native rate is not 16000."
It proposes the `rubato` crate, but it omits a critical detail: `rubato` requires strict input frame sizes depending on the resampler used (e.g., `FftFixedIn`). The `cpal` callback, on the other hand, delivers audio buffers of varying lengths depending on OS scheduling and hardware. If a junior engineer feeds the variable-length slice directly into `rubato.process()`, the application will panic.

**Recommendation:**
Update section `6.3 src/audio.rs` to explicitly mention buffer accumulation:
*   Before feeding data to `rubato`, incoming slices from the channel must be collected into an internal buffer (or ring-buffer).
*   Data should only be popped from this buffer and passed to `rubato` in exact chunk sizes (as defined by the resampler's required input frame count).
*   Any leftover frames must remain in the buffer for the next iteration.

## 3. Emitting D-Bus Signals Outside Method Calls (`zbus`)

**The Shortcoming:**
The plan outlines the D-Bus interface and instructs the application to emit `RecordingStateChanged(true)` or `false` on session start and stop.
In `zbus` v4, emitting signals from *outside* a method call (e.g., triggering a signal from a background thread or a global hotkey event) is not trivial for a junior engineer. They must obtain an `InterfaceRef` or an `object_server::SignalContext` from the interface when it is registered. If the plan just says "emit the signal", the junior will likely be unable to figure out how to bridge the hotkey event back into the D-Bus server.

**Recommendation:**
Update section `6.7 src/dbus.rs` to explain the signal emission strategy:
*   Explain that the `StrisperWayland` interface struct needs to store a way to be notified of state changes, or that the application needs to extract and keep the `zbus::object_server::SignalContext`.
*   Provide a brief hint to use `InterfaceRef::signal_context()` inside the `tokio` setup loop and pass that context (or a channel that triggers it) to the component that toggles the recording state.

## 4. `clap` Presence Flags Re-clarification

**The Shortcoming:**
The original plan was corrected for `--no-evdev false`. However, the refined plan in `1.9` just says: "If `--no-evdev` exists, it disables evdev."
A junior engineer might implement this flag as a boolean `#[arg(long)] no_evdev: bool` in `clap`. But the default behavior of `bool` arguments in `clap` v4 (using `action = ArgAction::SetTrue`) means its value is `true` when the flag is present and `false` when absent. This causes a double-negative cognitive load (`if !args.no_evdev`).

**Recommendation:**
Keep it simple for the junior developer. Tell them to use `#[arg(long, default_value_t = false)] no_evdev: bool` or suggest renaming the flag to something affirmative like `--force-evdev` or strictly define the struct mapping to avoid logical bugs during the desktop detection phase.

## Conclusion

The `03-REFINED-IMPL-PLAN-WAYLAND-CLIENT.md` document is solid in its system design, dependency selection, and platform-specific awareness. By adding explicit instructions on **thread-boundary crossings**, **buffer chunking**, and **zbus signal contexts**, the plan will be truly robust and implementable by a junior developer without requiring them to decipher cryptic runtime panics.