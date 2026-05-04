use strisper_wayland::{audio, config, dbus, hotkey, inject, proxy};

use std::path::PathBuf;

use anyhow::Context;
use clap::Parser;
use tokio::io::AsyncWriteExt;
use tokio::sync::mpsc;
use tracing::{error, info, warn};

/// Evdev hotkey listener policy.
#[derive(clap::ValueEnum, Clone, Debug, Default)]
pub enum EvdevMode {
    /// Run evdev listener iff the current desktop is not GNOME (default).
    #[default]
    Auto,
    /// Always run the evdev listener (useful for debugging on GNOME).
    On,
    /// Never run the evdev listener.
    Off,
}

#[derive(Parser, Debug)]
#[command(
    name = "strisper-wayland",
    about = "Wayland speech-to-text client for whisper_streaming"
)]
pub struct Args {
    /// Evdev hotkey listener mode.
    #[arg(long, value_enum, default_value_t = EvdevMode::Auto)]
    pub evdev: EvdevMode,

    /// Path to the TOML config file (default: ~/.config/strisper-wayland/config.toml).
    #[arg(long)]
    pub config: Option<PathBuf>,

    /// Proxy host (overrides config).
    #[arg(long)]
    pub host: Option<String>,

    /// Proxy port (overrides config).
    #[arg(long)]
    pub port: Option<u16>,

    /// Audio device hint (substring match; overrides config).
    #[arg(long)]
    pub device: Option<String>,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum RecordingMode {
    Keyboard,
    File,
}

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env()
                .unwrap_or_else(|_| "strisper_wayland=info".parse().unwrap()),
        )
        .init();

    let args = Args::parse();
    let cfg = config::load(args.config.as_deref())?;

    let host = args.host.unwrap_or(cfg.server.host.clone());
    let port = args.port.unwrap_or(cfg.server.port);
    let device_hint = args
        .device
        .or_else(|| (!cfg.audio.device.is_empty()).then(|| cfg.audio.device.clone()));
    let file_path = expand_tilde(&cfg.file.path);

    // D-Bus command channel: method handlers drop commands here; the event
    // loop below processes them.
    let (cmd_tx, mut cmd_rx) = mpsc::unbounded_channel::<dbus::Command>();
    let (_conn, iface_ref) = dbus::start(cmd_tx.clone())
        .await
        .context("D-Bus setup failed")?;

    let inject_method = inject::detect_method(&cfg.inject.method);
    let inject_delay = cfg.inject.delay_ms;

    let want_evdev = match args.evdev {
        EvdevMode::On => true,
        EvdevMode::Off => false,
        EvdevMode::Auto => !inject::is_gnome(),
    };

    let mut hotkey_rx: Option<mpsc::UnboundedReceiver<RecordingMode>> = None;
    if want_evdev {
        match hotkey::HotkeySpec::parse(&cfg.hotkey.key) {
            Ok(spec) => match hotkey::start_listener(spec) {
                Ok(rx) => {
                    info!("evdev listener active for '{}'", cfg.hotkey.key);
                    hotkey_rx = Some(map_hotkey_rx(rx, RecordingMode::Keyboard));
                }
                Err(e) => warn!("could not start evdev listener: {e}"),
            },
            Err(e) => warn!("invalid hotkey spec '{}': {e}", cfg.hotkey.key),
        }

        match hotkey::HotkeySpec::parse(&cfg.hotkey.file_key) {
            Ok(spec) => match hotkey::start_listener(spec) {
                Ok(rx) => {
                    info!("evdev listener active for '{}'", cfg.hotkey.file_key);
                    hotkey_rx = Some(merge_hotkey_rx(
                        hotkey_rx,
                        map_hotkey_rx(rx, RecordingMode::File),
                    ));
                }
                Err(e) => warn!("could not start evdev listener: {e}"),
            },
            Err(e) => warn!("invalid hotkey spec '{}': {e}", cfg.hotkey.file_key),
        }
    }

    info!("strisper-wayland ready (proxy={host}:{port})");

    let mut audio_session: Option<audio::AudioSession> = None;
    let mut text_rx: Option<mpsc::UnboundedReceiver<String>> = None;
    let mut transcript_spacing = inject::TranscriptSpacing::default();
    let mut mode: Option<RecordingMode> = None;

    loop {
        tokio::select! {
            // Commands from D-Bus clients.
            Some(cmd) = cmd_rx.recv() => {
                let new_mode = match cmd {
                    dbus::Command::Start => match mode {
                        None | Some(RecordingMode::Keyboard) => Some(RecordingMode::Keyboard),
                        Some(RecordingMode::File) => None,
                    },
                    dbus::Command::Toggle => toggled_mode(mode, RecordingMode::Keyboard),
                    dbus::Command::ToggleFile => toggled_mode(mode, RecordingMode::File),
                    dbus::Command::Stop => None,
                };
                handle_mode_change(
                    new_mode,
                    &host, port, device_hint.as_deref(),
                    &mut audio_session, &mut text_rx,
                    &mut transcript_spacing, &mut mode, &iface_ref,
                ).await;
            }

            // Hotkey events from the evdev listener (non-GNOME only).
            Some(requested) = recv_or_pending(hotkey_rx.as_mut()) => {
                handle_mode_change(
                    toggled_mode(mode, requested),
                    &host, port, device_hint.as_deref(),
                    &mut audio_session, &mut text_rx,
                    &mut transcript_spacing, &mut mode, &iface_ref,
                ).await;
            }

            // Transcript text arriving from the proxy.
            Some(text) = recv_or_pending_str(text_rx.as_mut()) => {
                info!("transcript: {text}");
                let text = transcript_spacing.apply(&text);
                match mode {
                    Some(RecordingMode::Keyboard) => {
                        if let Err(e) = inject::inject_text(&text, &inject_method, inject_delay).await {
                            warn!("inject failed: {e}");
                        }
                    }
                    Some(RecordingMode::File) => {
                        if let Err(e) = append_to_file(&file_path, &text).await {
                            warn!("file append failed: {e}");
                        }
                    }
                    None => {}
                }
            }
        }
    }
}

fn toggled_mode(
    active: Option<RecordingMode>,
    requested: RecordingMode,
) -> Option<RecordingMode> {
    match active {
        None => Some(requested),
        Some(active) if active == requested => None,
        Some(_) => None,
    }
}

async fn handle_mode_change(
    new_mode: Option<RecordingMode>,
    host: &str,
    port: u16,
    device_hint: Option<&str>,
    audio_session: &mut Option<audio::AudioSession>,
    text_rx: &mut Option<mpsc::UnboundedReceiver<String>>,
    transcript_spacing: &mut inject::TranscriptSpacing,
    mode: &mut Option<RecordingMode>,
    iface_ref: &zbus::object_server::InterfaceRef<dbus::Strisper>,
) {
    if new_mode == *mode {
        return;
    }

    transcript_spacing.reset();
    let actual_mode = transition(
        new_mode,
        host, port, device_hint,
        audio_session, text_rx,
    )
    .await;
    *mode = actual_mode;
    if let Err(e) = dbus::set_recording(iface_ref, mode.is_some()).await {
        warn!("D-Bus signal failed: {e}");
    }
}

/// Start or stop a recording session, maintaining drop-ordering:
/// 1. Drop the cpal stream (stops audio thread and closes pcm_tx).
/// 2. Drop text_rx (proxy write task observes closed pcm_rx and flushes).
/// Then, and only then, `set_recording` in the caller emits the D-Bus signal.
async fn transition(
    mode: Option<RecordingMode>,
    host: &str,
    port: u16,
    device_hint: Option<&str>,
    audio_session: &mut Option<audio::AudioSession>,
    text_rx: &mut Option<mpsc::UnboundedReceiver<String>>,
) -> Option<RecordingMode> {
    if let Some(mode) = mode {
        let (pcm_tx, pcm_rx) = mpsc::unbounded_channel::<Vec<f32>>();
        match audio::start(device_hint, pcm_tx) {
            Ok((session, native_rate, channels)) => {
                match proxy::connect(host, port, pcm_rx, channels, native_rate).await {
                    Ok(rx) => {
                        *audio_session = Some(session);
                        *text_rx = Some(rx);
                        info!("recording started ({mode:?})");
                        Some(mode)
                    }
                    Err(e) => {
                        error!("proxy connect failed: {e}");
                        None
                    }
                }
            }
            Err(e) => {
                error!("audio start failed: {e}");
                None
            }
        }
    } else {
        // Drop stream first — this is the producer side of pcm_tx.
        drop(audio_session.take());
        // Drop text_rx last.
        drop(text_rx.take());
        info!("recording stopped");
        None
    }
}

fn expand_tilde(path: &str) -> PathBuf {
    if path == "~" {
        return std::env::var_os("HOME").map(PathBuf::from).unwrap_or_else(|| PathBuf::from("."));
    }
    if let Some(rest) = path.strip_prefix("~/") {
        if let Some(home) = std::env::var_os("HOME") {
            return PathBuf::from(home).join(rest);
        }
    }
    PathBuf::from(path)
}

async fn append_to_file(path: &PathBuf, text: &str) -> anyhow::Result<()> {
    if let Some(parent) = path.parent() {
        tokio::fs::create_dir_all(parent)
            .await
            .with_context(|| format!("failed to create {}", parent.display()))?;
    }

    let mut file = tokio::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(path)
        .await
        .with_context(|| format!("failed to open {}", path.display()))?;
    file.write_all(text.as_bytes())
        .await
        .with_context(|| format!("failed to write {}", path.display()))?;
    Ok(())
}

fn map_hotkey_rx(
    mut rx: mpsc::UnboundedReceiver<()>,
    mode: RecordingMode,
) -> mpsc::UnboundedReceiver<RecordingMode> {
    let (tx, out_rx) = mpsc::unbounded_channel();
    tokio::spawn(async move {
        while rx.recv().await.is_some() {
            if tx.send(mode).is_err() {
                break;
            }
        }
    });
    out_rx
}

fn merge_hotkey_rx(
    existing: Option<mpsc::UnboundedReceiver<RecordingMode>>,
    mut incoming: mpsc::UnboundedReceiver<RecordingMode>,
) -> mpsc::UnboundedReceiver<RecordingMode> {
    let (tx, out_rx) = mpsc::unbounded_channel();
    if let Some(mut existing) = existing {
        let tx_existing = tx.clone();
        tokio::spawn(async move {
            while let Some(mode) = existing.recv().await {
                if tx_existing.send(mode).is_err() {
                    break;
                }
            }
        });
    }
    tokio::spawn(async move {
        while let Some(mode) = incoming.recv().await {
            if tx.send(mode).is_err() {
                break;
            }
        }
    });
    out_rx
}

/// Receive from `rx` if it is `Some`, otherwise return a future that never
/// resolves (used to make optional branches in `tokio::select!`).
async fn recv_or_pending<T>(rx: Option<&mut mpsc::UnboundedReceiver<T>>) -> Option<T> {
    match rx {
        Some(r) => r.recv().await,
        None => std::future::pending().await,
    }
}

async fn recv_or_pending_str(
    rx: Option<&mut mpsc::UnboundedReceiver<String>>,
) -> Option<String> {
    match rx {
        Some(r) => r.recv().await,
        None => std::future::pending().await,
    }
}
