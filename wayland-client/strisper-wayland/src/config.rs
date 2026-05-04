use std::path::{Path, PathBuf};

use anyhow::Context;
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct Config {
    #[serde(default)]
    pub server: ServerConfig,
    #[serde(default)]
    pub audio: AudioConfig,
    #[serde(default)]
    pub inject: InjectConfig,
    /// Used only on non-GNOME Wayland; GNOME hotkey is configured via GSettings.
    #[serde(default)]
    pub hotkey: HotkeyConfig,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
#[serde(default)]
pub struct ServerConfig {
    pub host: String,
    pub port: u16,
}

impl Default for ServerConfig {
    fn default() -> Self {
        Self {
            host: "127.0.0.1".to_string(),
            port: 43007,
        }
    }
}

#[derive(Debug, Clone, Deserialize, Serialize, Default)]
pub struct AudioConfig {
    pub device: String,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
#[serde(default)]
pub struct InjectConfig {
    pub method: String,
    pub delay_ms: u64,
}

impl Default for InjectConfig {
    fn default() -> Self {
        Self {
            method: "auto".to_string(),
            delay_ms: 12,
        }
    }
}

#[derive(Debug, Clone, Deserialize, Serialize)]
#[serde(default)]
pub struct HotkeyConfig {
    pub key: String,
}

impl Default for HotkeyConfig {
    fn default() -> Self {
        Self {
            key: "Ctrl+Shift+F9".to_string(),
        }
    }
}

impl Default for Config {
    fn default() -> Self {
        Self {
            server: ServerConfig::default(),
            audio: AudioConfig::default(),
            inject: InjectConfig::default(),
            hotkey: HotkeyConfig::default(),
        }
    }
}

pub fn default_config_path() -> PathBuf {
    let home = std::env::var("HOME").unwrap_or_else(|_| ".".to_string());
    PathBuf::from(home)
        .join(".config")
        .join("strisper-wayland")
        .join("config.toml")
}

/// Load config from `path`, or use defaults if the file does not exist.
pub fn load(path: Option<&Path>) -> anyhow::Result<Config> {
    let path = match path {
        Some(p) => p.to_path_buf(),
        None => default_config_path(),
    };
    if !path.exists() {
        return Ok(Config::default());
    }
    let content = std::fs::read_to_string(&path)
        .with_context(|| format!("failed to read config from {}", path.display()))?;
    let config: Config = toml::from_str(&content)
        .with_context(|| format!("failed to parse config from {}", path.display()))?;
    Ok(config)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    #[test]
    fn default_config_has_correct_values() {
        let cfg = Config::default();
        assert_eq!(cfg.server.host, "127.0.0.1");
        assert_eq!(cfg.server.port, 43007);
        assert_eq!(cfg.inject.method, "auto");
        assert_eq!(cfg.inject.delay_ms, 12);
        assert_eq!(cfg.hotkey.key, "Ctrl+Shift+F9");
        assert_eq!(cfg.audio.device, "");
    }

    #[test]
    fn loads_from_toml_string() {
        let s = r#"
[server]
host = "10.0.0.1"
port = 9000

[audio]
device = "pulse"

[inject]
method = "wtype"
delay_ms = 20

[hotkey]
key = "Ctrl+F12"
"#;
        let cfg: Config = toml::from_str(s).unwrap();
        assert_eq!(cfg.server.host, "10.0.0.1");
        assert_eq!(cfg.server.port, 9000);
        assert_eq!(cfg.audio.device, "pulse");
        assert_eq!(cfg.inject.method, "wtype");
        assert_eq!(cfg.inject.delay_ms, 20);
        assert_eq!(cfg.hotkey.key, "Ctrl+F12");
    }

    #[test]
    fn partial_toml_fills_defaults() {
        let s = "[server]\nport = 9999\n";
        let cfg: Config = toml::from_str(s).unwrap();
        assert_eq!(cfg.server.port, 9999);
        assert_eq!(cfg.server.host, "127.0.0.1");
        assert_eq!(cfg.hotkey.key, "Ctrl+Shift+F9");
    }

    #[test]
    fn load_returns_default_if_file_missing() {
        let cfg = load(Some(Path::new("/nonexistent/config.toml"))).unwrap();
        assert_eq!(cfg.server.port, 43007);
    }

    #[test]
    fn load_reads_file() {
        let path = std::env::temp_dir().join(format!("strisper-cfg-{}.toml", std::process::id()));
        {
            let mut f = std::fs::File::create(&path).unwrap();
            writeln!(f, "[server]").unwrap();
            writeln!(f, "port = 1234").unwrap();
        }
        let cfg = load(Some(&path)).unwrap();
        assert_eq!(cfg.server.port, 1234);
        std::fs::remove_file(&path).ok();
    }
}
