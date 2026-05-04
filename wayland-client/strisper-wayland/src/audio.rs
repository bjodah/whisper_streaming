use anyhow::Context;
use cpal::traits::{DeviceTrait, HostTrait, StreamTrait};
use tokio::sync::mpsc;
use tracing::info;

/// Keeps the cpal stream alive. Drop to stop audio capture.
pub struct AudioSession {
    _stream: cpal::Stream,
}

/// Open an input device and start streaming raw audio samples via `pcm_tx`.
///
/// The callback only sends `Vec<f32>` — all downmix and resample work happens
/// on the receiving tokio task in `proxy.rs`, never inside the callback.
///
/// Returns `(session, native_sample_rate, channel_count)`.
pub fn start(
    device_hint: Option<&str>,
    pcm_tx: mpsc::UnboundedSender<Vec<f32>>,
) -> anyhow::Result<(AudioSession, u32, u16)> {
    let host = cpal::default_host();

    let device = if let Some(hint) = device_hint {
        host.input_devices()
            .context("cannot list input devices")?
            .find(|d| d.name().map(|n| n.contains(hint)).unwrap_or(false))
            .with_context(|| format!("no input device matching '{hint}'"))?
    } else {
        host.default_input_device()
            .context("no default input device")?
    };

    info!("audio device: {}", device.name().unwrap_or_default());

    let config = device
        .default_input_config()
        .context("cannot get default input config")?;

    let native_rate = config.sample_rate().0;
    let channels = config.channels();
    let sample_format = config.sample_format();

    info!("native rate={native_rate} channels={channels} format={sample_format:?}");

    let err_fn = |e: cpal::StreamError| tracing::warn!("audio stream error: {e}");

    // Build the stream using the device's native format. The callback's only
    // job is to forward raw samples; it must not block or await.
    let stream = match sample_format {
        cpal::SampleFormat::F32 => {
            let tx = pcm_tx;
            device.build_input_stream(
                &config.into(),
                move |data: &[f32], _: &cpal::InputCallbackInfo| {
                    let _ = tx.send(data.to_vec());
                },
                err_fn,
                None,
            )
        }
        cpal::SampleFormat::I16 => {
            let tx = pcm_tx;
            device.build_input_stream(
                &config.into(),
                move |data: &[i16], _: &cpal::InputCallbackInfo| {
                    let buf: Vec<f32> =
                        data.iter().map(|&s| s as f32 / i16::MAX as f32).collect();
                    let _ = tx.send(buf);
                },
                err_fn,
                None,
            )
        }
        cpal::SampleFormat::U16 => {
            let tx = pcm_tx;
            device.build_input_stream(
                &config.into(),
                move |data: &[u16], _: &cpal::InputCallbackInfo| {
                    let buf: Vec<f32> = data
                        .iter()
                        .map(|&s| (s as f32 - 32768.0) / 32768.0)
                        .collect();
                    let _ = tx.send(buf);
                },
                err_fn,
                None,
            )
        }
        other => anyhow::bail!("unsupported sample format: {other:?}"),
    }
    .context("failed to build input stream")?;

    stream.play().context("failed to start audio stream")?;

    Ok((AudioSession { _stream: stream }, native_rate, channels))
}
