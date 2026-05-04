#!/usr/bin/env bash
# install.sh — Installs strisper-wayland and its supporting files.
#
# Usage:
#   ./install.sh [--gnome] [--no-service] [--prefix PREFIX]
#
# Options:
#   --gnome        Also install the GNOME Shell extension.
#   --no-service   Skip installing the systemd user service.
#   --prefix DIR   Binary install prefix (default: $HOME/.local).
#
# Prerequisites (Ubuntu 22.04+ / Debian):
#   sudo apt install libasound2-dev pkg-config build-essential
#   # For ydotool injection:
#   sudo apt install ydotool
#   sudo systemctl enable --now ydotool
#   sudo usermod -aG input $USER   # then log out / in
#   # For wtype injection (alternative):
#   sudo apt install wtype
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PREFIX="$HOME/.local"
INSTALL_GNOME=false
INSTALL_SERVICE=true

for arg in "$@"; do
    case "$arg" in
        --gnome)       INSTALL_GNOME=true ;;
        --no-service)  INSTALL_SERVICE=false ;;
        --prefix=*)    PREFIX="${arg#--prefix=}" ;;
        --prefix)      shift; PREFIX="$1" ;;
        -h|--help)
            grep '^#' "$0" | sed 's/^# \?//'
            exit 0 ;;
        *) echo "Unknown option: $arg" >&2; exit 1 ;;
    esac
done

BIN_DIR="$PREFIX/bin"
EXT_DIR="$HOME/.local/share/gnome-shell/extensions"
SYSTEMD_DIR="$HOME/.config/systemd/user"
APP_DIR="$HOME/.local/share/applications"

echo "==> Building strisper-wayland…"
cargo build --manifest-path "$SCRIPT_DIR/strisper-wayland/Cargo.toml" --release

echo "==> Installing binary to $BIN_DIR"
mkdir -p "$BIN_DIR"
install -m 0755 "$SCRIPT_DIR/strisper-wayland/target/release/strisper-wayland" "$BIN_DIR/strisper-wayland"

echo "==> Installing .desktop file"
mkdir -p "$APP_DIR"
install -m 0644 "$SCRIPT_DIR/strisper-wayland.desktop" "$APP_DIR/strisper-wayland.desktop"

if $INSTALL_SERVICE; then
    echo "==> Installing systemd user service"
    mkdir -p "$SYSTEMD_DIR"
    install -m 0644 "$SCRIPT_DIR/strisper-wayland.service" "$SYSTEMD_DIR/strisper-wayland.service"
    systemctl --user daemon-reload
    echo "    Enable with: systemctl --user enable --now strisper-wayland"
fi

if $INSTALL_GNOME; then
    echo "==> Installing GNOME Shell extension"
    EXT_UUID="strisper@whisper-streaming"
    EXT_DEST="$EXT_DIR/$EXT_UUID"
    mkdir -p "$EXT_DEST"
    cp -r "$SCRIPT_DIR/gnome-extension/$EXT_UUID/." "$EXT_DEST/"

    echo "==> Compiling GSettings schema"
    glib-compile-schemas "$EXT_DEST/schemas"
    echo "    Enable with: gnome-extensions enable $EXT_UUID"
    echo "    (You may need to restart GNOME Shell first: Alt+F2 → r → Enter)"
fi

# Warn if ydotool service is not running.
if command -v ydotool &>/dev/null; then
    if ! systemctl --user is-active --quiet ydotool.service 2>/dev/null && \
       ! systemctl       is-active --quiet ydotool.service 2>/dev/null; then
        echo ""
        echo "WARNING: ydotool is installed but ydotool.service is not running."
        echo "         Text injection via ydotool will fail until the service is started."
        echo "         Run:  sudo systemctl enable --now ydotool"
        echo "         Also: sudo usermod -aG input \$USER  (then log out/in)"
    fi
fi

echo ""
echo "Installation complete."
echo "Run 'strisper-wayland --help' for usage."
