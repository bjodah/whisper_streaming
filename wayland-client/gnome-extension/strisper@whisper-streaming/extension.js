/* Strisper Wayland GNOME Shell Extension
 *
 * Listens on D-Bus for io.github.bjodah.StrisperWayland and shows a
 * panel indicator that reflects the recording state.
 */
'use strict';

import GObject from 'gi://GObject';
import St from 'gi://St';
import Gio from 'gi://Gio';
import GLib from 'gi://GLib';
import Clutter from 'gi://Clutter';

import {Extension, gettext as _} from 'resource:///org/gnome/shell/extensions/extension.js';
import * as PanelMenu from 'resource:///org/gnome/shell/ui/panelMenu.js';
import * as PopupMenu from 'resource:///org/gnome/shell/ui/popupMenu.js';
import * as Main from 'resource:///org/gnome/shell/ui/main.js';

// D-Bus interface XML for strisper-wayland.
const STRISPER_IFACE = `
<node>
  <interface name="io.github.bjodah.StrisperWayland">
    <method name="StartRecording"/>
    <method name="StopRecording"/>
    <method name="ToggleRecording"/>
    <property name="Recording" type="b" access="read"/>
    <signal name="RecordingStateChanged">
      <arg type="b" name="recording"/>
    </signal>
  </interface>
</node>`;

const BUS_NAME    = 'io.github.bjodah.StrisperWayland';
const OBJECT_PATH = '/io/github/bjodah/StrisperWayland';

const StrisperProxy = Gio.DBusProxy.makeProxyWrapper(STRISPER_IFACE);

// Panel indicator widget.
const StrisperIndicator = GObject.registerClass(
class StrisperIndicator extends PanelMenu.Button {
    _init(extension) {
        super._init(0.0, _('Strisper'));
        this._ext = extension;

        this._icon = new St.Icon({
            icon_name: 'audio-input-microphone-symbolic',
            style_class: 'system-status-icon',
        });
        this.add_child(this._icon);

        // Toggle menu item.
        this._toggleItem = new PopupMenu.PopupMenuItem(_('Toggle Recording'));
        this._toggleItem.connect('activate', () => {
            if (this._proxy)
                this._proxy.ToggleRecordingRemote(() => {});
        });
        this.menu.addMenuItem(this._toggleItem);

        this._recording = false;
        this._proxy = null;
        this._watchId = 0;
        this._connectDBus();
    }

    _connectDBus() {
        // Watch the bus name so we can show/hide based on daemon presence.
        this._watchId = Gio.bus_watch_name(
            Gio.BusType.SESSION,
            BUS_NAME,
            Gio.BusNameWatcherFlags.NONE,
            this._onNameAppeared.bind(this),
            this._onNameVanished.bind(this),
        );
    }

    _onNameAppeared(_connection, _name, _owner) {
        this.show();
        this._proxy = new StrisperProxy(
            Gio.DBus.session,
            BUS_NAME,
            OBJECT_PATH,
            this._onProxyReady.bind(this),
        );
    }

    _onProxyReady(proxy, error) {
        if (error) {
            logError(error, 'StrisperProxy');
            return;
        }
        // Subscribe to the RecordingStateChanged signal.
        proxy.connectSignal('RecordingStateChanged', (_proxy, _sender, [recording]) => {
            this._setRecording(recording);
        });
        // Read initial state.
        const recording = proxy.Recording;
        if (recording !== null)
            this._setRecording(recording);
    }

    _onNameVanished() {
        this._proxy = null;
        this._setRecording(false);
        this.hide();
    }

    _setRecording(recording) {
        this._recording = recording;
        if (recording) {
            this._icon.icon_name = 'media-record-symbolic';
            this._icon.add_style_class_name('strisper-recording');
        } else {
            this._icon.icon_name = 'audio-input-microphone-symbolic';
            this._icon.remove_style_class_name('strisper-recording');
        }
    }

    _handleHotkey() {
        if (this._proxy)
            this._proxy.ToggleRecordingRemote(() => {});
    }

    destroy() {
        if (this._watchId) {
            Gio.bus_unwatch_name(this._watchId);
            this._watchId = 0;
        }
        super.destroy();
    }
});

export default class StrisperExtension extends Extension {
    enable() {
        this._settings = this.getSettings();
        this._indicator = new StrisperIndicator(this);
        Main.panel.addToStatusArea(this.uuid, this._indicator);

        // Register GNOME keybinding.
        this._bindHotkey();
        this._settings.connect('changed::hotkey', () => this._rebindHotkey());
    }

    _bindHotkey() {
        Main.wm.addKeybinding(
            'hotkey',
            this._settings,
            Meta.KeyBindingFlags.NONE,
            Shell.ActionMode.NORMAL | Shell.ActionMode.OVERVIEW,
            () => this._indicator._handleHotkey(),
        );
    }

    _rebindHotkey() {
        Main.wm.removeKeybinding('hotkey');
        this._bindHotkey();
    }

    disable() {
        Main.wm.removeKeybinding('hotkey');
        if (this._indicator) {
            this._indicator.destroy();
            this._indicator = null;
        }
        this._settings = null;
    }
}
