//! Integration tests for strisper-wayland.
//!
//! These tests exercise the pure/computational layers (config parsing, PCM
//! conversion, transcript parsing, hotkey parsing) without requiring a live
//! audio device, D-Bus session, or network connection.

use rubato::{FftFixedIn, Resampler};
use strisper_wayland::{hotkey::HotkeySpec, inject::InjectMethod, proxy};

// --------------------------------------------------------------------------
// Config tests
// --------------------------------------------------------------------------

#[test]
fn default_config_is_sane() {
    use strisper_wayland::config::{load, Config};
    let cfg = Config::default();
    assert_eq!(cfg.server.host, "127.0.0.1");
    assert_eq!(cfg.server.port, 43007);
    assert_eq!(cfg.inject.method, "auto");
    assert_eq!(cfg.inject.delay_ms, 12);
    assert_eq!(cfg.hotkey.key, "Ctrl+Shift+F9");

    // Loading a nonexistent path must return defaults.
    let cfg2 = load(Some(std::path::Path::new("/no/such/path.toml"))).unwrap();
    assert_eq!(cfg2.server.port, cfg.server.port);
}

#[test]
fn config_roundtrips_through_toml() {
    use strisper_wayland::config::Config;

    let original = r#"
[server]
host = "192.168.1.5"
port = 8080

[audio]
device = "hw:0,0"

[inject]
method = "wtype"
delay_ms = 50

[hotkey]
key = "Ctrl+Alt+F10"
"#;
    let cfg: Config = toml::from_str(original).unwrap();
    assert_eq!(cfg.server.host, "192.168.1.5");
    assert_eq!(cfg.server.port, 8080);
    assert_eq!(cfg.audio.device, "hw:0,0");
    assert_eq!(cfg.inject.method, "wtype");
    assert_eq!(cfg.inject.delay_ms, 50);
    assert_eq!(cfg.hotkey.key, "Ctrl+Alt+F10");
}

// --------------------------------------------------------------------------
// PCM conversion tests
// --------------------------------------------------------------------------

#[test]
fn pcm_to_i16_le_is_correct() {
    let cases: &[(&[f32], &[i16])] = &[
        (&[0.0], &[0]),
        (&[1.0], &[i16::MAX]),
        (&[-1.0], &[-i16::MAX]),
        (&[2.0], &[i16::MAX]),   // clamped
        (&[-2.0], &[-i16::MAX]), // clamped
    ];
    for (input, expected) in cases {
        let bytes = proxy::pcm_to_i16_le(input);
        let decoded: Vec<i16> = bytes
            .chunks_exact(2)
            .map(|b| i16::from_le_bytes([b[0], b[1]]))
            .collect();
        assert_eq!(&decoded, expected, "mismatch for input {input:?}");
    }
}

#[test]
fn pcm_to_i16_le_length_is_double() {
    let input = vec![0.0f32; 100];
    let bytes = proxy::pcm_to_i16_le(&input);
    assert_eq!(bytes.len(), 200);
}

// --------------------------------------------------------------------------
// Transcript parsing tests
// --------------------------------------------------------------------------

#[test]
fn parse_transcript_line_extracts_text() {
    assert_eq!(proxy::parse_transcript_line("100 200 hello world"), "hello world");
    assert_eq!(proxy::parse_transcript_line("0 1 word"), "word");
    assert_eq!(
        proxy::parse_transcript_line("1000 2500 multiple words here"),
        "multiple words here"
    );
}

#[test]
fn parse_transcript_line_handles_edge_cases() {
    assert_eq!(proxy::parse_transcript_line(""), "");
    assert_eq!(proxy::parse_transcript_line("0 0"), "");
    assert_eq!(proxy::parse_transcript_line("0 500 "), "");
}

// --------------------------------------------------------------------------
// Hotkey parsing tests
// --------------------------------------------------------------------------

#[test]
fn hotkey_default_binding_parses() {
    use evdev::Key;
    let spec = HotkeySpec::parse("Ctrl+Shift+F9").unwrap();
    assert!(spec.modifiers.ctrl);
    assert!(spec.modifiers.shift);
    assert!(!spec.modifiers.alt);
    assert!(!spec.modifiers.meta);
    assert_eq!(spec.trigger, Key::KEY_F9);
}

#[test]
fn hotkey_all_function_keys_parse() {
    for n in 1u8..=12 {
        let s = format!("F{n}");
        assert!(HotkeySpec::parse(&s).is_ok(), "failed to parse {s}");
    }
}

#[test]
fn hotkey_all_modifiers_parse() {
    let spec = HotkeySpec::parse("Ctrl+Shift+Alt+Super+F5").unwrap();
    assert!(spec.modifiers.ctrl);
    assert!(spec.modifiers.shift);
    assert!(spec.modifiers.alt);
    assert!(spec.modifiers.meta);
}

#[test]
fn hotkey_no_trigger_is_error() {
    assert!(HotkeySpec::parse("Ctrl+Shift").is_err());
}

#[test]
fn hotkey_unknown_key_is_error() {
    assert!(HotkeySpec::parse("Ctrl+PageDown").is_err());
}

// --------------------------------------------------------------------------
// Inject method detection
// --------------------------------------------------------------------------

#[test]
fn inject_explicit_methods_respected() {
    use strisper_wayland::inject::detect_method;
    assert_eq!(detect_method("ydotool"), InjectMethod::Ydotool);
    assert_eq!(detect_method("wtype"), InjectMethod::Wtype);
}

// --------------------------------------------------------------------------
// Resampler integration
// --------------------------------------------------------------------------

#[test]
fn resampler_44100_to_16000_runs() {
    let mut r = FftFixedIn::<f32>::new(44100, 16000, 1024, 2, 1).unwrap();
    let need = r.input_frames_next();
    let input = vec![0.0f32; need];
    let out = r.process(&[input], None).unwrap();
    assert!(!out[0].is_empty(), "expected non-empty output");
}

#[test]
fn resampler_48000_to_16000_runs() {
    let mut r = FftFixedIn::<f32>::new(48000, 16000, 1024, 2, 1).unwrap();
    let need = r.input_frames_next();
    let input = vec![0.0f32; need];
    let out = r.process(&[input], None).unwrap();
    assert!(!out[0].is_empty());
}

#[test]
fn resampler_process_partial_flush() {
    let mut r = FftFixedIn::<f32>::new(44100, 16000, 1024, 2, 1).unwrap();
    let partial = vec![0.0f32; 256];
    let out = r.process_partial(Some(&[partial]), None).unwrap();
    assert_eq!(out.len(), 1, "should return one channel");
}

#[test]
fn resampler_undersized_input_is_error() {
    // Verify the exact failure mode documented in §0 of the final plan:
    // FftFixedIn returns Err on undersized input.
    let mut r = FftFixedIn::<f32>::new(44100, 16000, 1024, 2, 1).unwrap();
    let need = r.input_frames_next();
    assert!(need > 0);
    let too_small = vec![0.0f32; need - 1];
    let result = r.process(&[too_small], None);
    assert!(result.is_err(), "expected Err on undersized input, got Ok");
}
