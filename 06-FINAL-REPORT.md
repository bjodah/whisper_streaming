  What was done

  Bug fixed

   - ServerConfig, InjectConfig, HotkeyConfig lacked #[serde(default)] at the struct level, causing TOML deserialization to fail when any field was omitted. Fixed by adding #[serde(default)] to each struct.

  src/lib.rs — created

  Re-exports all modules (audio, config, dbus, hotkey, inject, proxy) so the integration tests in tests/ can import from the crate.

  src/main.rs — updated

  Replaced mod x; declarations with use strisper_wayland::x; now that both a binary (main.rs) and library (lib.rs) target exist in the same crate.

  Supporting files created

  ┌────────────────────────────────────────────────────────────┬───────────────────────────────────────────────────────────────────────────────────────────────────┐
  │ File                                                       │ Purpose                                                                                           │
  ├────────────────────────────────────────────────────────────┼───────────────────────────────────────────────────────────────────────────────────────────────────┤
  │ gnome-extension/strisper@whisper-streaming/metadata.json   │ GNOME 45–50 compatible extension metadata                                                         │
  ├────────────────────────────────────────────────────────────┼───────────────────────────────────────────────────────────────────────────────────────────────────┤
  │ gnome-extension/.../extension.js                           │ Panel indicator + D-Bus proxy + hotkey binding                                                    │
  ├────────────────────────────────────────────────────────────┼───────────────────────────────────────────────────────────────────────────────────────────────────┤
  │ gnome-extension/.../stylesheet.css                         │ Red icon during recording                                                                         │
  ├────────────────────────────────────────────────────────────┼───────────────────────────────────────────────────────────────────────────────────────────────────┤
  │ gnome-extension/.../schemas/...gschema.xml                 │ GSettings schema (validated with glib-compile-schemas)                                            │
  ├────────────────────────────────────────────────────────────┼───────────────────────────────────────────────────────────────────────────────────────────────────┤
  │ strisper-wayland.service                                   │ systemd user unit                                                                                 │
  ├────────────────────────────────────────────────────────────┼───────────────────────────────────────────────────────────────────────────────────────────────────┤
  │ strisper-wayland.desktop                                   │ XDG desktop entry                                                                                 │
  ├────────────────────────────────────────────────────────────┼───────────────────────────────────────────────────────────────────────────────────────────────────┤
  │ install.sh                                                 │ Build + install script with ydotool warnings                                                      │
  ├────────────────────────────────────────────────────────────┼───────────────────────────────────────────────────────────────────────────────────────────────────┤
  │ README.md                                                  │ Full docs: prerequisites, quick start, GNOME install, non-GNOME evdev, D-Bus API, troubleshooting │
  └────────────────────────────────────────────────────────────┴───────────────────────────────────────────────────────────────────────────────────────────────────┘
