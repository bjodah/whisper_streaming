use anyhow::Context;
use rubato::{FftFixedIn, Resampler};
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};
use tokio::net::TcpStream;
use tokio::sync::mpsc;
use tracing::{debug, info, warn};

/// Connect to the whisper proxy at `host:port`.
///
/// Spawns two background tasks:
/// - **write task**: consumes `pcm_rx`, downmixes to mono, resamples to 16 kHz,
///   converts to i16-LE, and writes to TCP.
/// - **read task**: reads transcript lines from TCP and sends the text payload
///   to the returned receiver.
///
/// The caller stops transcription by dropping `pcm_rx` and the returned
/// `AudioSession` (which closes the write end).
pub async fn connect(
    host: &str,
    port: u16,
    mut pcm_rx: mpsc::UnboundedReceiver<Vec<f32>>,
    channels: u16,
    native_rate: u32,
) -> anyhow::Result<mpsc::UnboundedReceiver<String>> {
    let addr = format!("{host}:{port}");
    let stream = TcpStream::connect(&addr)
        .await
        .with_context(|| format!("cannot connect to proxy at {addr}"))?;

    info!("connected to proxy at {addr}");

    let (tcp_read, tcp_write) = stream.into_split();
    let (text_tx, text_rx) = mpsc::unbounded_channel::<String>();

    // Read task: parse transcript lines and forward text payload.
    tokio::spawn(async move {
        let reader = BufReader::new(tcp_read);
        let mut lines = reader.lines();
        while let Ok(Some(line)) = lines.next_line().await {
            let text = parse_transcript_line(&line);
            if !text.is_empty() && text_tx.send(text).is_err() {
                break;
            }
        }
        debug!("proxy read task exiting");
    });

    // Write task: downmix → resample → i16-LE → TCP.
    tokio::spawn(async move {
        if let Err(e) = pcm_write_loop(tcp_write, &mut pcm_rx, channels, native_rate).await {
            warn!("proxy write task error: {e}");
        }
        debug!("proxy write task exiting");
    });

    Ok(text_rx)
}

async fn pcm_write_loop(
    mut tcp: tokio::net::tcp::OwnedWriteHalf,
    pcm_rx: &mut mpsc::UnboundedReceiver<Vec<f32>>,
    channels: u16,
    native_rate: u32,
) -> anyhow::Result<()> {
    // FftFixedIn requires an exact input chunk size; we accumulate samples
    // and feed `input_frames_next()` at a time.
    const CHUNK_IN: usize = 1024;
    let mut accum: Vec<f32> = Vec::with_capacity(8192);
    let mut resampler: Option<FftFixedIn<f32>> = None;

    while let Some(buf) = pcm_rx.recv().await {
        let mono = downmix(buf, channels);

        if native_rate != 16_000 {
            let r = resampler.get_or_insert_with(|| {
                FftFixedIn::<f32>::new(native_rate as usize, 16_000, CHUNK_IN, 2, 1)
                    .expect("resampler init")
            });

            accum.extend_from_slice(&mono);
            let need = r.input_frames_next();
            while accum.len() >= need {
                let head: Vec<f32> = accum.drain(..need).collect();
                let out = r.process(&[head], None)?;
                tcp.write_all(&pcm_to_i16_le(&out[0])).await.context("TCP write")?;
            }
        } else {
            tcp.write_all(&pcm_to_i16_le(&mono)).await.context("TCP write")?;
        }
    }

    // End-of-stream: flush any leftover samples through the resampler.
    if let Some(mut r) = resampler.take() {
        if !accum.is_empty() {
            let out = r.process_partial(Some(&[accum]), None)?;
            tcp.write_all(&pcm_to_i16_le(&out[0])).await.context("TCP write")?;
        }
    }

    Ok(())
}

fn downmix(buf: Vec<f32>, channels: u16) -> Vec<f32> {
    if channels == 1 {
        buf
    } else {
        buf.chunks_exact(channels as usize)
            .map(|frame| frame.iter().sum::<f32>() / channels as f32)
            .collect()
    }
}

/// Convert f32 samples (range -1.0..=1.0) to signed 16-bit little-endian bytes.
pub fn pcm_to_i16_le(samples: &[f32]) -> Vec<u8> {
    let mut out = Vec::with_capacity(samples.len() * 2);
    for &s in samples {
        let i = (s.clamp(-1.0, 1.0) * i16::MAX as f32) as i16;
        out.extend_from_slice(&i.to_le_bytes());
    }
    out
}

/// Parse a proxy transcript line of the form `<start_ms> <end_ms> <text>`.
/// Returns the text portion, or an empty string if malformed.
pub fn parse_transcript_line(line: &str) -> String {
    let mut parts = line.splitn(3, ' ');
    parts.next(); // start_ms
    parts.next(); // end_ms
    parts.next().unwrap_or("").to_string()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_transcript_basic() {
        assert_eq!(parse_transcript_line("100 200 hello world"), "hello world");
        assert_eq!(parse_transcript_line("0 1 word"), "word");
        assert_eq!(parse_transcript_line("0 500 "), "");
        assert_eq!(parse_transcript_line(""), "");
        assert_eq!(parse_transcript_line("0 0"), "");
    }

    #[test]
    fn parse_transcript_preserves_spaces_in_text() {
        assert_eq!(
            parse_transcript_line("1000 2000 hello beautiful world"),
            "hello beautiful world"
        );
    }

    #[test]
    fn pcm_to_i16_le_zero() {
        let b = pcm_to_i16_le(&[0.0]);
        assert_eq!(b.len(), 2);
        assert_eq!(i16::from_le_bytes([b[0], b[1]]), 0);
    }

    #[test]
    fn pcm_to_i16_le_full_scale() {
        let b = pcm_to_i16_le(&[1.0, -1.0]);
        assert_eq!(i16::from_le_bytes([b[0], b[1]]), i16::MAX);
        assert_eq!(i16::from_le_bytes([b[2], b[3]]), -i16::MAX);
    }

    #[test]
    fn pcm_to_i16_le_clamps_overrange() {
        let b = pcm_to_i16_le(&[2.0, -3.0]);
        assert_eq!(i16::from_le_bytes([b[0], b[1]]), i16::MAX);
        assert_eq!(i16::from_le_bytes([b[2], b[3]]), -i16::MAX);
    }

    #[test]
    fn downmix_mono_passthrough() {
        let v = vec![0.5f32, -0.5, 0.25];
        assert_eq!(downmix(v.clone(), 1), v);
    }

    #[test]
    fn downmix_stereo_averages() {
        let v = vec![0.0f32, 1.0, 0.5, -0.5];
        let m = downmix(v, 2);
        assert!((m[0] - 0.5).abs() < 1e-6, "expected 0.5, got {}", m[0]);
        assert!((m[1] - 0.0).abs() < 1e-6, "expected 0.0, got {}", m[1]);
    }

    #[test]
    fn downmix_quad_averages() {
        // 4 channels: [1, 3, 5, 7] -> average = 4
        let v = vec![1.0f32, 3.0, 5.0, 7.0];
        let m = downmix(v, 4);
        assert_eq!(m.len(), 1);
        assert!((m[0] - 4.0).abs() < 1e-6);
    }

    #[test]
    fn resample_44100_to_16000() {
        let mut r = FftFixedIn::<f32>::new(44100, 16000, 1024, 2, 1).unwrap();
        let need = r.input_frames_next();
        let input = vec![0.0f32; need];
        let out = r.process(&[input], None).unwrap();
        assert!(!out[0].is_empty());
    }

    #[test]
    fn resample_48000_to_16000() {
        let mut r = FftFixedIn::<f32>::new(48000, 16000, 1024, 2, 1).unwrap();
        let need = r.input_frames_next();
        let input = vec![0.0f32; need];
        let out = r.process(&[input], None).unwrap();
        assert!(!out[0].is_empty());
    }

    #[test]
    fn resampler_flush_with_process_partial() {
        let mut r = FftFixedIn::<f32>::new(44100, 16000, 1024, 2, 1).unwrap();
        let partial = vec![0.0f32; 512]; // less than input_frames_next()
        let out = r.process_partial(Some(&[partial]), None).unwrap();
        // May or may not produce output, but must not error
        assert_eq!(out.len(), 1);
    }
}
