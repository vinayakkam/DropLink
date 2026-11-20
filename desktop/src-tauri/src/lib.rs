//! DropLink desktop shell.
//!
//! The window shows the plain-JS UI from `web/` (`frontendDist`). All
//! networking and file transfer happens in the Go sidecar, which this shell
//! starts on launch and stops on quit ([`sidecar`]). The UI asks for the
//! sidecar's port and token with the `sidecar_info` command, then talks to it
//! over HTTP on 127.0.0.1.

pub mod sidecar;

use tauri::{Manager, RunEvent};

#[tauri::command]
fn sidecar_info(state: tauri::State<'_, sidecar::Sidecar>) -> sidecar::SidecarInfo {
    state.info()
}

#[tauri::command]
async fn restart_sidecar(app: tauri::AppHandle) -> sidecar::SidecarInfo {
    app.state::<sidecar::Sidecar>().stop();
    sidecar::launch(app.clone()).await;
    app.state::<sidecar::Sidecar>().info()
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    let app = tauri::Builder::default()
        // Must come first, so a second launch is handed to this window before
        // it can start a second sidecar.
        .plugin(tauri_plugin_single_instance::init(|app, _argv, _cwd| {
            if let Some(window) = app.get_webview_window("main") {
                let _ = window.unminimize();
                let _ = window.show();
                let _ = window.set_focus();
            }
        }))
        .manage(sidecar::Sidecar::default())
        .invoke_handler(tauri::generate_handler![sidecar_info, restart_sidecar])
        .setup(|app| {
            let handle = app.handle().clone();
            tauri::async_runtime::spawn(sidecar::launch(handle));
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("error while building DropLink");

    app.run(|app, event| {
        if let RunEvent::Exit = event {
            app.state::<sidecar::Sidecar>().stop();
        }
    });
}
