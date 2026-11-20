//! Lifecycle of the Go sidecar (`cmd/droplink-sidecar`).
//!
//! Same shape as PrivMeet's backend manager: a child process started with a
//! list of arguments (never a shell), readiness reported by the child, and a
//! stop on quit. Three things keep it from leaking:
//! - `kill_on_drop`, so dropping the handle kills the process;
//! - an explicit kill on `RunEvent::Exit`;
//! - `-watch-stdin`: the sidecar exits by itself when our end of its stdin
//!   closes, which also covers this shell crashing.

use std::path::PathBuf;
use std::process::Stdio;
use std::sync::Mutex;
use std::time::Duration;

use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Emitter, Manager};
use tokio::io::{AsyncBufReadExt, BufReader};
use tokio::process::{Child, Command};

/// Start of the sidecar's ready line; must match `server.ReadyPrefix` in Go.
pub const READY_PREFIX: &str = "DROPLINK_READY ";

/// How long the sidecar gets to print its ready line.
const READY_TIMEOUT: Duration = Duration::from_secs(10);

/// The JSON after [`READY_PREFIX`].
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Ready {
    pub port: u16,
    pub token: String,
    pub version: String,
}

/// Parses one stdout line; `None` for anything that is not a valid ready line.
pub fn parse_ready(line: &str) -> Option<Ready> {
    let json = line.trim().strip_prefix(READY_PREFIX)?;
    let ready: Ready = serde_json::from_str(json).ok()?;
    // A real token is 64 hex chars; anything short is not ours.
    if ready.port == 0 || ready.token.len() < 32 {
        return None;
    }
    Some(ready)
}

/// What `sidecar_info` returns to the UI.
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct SidecarInfo {
    /// "starting", "ready" or "failed".
    pub state: &'static str,
    pub port: Option<u16>,
    pub token: Option<String>,
    pub version: Option<String>,
    pub error: Option<String>,
}

#[derive(Default)]
struct Inner {
    child: Option<Child>,
    ready: Option<Ready>,
    error: Option<String>,
}

/// Tauri-managed state holding the running sidecar.
#[derive(Default)]
pub struct Sidecar {
    inner: Mutex<Inner>,
}

impl Sidecar {
    pub fn info(&self) -> SidecarInfo {
        let inner = self.inner.lock().unwrap_or_else(|e| e.into_inner());
        match (&inner.ready, &inner.error) {
            (Some(r), _) => SidecarInfo {
                state: "ready",
                port: Some(r.port),
                token: Some(r.token.clone()),
                version: Some(r.version.clone()),
                error: None,
            },
            (None, Some(e)) => SidecarInfo {
                state: "failed",
                port: None,
                token: None,
                version: None,
                error: Some(e.clone()),
            },
            _ => SidecarInfo {
                state: "starting",
                port: None,
                token: None,
                version: None,
                error: None,
            },
        }
    }

    /// Kills the sidecar if it runs. Safe to call more than once.
    pub fn stop(&self) {
        let mut inner = self.inner.lock().unwrap_or_else(|e| e.into_inner());
        if let Some(mut child) = inner.child.take() {
            let _ = child.start_kill();
        }
        inner.ready = None;
        inner.error = None;
    }

    fn set(&self, child: Option<Child>, ready: Option<Ready>, error: Option<String>) {
        let mut inner = self.inner.lock().unwrap_or_else(|e| e.into_inner());
        if let Some(mut old) = inner.child.take() {
            let _ = old.start_kill();
        }
        *inner = Inner { child, ready, error };
    }
}

/// Where the bundled sidecar sits: next to the app's own executable. Tauri
/// copies `externalBin` there, without the target-triple suffix, in both
/// `tauri dev` and installed builds.
pub fn binary_path() -> std::io::Result<PathBuf> {
    let exe = std::env::current_exe()?;
    let dir = exe
        .parent()
        .ok_or_else(|| std::io::Error::other("executable has no parent directory"))?;
    let name = if cfg!(windows) {
        "droplink-sidecar.exe"
    } else {
        "droplink-sidecar"
    };
    Ok(dir.join(name))
}

/// Starts the sidecar, records the result in [`Sidecar`] state and emits
/// `sidecar-ready` or `sidecar-failed` to the UI.
pub async fn launch(app: AppHandle) {
    let state = app.state::<Sidecar>();
    match spawn(&app).await {
        Ok((child, ready)) => {
            state.set(Some(child), Some(ready), None);
            let _ = app.emit("sidecar-ready", state.info());
        }
        Err(e) => {
            eprintln!("[droplink] sidecar failed: {e}");
            state.set(None, None, Some(e));
            let _ = app.emit("sidecar-failed", state.info());
        }
    }
}

async fn spawn(app: &AppHandle) -> Result<(Child, Ready), String> {
    let path = binary_path().map_err(|e| format!("locate sidecar: {e}"))?;
    let data_dir = app
        .path()
        .app_data_dir()
        .map_err(|e| format!("app data dir: {e}"))?;

    let mut cmd = Command::new(&path);
    cmd.arg("-watch-stdin")
        .arg("-data-dir")
        .arg(&data_dir)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .kill_on_drop(true);
    #[cfg(windows)]
    {
        // CREATE_NO_WINDOW: no console window flashing up for the sidecar.
        cmd.creation_flags(0x0800_0000);
    }

    let mut child = cmd
        .spawn()
        .map_err(|e| format!("start {}: {e}", path.display()))?;
    let stdout = child.stdout.take().ok_or("sidecar stdout missing")?;
    if let Some(stderr) = child.stderr.take() {
        // Forward the sidecar's log to ours, line by line.
        tauri::async_runtime::spawn(async move {
            let mut lines = BufReader::new(stderr).lines();
            while let Ok(Some(line)) = lines.next_line().await {
                eprintln!("[sidecar] {line}");
            }
        });
    }

    let mut lines = BufReader::new(stdout).lines();
    let ready = tokio::time::timeout(READY_TIMEOUT, async {
        while let Ok(Some(line)) = lines.next_line().await {
            if let Some(r) = parse_ready(&line) {
                return Ok(r);
            }
        }
        Err("sidecar exited before it was ready".to_string())
    })
    .await
    .map_err(|_| "sidecar did not report ready within 10 s".to_string())??;

    // Keep draining stdout so the pipe can never fill and block the sidecar.
    tauri::async_runtime::spawn(async move { while let Ok(Some(_)) = lines.next_line().await {} });
    Ok((child, ready))
}
