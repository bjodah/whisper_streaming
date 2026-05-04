use strisper_wayland::{audio, config, dbus, hotkey, inject, proxy};

use std::path::PathBuf;

use anyhow::Context;
use clap::Parser;
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

    let mut hotkey_rx: Option<mpsc::UnboundedReceiver<()>> = None;
    if want_evdev {
        match hotkey::HotkeySpec::parse(&cfg.hotkey.key) {
            Ok(spec) => match hotkey::start_listener(spec) {
                Ok(rx) => {
                    info!("evdev listener active for '{}'", cfg.hotkey.key);
                    hotkey_rx = Some(rx);
                }
                Err(e) => warn!("could not start evdev listener: {e}"),
            },
            Err(e) => warn!("invalid hotkey spec '{}': {e}", cfg.hotkey.key),
        }
    }

    info!("strisper-wayland ready (proxy={host}:{port})");

    let mut audio_session: Option<audio::AudioSession> = None;
    let mut text_rx: Option<mpsc::UnboundedReceiver<String>> = None;
    let mut recording = false;

    loop {
        tokio::select! {
            // Commands from D-Bus clients.
            Some(cmd) = cmd_rx.recv() => {
                let new_state = match cmd {
                    dbus::Command::Start  if !recording => true,
                    dbus::Command::Stop   if  recording => false,
                    dbus::Command::Toggle              => !recording,
                    _                                  => recording,
                };
                if new_state != recording {
                    transition(
                        new_state,
                        &host, port, device_hint.as_deref(),
                        &mut audio_session, &mut text_rx,
                    )
                    .await;
                    recording = new_state;
                    if let Err(e) = dbus::set_recording(&iface_ref, recording).await {
                        warn!("D-Bus signal failed: {e}");
                    }
                }
            }

            // Hotkey events from the evdev listener (non-GNOME only).
            Some(()) = recv_or_pending(hotkey_rx.as_mut()) => {
                let new_state = !recording;
                transition(
                    new_state,
                    &host, port, device_hint.as_deref(),
                    &mut audio_session, &mut text_rx,
                )
                .await;
                recording = new_state;
                if let Err(e) = dbus::set_recording(&iface_ref, recording).await {
                    warn!("D-Bus signal failed: {e}");
                }
            }

            // Transcript text arriving from the proxy.
            Some(text) = recv_or_pending_str(text_rx.as_mut()) => {
                info!("transcript: {text}");
                if let Err(e) = inject::inject_text(&text, &inject_method, inject_delay).await {
                    warn!("inject failed: {e}");
                }
            }
        }
    }
}

/// Start or stop a recording session, maintaining drop-ordering:
/// 1. Drop the cpal stream (stops audio thread and closes pcm_tx).
/// 2. Drop text_rx (proxy write task observes closed pcm_rx and flushes).
/// Then, and only then, `set_recording` in the caller emits the D-Bus signal.
async fn transition(
    start: bool,
    host: &str,
    port: u16,
    device_hint: Option<&str>,
    audio_session: &mut Option<audio::AudioSession>,
    text_rx: &mut Option<mpsc::UnboundedReceiver<String>>,
) {
    if start {
        let (pcm_tx, pcm_rx) = mpsc::unbounded_channel::<Vec<f32>>();
        match audio::start(device_hint, pcm_tx) {
            Ok((session, native_rate, channels)) => {
                match proxy::connect(host, port, pcm_rx, channels, native_rate).await {
                    Ok(rx) => {
                        *audio_session = Some(session);
                        *text_rx = Some(rx);
                        info!("recording started");
                    }
                    Err(e) => error!("proxy connect failed: {e}"),
                }
            }
            Err(e) => error!("audio start failed: {e}"),
        }
    } else {
        // Drop stream first — this is the producer side of pcm_tx.
        drop(audio_session.take());
        // Drop text_rx last.
        drop(text_rx.take());
        info!("recording stopped");
    }
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
