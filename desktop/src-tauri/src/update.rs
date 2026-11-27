//! Installing a downloaded update.
//!
//! The sidecar downloads the new installer and keeps it only after its
//! SHA-256 and Ed25519 signature check out (internal/update). This command
//! then runs exactly that file, `<app data>/updates/droplink-update-setup.exe`,
//! silently with `/RELAUNCH=1`, and quits: the installer closes nothing that
//! is still running, replaces the app and starts it again. It takes no path
//! from the UI, so a compromised page cannot make it run anything else.

use std::path::{Path, PathBuf};

use tauri::{AppHandle, Manager};

/// Must match update.InstallerFile in Go.
pub const INSTALLER_FILE: &str = "droplink-update-setup.exe";

/// Arguments for a quiet update that ends with DropLink running again.
pub const INSTALLER_ARGS: [&str; 4] = ["/SILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/RELAUNCH=1"];

/// Where the verified installer is, under the app's data directory.
pub fn installer_path(data_dir: &Path) -> PathBuf {
    data_dir.join("updates").join(INSTALLER_FILE)
}

#[tauri::command]
pub fn install_update(app: AppHandle) -> Result<(), String> {
    let data = app.path().app_data_dir().map_err(|e| e.to_string())?;
    let path = installer_path(&data);
    if !path.is_file() {
        return Err("no downloaded update to install".into());
    }
    launch(&path)?;
    // The installer waits for nothing: quit now so it can replace us. The
    // exit handler stops the sidecar.
    app.exit(0);
    Ok(())
}

#[cfg(windows)]
fn launch(path: &Path) -> Result<(), String> {
    use std::os::windows::process::CommandExt;
    const DETACHED_PROCESS: u32 = 0x0000_0008;
    const CREATE_NEW_PROCESS_GROUP: u32 = 0x0000_0200;
    std::process::Command::new(path)
        .args(INSTALLER_ARGS)
        .creation_flags(DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP)
        .spawn()
        .map(|_| ())
        .map_err(|e| format!("start installer: {e}"))
}

#[cfg(not(windows))]
fn launch(_path: &Path) -> Result<(), String> {
    Err("self-update installs on Windows only; use your package manager".into())
}
