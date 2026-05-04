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

async fn inject_ydotool(text: &str) -> anyhow::Result<()> {
    let mut child = Command::new("ydotool")
        .args(["type", "--file", "-"])
        .stdin(Stdio::piped())
        .spawn()
        .context("failed to spawn ydotool")?;

    if let Some(mut stdin) = child.stdin.take() {
        stdin.write_all(text.as_bytes()).await?;
    }
    child.wait().await.context("ydotool exited with error")?;
    Ok(())
}

async fn inject_wtype(text: &str) -> anyhow::Result<()> {
    Command::new("wtype")
        .arg(text)
        .spawn()
        .context("failed to spawn wtype")?
        .wait()
        .await
        .context("wtype exited with error")?;
    Ok(())
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
