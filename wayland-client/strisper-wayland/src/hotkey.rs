use anyhow::Context;
use evdev::{Device, InputEventKind, Key};
use std::collections::HashSet;
use tokio::sync::mpsc;
use tracing::{debug, info, warn};

/// A parsed hotkey specification such as `"Ctrl+Shift+F9"`.
#[derive(Debug, Clone)]
pub struct HotkeySpec {
    pub modifiers: ModifierSet,
    pub trigger: Key,
}

#[derive(Debug, Clone, Default)]
pub struct ModifierSet {
    pub ctrl: bool,
    pub shift: bool,
    pub alt: bool,
    pub meta: bool,
}

impl HotkeySpec {
    /// Parse a `+`-separated spec. Recognised modifiers: `Ctrl`, `Shift`,
    /// `Alt`, `Super`/`Meta`. Recognised trigger keys: `F1`–`F12`.
    pub fn parse(spec: &str) -> anyhow::Result<Self> {
        let mut modifiers = ModifierSet::default();
        let mut trigger: Option<Key> = None;

        for part in spec.split('+') {
            match part.trim() {
                "Ctrl" | "ctrl" | "Control" => modifiers.ctrl = true,
                "Shift" | "shift" => modifiers.shift = true,
                "Alt" | "alt" => modifiers.alt = true,
                "Super" | "super" | "Meta" | "meta" => modifiers.meta = true,
                key => {
                    trigger = Some(
                        parse_key(key)
                            .with_context(|| format!("unknown key token '{key}'"))?,
                    );
                }
            }
        }

        let trigger = trigger.context("hotkey spec has no trigger key (expected e.g. F9)")?;
        Ok(Self { modifiers, trigger })
    }
}

fn parse_key(s: &str) -> anyhow::Result<Key> {
    // Function keys F1-F12 (non-consecutive in scan codes).
    match s {
        "F1" => return Ok(Key::KEY_F1),
        "F2" => return Ok(Key::KEY_F2),
        "F3" => return Ok(Key::KEY_F3),
        "F4" => return Ok(Key::KEY_F4),
        "F5" => return Ok(Key::KEY_F5),
        "F6" => return Ok(Key::KEY_F6),
        "F7" => return Ok(Key::KEY_F7),
        "F8" => return Ok(Key::KEY_F8),
        "F9" => return Ok(Key::KEY_F9),
        "F10" => return Ok(Key::KEY_F10),
        "F11" => return Ok(Key::KEY_F11),
        "F12" => return Ok(Key::KEY_F12),
        _ => {}
    }
    anyhow::bail!("unsupported key '{s}'; supported: F1–F12")
}

/// Start evdev hotkey listeners for all keyboard devices in `/dev/input/`.
/// Returns a channel that fires `()` each time the hotkey is pressed.
pub fn start_listener(spec: HotkeySpec) -> anyhow::Result<mpsc::UnboundedReceiver<()>> {
    let keyboards = find_keyboards().context("could not enumerate keyboard devices")?;
    if keyboards.is_empty() {
        anyhow::bail!("no keyboard devices found in /dev/input/");
    }

    let (tx, rx) = mpsc::unbounded_channel::<()>();

    for path in keyboards {
        let spec_clone = spec.clone();
        let tx_clone = tx.clone();
        tokio::task::spawn_blocking(move || {
            listen_device(&path, &spec_clone, tx_clone);
        });
    }

    Ok(rx)
}

fn find_keyboards() -> anyhow::Result<Vec<std::path::PathBuf>> {
    let mut keyboards = Vec::new();
    for entry in std::fs::read_dir("/dev/input").context("cannot read /dev/input")? {
        let entry = entry?;
        let path = entry.path();
        if path
            .file_name()
            .and_then(|n| n.to_str())
            .map(|n| n.starts_with("event"))
            .unwrap_or(false)
        {
            if let Ok(dev) = Device::open(&path) {
                if dev.supported_keys().is_some() {
                    keyboards.push(path);
                }
            }
        }
    }
    Ok(keyboards)
}

fn listen_device(path: &std::path::Path, spec: &HotkeySpec, tx: mpsc::UnboundedSender<()>) {
    let mut dev = match Device::open(path) {
        Ok(d) => d,
        Err(e) => {
            warn!("cannot open {}: {e}", path.display());
            return;
        }
    };

    info!("watching hotkeys on {}", path.display());

    let trigger = spec.trigger;
    let need_ctrl = spec.modifiers.ctrl;
    let need_shift = spec.modifiers.shift;
    let need_alt = spec.modifiers.alt;
    let need_meta = spec.modifiers.meta;

    let mut held: HashSet<u16> = HashSet::new();

    loop {
        let events = match dev.fetch_events() {
            Ok(e) => e,
            Err(e) => {
                debug!("evdev error on {}: {e}", path.display());
                break;
            }
        };

        for event in events {
            if let InputEventKind::Key(key) = event.kind() {
                let code = key.code();
                match event.value() {
                    1 => {
                        // Key pressed
                        held.insert(code);
                        if key == trigger {
                            let ctrl_ok = !need_ctrl
                                || held.contains(&Key::KEY_LEFTCTRL.code())
                                || held.contains(&Key::KEY_RIGHTCTRL.code());
                            let shift_ok = !need_shift
                                || held.contains(&Key::KEY_LEFTSHIFT.code())
                                || held.contains(&Key::KEY_RIGHTSHIFT.code());
                            let alt_ok = !need_alt
                                || held.contains(&Key::KEY_LEFTALT.code())
                                || held.contains(&Key::KEY_RIGHTALT.code());
                            let meta_ok = !need_meta
                                || held.contains(&Key::KEY_LEFTMETA.code())
                                || held.contains(&Key::KEY_RIGHTMETA.code());
                            if ctrl_ok && shift_ok && alt_ok && meta_ok {
                                let _ = tx.send(());
                            }
                        }
                    }
                    0 => {
                        // Key released
                        held.remove(&code);
                    }
                    _ => {} // key repeat — ignore
                }
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_ctrl_shift_f9() {
        let spec = HotkeySpec::parse("Ctrl+Shift+F9").unwrap();
        assert!(spec.modifiers.ctrl);
        assert!(spec.modifiers.shift);
        assert!(!spec.modifiers.alt);
        assert!(!spec.modifiers.meta);
        assert_eq!(spec.trigger, Key::KEY_F9);
    }

    #[test]
    fn parse_ctrl_f1() {
        let spec = HotkeySpec::parse("Ctrl+F1").unwrap();
        assert!(spec.modifiers.ctrl);
        assert!(!spec.modifiers.shift);
        assert_eq!(spec.trigger, Key::KEY_F1);
    }

    #[test]
    fn parse_f12_only() {
        let spec = HotkeySpec::parse("F12").unwrap();
        assert!(!spec.modifiers.ctrl);
        assert!(!spec.modifiers.shift);
        assert_eq!(spec.trigger, Key::KEY_F12);
    }

    #[test]
    fn parse_all_modifiers() {
        let spec = HotkeySpec::parse("Ctrl+Shift+Alt+Super+F5").unwrap();
        assert!(spec.modifiers.ctrl);
        assert!(spec.modifiers.shift);
        assert!(spec.modifiers.alt);
        assert!(spec.modifiers.meta);
        assert_eq!(spec.trigger, Key::KEY_F5);
    }

    #[test]
    fn parse_no_trigger_fails() {
        assert!(HotkeySpec::parse("Ctrl+Shift").is_err());
    }

    #[test]
    fn parse_unknown_key_fails() {
        assert!(HotkeySpec::parse("Ctrl+PageUp").is_err());
    }

    #[test]
    fn parse_empty_fails() {
        assert!(HotkeySpec::parse("").is_err());
    }

    #[test]
    fn all_function_keys_parse() {
        for n in 1..=12u8 {
            let spec = format!("F{n}");
            assert!(HotkeySpec::parse(&spec).is_ok(), "F{n} should parse");
        }
    }
}
