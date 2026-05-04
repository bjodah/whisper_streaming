use anyhow::Context;
use std::process::Stdio;
use tokio::io::AsyncWriteExt;
use tokio::process::Command;
use tracing::info;

#[derive(Debug, Clone, PartialEq)]
pub enum InjectMethod {
    Ydotool,
    Wtype,
}

/// Resolve the injection method. Explicit values (`"ydotool"`, `"wtype"`) take
/// priority; anything else triggers auto-detection.
pub fn detect_method(configured: &str) -> InjectMethod {
    match configured {
        "ydotool" => InjectMethod::Ydotool,
        "wtype" => InjectMethod::Wtype,
        _ => auto_detect(),
    }
}

fn auto_detect() -> InjectMethod {
    if is_gnome() {
        info!("GNOME detected → using ydotool");
        InjectMethod::Ydotool
    } else if is_wlroots() && cmd_exists("wtype") {
        info!("wlroots + wtype detected → using wtype");
        InjectMethod::Wtype
    } else {
        info!("defaulting to ydotool");
        InjectMethod::Ydotool
    }
}

/// Returns `true` when running under GNOME Shell.
pub fn is_gnome() -> bool {
    let desktop = std::env::var("XDG_CURRENT_DESKTOP")
        .unwrap_or_default()
        .to_lowercase();
    let session = std::env::var("DESKTOP_SESSION")
        .unwrap_or_default()
        .to_lowercase();
    desktop.contains("gnome") || session.contains("gnome")
}

fn is_wlroots() -> bool {
    let desktop = std::env::var("XDG_CURRENT_DESKTOP")
        .unwrap_or_default()
        .to_lowercase();
    ["sway", "hyprland", "river", "wayfire", "labwc"]
        .iter()
        .any(|&d| desktop.contains(d))
}

fn cmd_exists(cmd: &str) -> bool {
    std::process::Command::new("which")
        .arg(cmd)
        .output()
        .map(|o| o.status.success())
        .unwrap_or(false)
}

/// Inject `text` into the currently focused window using the given method.
/// Waits `delay_ms` before injecting to allow focus to settle.
pub async fn inject_text(text: &str, method: &InjectMethod, delay_ms: u64) -> anyhow::Result<()> {
    if delay_ms > 0 {
        tokio::time::sleep(std::time::Duration::from_millis(delay_ms)).await;
    }
    match method {
        InjectMethod::Ydotool => inject_ydotool(text).await,
        InjectMethod::Wtype => inject_wtype(text).await,
    }
}

#[derive(Default)]
pub struct TranscriptSpacing {
    needs_separator: bool,
}

impl TranscriptSpacing {
    pub fn reset(&mut self) {
        self.needs_separator = false;
    }

    pub fn apply(&mut self, text: &str) -> String {
        if text.is_empty() {
            return String::new();
        }

        let prepend_space = self.needs_separator
            && !text.starts_with(char::is_whitespace)
            && !starts_without_separator(text);

        let mut out = String::with_capacity(text.len() + usize::from(prepend_space));
        if prepend_space {
            out.push(' ');
        }
        out.push_str(text);

        self.needs_separator = ends_needing_separator(text);
        out
    }
}

fn starts_without_separator(text: &str) -> bool {
    matches!(
        text.chars().next(),
        Some('.' | ',' | '!' | '?' | ';' | ':' | '%' | ')' | ']' | '}')
    )
}

fn ends_needing_separator(text: &str) -> bool {
    if text.ends_with(char::is_whitespace) {
        return false;
    }

    text.chars()
        .rev()
        .find(|c| !c.is_whitespace())
        .is_some_and(|c| !matches!(c, '(' | '[' | '{'))
}

async fn inject_ydotool(text: &str) -> anyhow::Result<()> {
    for segment in ydotool_segments(text) {
        match segment {
            YdotoolSegment::Text(text) => inject_ydotool_text(&text).await?,
            YdotoolSegment::Keys(keys) => inject_ydotool_keys(&keys).await?,
        }
    }

    Ok(())
}

async fn inject_ydotool_text(text: &str) -> anyhow::Result<()> {
    let mut child = Command::new("ydotool")
        .args(["type", "--file", "-"])
        .stdin(Stdio::piped())
        .spawn()
        .context("failed to spawn ydotool")?;

    if let Some(mut stdin) = child.stdin.take() {
        stdin.write_all(text.as_bytes()).await?;
    }
    let status = child.wait().await.context("ydotool exited with error")?;
    anyhow::ensure!(status.success(), "ydotool type failed with {status}");
    Ok(())
}

async fn inject_ydotool_keys(keys: &[&'static str]) -> anyhow::Result<()> {
    let status = Command::new("ydotool")
        .arg("key")
        .args(keys)
        .spawn()
        .context("failed to spawn ydotool")?
        .wait()
        .await
        .context("ydotool exited with error")?;
    anyhow::ensure!(status.success(), "ydotool key failed with {status}");
    Ok(())
}

async fn inject_wtype(text: &str) -> anyhow::Result<()> {
    let status = Command::new("wtype")
        .arg(text)
        .spawn()
        .context("failed to spawn wtype")?
        .wait()
        .await
        .context("wtype exited with error")?;
    anyhow::ensure!(status.success(), "wtype failed with {status}");
    Ok(())
}

#[derive(Debug, PartialEq)]
enum YdotoolSegment {
    Text(String),
    Keys(Vec<&'static str>),
}

fn ydotool_segments(text: &str) -> Vec<YdotoolSegment> {
    let mut segments = Vec::new();
    let mut text_buf = String::new();
    let mut keys_buf = Vec::new();

    let flush_text = |segments: &mut Vec<YdotoolSegment>, text_buf: &mut String| {
        if !text_buf.is_empty() {
            segments.push(YdotoolSegment::Text(std::mem::take(text_buf)));
        }
    };
    let flush_keys = |segments: &mut Vec<YdotoolSegment>, keys_buf: &mut Vec<&'static str>| {
        if !keys_buf.is_empty() {
            segments.push(YdotoolSegment::Keys(std::mem::take(keys_buf)));
        }
    };

    for ch in text.chars() {
        let key = match ch {
            // ydotool type can drop ASCII whitespace with some versions/layouts.
            // Emit those keys explicitly via evdev keycodes.
            ' ' => Some(("57:1", "57:0")),  // KEY_SPACE
            '\n' => Some(("28:1", "28:0")), // KEY_ENTER
            '\t' => Some(("15:1", "15:0")), // KEY_TAB
            _ => None,
        };

        if let Some((down, up)) = key {
            flush_text(&mut segments, &mut text_buf);
            keys_buf.push(down);
            keys_buf.push(up);
        } else {
            flush_keys(&mut segments, &mut keys_buf);
            text_buf.push(ch);
        }
    }

    flush_text(&mut segments, &mut text_buf);
    flush_keys(&mut segments, &mut keys_buf);
    segments
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn detect_explicit_ydotool() {
        assert_eq!(detect_method("ydotool"), InjectMethod::Ydotool);
    }

    #[test]
    fn detect_explicit_wtype() {
        assert_eq!(detect_method("wtype"), InjectMethod::Wtype);
    }

    #[test]
    fn detect_auto_returns_something() {
        // Just ensure it doesn't panic with unknown method names.
        let _ = detect_method("auto");
        let _ = detect_method("unknown");
    }

    #[test]
    fn transcript_spacing_separates_word_events() {
        let mut spacing = TranscriptSpacing::default();
        assert_eq!(spacing.apply("However..."), "However...");
        assert_eq!(spacing.apply("there"), " there");
        assert_eq!(spacing.apply("is"), " is");
        assert_eq!(spacing.apply("one"), " one");
    }

    #[test]
    fn transcript_spacing_does_not_duplicate_existing_space() {
        let mut spacing = TranscriptSpacing::default();
        assert_eq!(spacing.apply("hello "), "hello ");
        assert_eq!(spacing.apply("world"), "world");
    }

    #[test]
    fn transcript_spacing_does_not_insert_before_punctuation() {
        let mut spacing = TranscriptSpacing::default();
        assert_eq!(spacing.apply("hello"), "hello");
        assert_eq!(spacing.apply(","), ",");
        assert_eq!(spacing.apply("world"), " world");
    }

    #[test]
    fn ydotool_segments_emit_spaces_as_key_events() {
        assert_eq!(
            ydotool_segments("hello world"),
            vec![
                YdotoolSegment::Text("hello".to_string()),
                YdotoolSegment::Keys(vec!["57:1", "57:0"]),
                YdotoolSegment::Text("world".to_string()),
            ]
        );
    }

    #[test]
    fn ydotool_segments_preserve_repeated_whitespace_order() {
        assert_eq!(
            ydotool_segments("a  b\nc\td"),
            vec![
                YdotoolSegment::Text("a".to_string()),
                YdotoolSegment::Keys(vec!["57:1", "57:0", "57:1", "57:0"]),
                YdotoolSegment::Text("b".to_string()),
                YdotoolSegment::Keys(vec!["28:1", "28:0"]),
                YdotoolSegment::Text("c".to_string()),
                YdotoolSegment::Keys(vec!["15:1", "15:0"]),
                YdotoolSegment::Text("d".to_string()),
            ]
        );
    }

    #[test]
    fn is_gnome_detects_gnome_desktop() {
        // Use a subprocess so we don't mutate the test process env (which
        // could race with other tests running in parallel).
        let out = std::process::Command::new("env")
            .env("XDG_CURRENT_DESKTOP", "GNOME")
            .env("DESKTOP_SESSION", "")
            .arg("true")
            .output()
            .unwrap();
        assert!(out.status.success());
    }
}
