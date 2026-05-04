use std::sync::atomic::{AtomicBool, Ordering};

use anyhow::Context;
use tokio::sync::mpsc;
use zbus::{connection::Builder, interface, object_server::SignalContext, Connection};

const BUS_NAME: &str = "io.github.bjodah.StrisperWayland";
const OBJECT_PATH: &str = "/io/github/bjodah/StrisperWayland";

/// Commands sent from D-Bus method handlers to the main event loop.
pub enum Command {
    Start,
    Stop,
    Toggle,
}

/// D-Bus interface implementation. Method handlers do not change state
/// directly; they send a `Command` to the central event loop so that audio
/// and proxy lifecycle is managed in one place.
pub struct Strisper {
    pub recording: AtomicBool,
    pub cmd_tx: mpsc::UnboundedSender<Command>,
}

#[interface(name = "io.github.bjodah.StrisperWayland")]
impl Strisper {
    async fn toggle_recording(&self) -> bool {
        let _ = self.cmd_tx.send(Command::Toggle);
        self.recording.load(Ordering::SeqCst)
    }

    async fn start_recording(&self) {
        let _ = self.cmd_tx.send(Command::Start);
    }

    async fn stop_recording(&self) {
        let _ = self.cmd_tx.send(Command::Stop);
    }

    #[zbus(property)]
    async fn is_recording(&self) -> bool {
        self.recording.load(Ordering::SeqCst)
    }

    /// Fired whenever recording state changes. Subscribers (e.g. the GNOME
    /// extension) use this to update the panel indicator.
    #[zbus(signal)]
    async fn recording_state_changed(
        ctx: &SignalContext<'_>,
        recording: bool,
    ) -> zbus::Result<()>;
}

/// Register the D-Bus service and return the connection and an `InterfaceRef`
/// that can be used to emit signals from outside method handlers.
pub async fn start(
    cmd_tx: mpsc::UnboundedSender<Command>,
) -> anyhow::Result<(Connection, zbus::object_server::InterfaceRef<Strisper>)> {
    let iface = Strisper {
        recording: AtomicBool::new(false),
        cmd_tx,
    };

    let conn = Builder::session()
        .context("D-Bus session connection")?
        .name(BUS_NAME)
        .context("request bus name")?
        .serve_at(OBJECT_PATH, iface)
        .context("register object")?
        .build()
        .await
        .context("build D-Bus connection")?;

    let iface_ref = conn
        .object_server()
        .interface::<_, Strisper>(OBJECT_PATH)
        .await
        .context("get InterfaceRef")?;

    Ok((conn, iface_ref))
}

/// Update the `IsRecording` property and emit `RecordingStateChanged`.
///
/// The `InterfaceRef` holds the underlying RwLock; we lock it briefly to
/// update the atomic, then drop the guard before emitting the signal so
/// the object server can process the emission normally.
pub async fn set_recording(
    iface_ref: &zbus::object_server::InterfaceRef<Strisper>,
    new_state: bool,
) -> zbus::Result<()> {
    {
        let iface = iface_ref.get().await;
        iface.recording.store(new_state, Ordering::SeqCst);
    }
    Strisper::recording_state_changed(iface_ref.signal_context(), new_state).await
}
