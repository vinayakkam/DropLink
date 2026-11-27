//! DropLink desktop shell.
//!
//! The window shows the plain-JS UI from `web/` (`frontendDist`). All
//! networking and file transfer happens in the Go sidecar, which this shell
//! starts on launch and stops on quit ([`sidecar`]). The UI asks for the
//! sidecar's port and token with the `sidecar_info` command, then talks to it
//! over HTTP on 127.0.0.1. The shell adds what only a desktop app can: a
//! tray icon and close-to-tray ([`tray`]), native notifications, real
//! file paths from drag-drop and pickers, the clipboard, and
//! `droplink://join/<CODE>` links ([`deeplink`]).

pub mod deeplink;
pub mod sidecar;
pub mod tray;
pub mod update;

use tauri::{Manager, RunEvent, WindowEvent};

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
            tray::set_visible(app, true);
        }))
        .plugin(tauri_plugin_notification::init())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_clipboard_manager::init())
        .plugin(tauri_plugin_deep_link::init())
        .manage(sidecar::Sidecar::default())
        .manage(tray::Behavior::default())
        .manage(deeplink::PendingJoin::default())
        .invoke_handler(tauri::generate_handler![
            sidecar_info,
            restart_sidecar,
            tray::sync_window_settings,
            deeplink::take_pending_join,
            update::install_update
        ])
        .setup(|app| {
            tray::install(app.handle())?;
            deeplink::install(app.handle());
            let handle = app.handle().clone();
            tauri::async_runtime::spawn(sidecar::launch(handle));
            Ok(())
        })
        .on_window_event(|window, event| {
            if let WindowEvent::CloseRequested { api, .. } = event {
                let app = window.app_handle();
                if tray::on_close(app.state::<tray::Behavior>().close_to_tray())
                    == tray::OnClose::Hide
                {
                    // Keep receiving in the background; the tray brings it back.
                    api.prevent_close();
                    let _ = window.hide();
                }
            }
        })
        .build(tauri::generate_context!())
        .expect("error while building DropLink");

    app.run(|app, event| {
        if let RunEvent::Exit = event {
            app.state::<sidecar::Sidecar>().stop();
        }
    });
}
