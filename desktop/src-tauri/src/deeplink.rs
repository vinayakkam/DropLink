//! `droplink://join/<CODE>` links: opening one starts DropLink (or wakes
//! the running one, through single-instance) and joins that room.

use std::sync::Mutex;

use tauri::{AppHandle, Emitter, Manager};
use tauri_plugin_deep_link::DeepLinkExt;

/// Room codes use these characters (no look-alikes); see internal/signal.
const ALPHABET: &str = "ABCDEFGHJKMNPQRSTUVWXYZ23456789";

/// Extracts the room code from a join link, or None for anything else.
/// Accepts any case and a trailing slash or query.
pub fn parse_join(url: &str) -> Option<String> {
    let url = url.trim();
    let rest = url.get(..16).filter(|p| p.eq_ignore_ascii_case("droplink://join/")).map(|_| &url[16..])?;
    let code: String = rest
        .split(|c| c == '/' || c == '?' || c == '#')
        .next()?
        .to_ascii_uppercase();
    (code.len() == 6 && code.chars().all(|c| ALPHABET.contains(c))).then_some(code)
}

/// A join requested before the UI was ready to hear it.
#[derive(Default)]
pub struct PendingJoin(Mutex<Option<String>>);

/// The UI asks for it once at startup.
#[tauri::command]
pub fn take_pending_join(state: tauri::State<'_, PendingJoin>) -> Option<String> {
    state.0.lock().unwrap_or_else(|e| e.into_inner()).take()
}

fn handle(app: &AppHandle, urls: impl IntoIterator<Item = String>) {
    for url in urls {
        if let Some(code) = parse_join(&url) {
            crate::tray::set_visible(app, true);
            *app.state::<PendingJoin>().0.lock().unwrap_or_else(|e| e.into_inner()) = Some(code.clone());
            let _ = app.emit("deep-link-join", code);
        }
    }
}

/// Registers the scheme (needed at run time in development on Windows and
/// Linux; installers register it too) and wires link events.
pub fn install(app: &AppHandle) {
    #[cfg(any(windows, target_os = "linux"))]
    {
        if let Err(e) = app.deep_link().register_all() {
            eprintln!("[droplink] could not register droplink:// links: {e}");
        }
    }
    if let Ok(Some(urls)) = app.deep_link().get_current() {
        handle(app, urls.into_iter().map(|u| u.to_string()));
    }
    let h = app.clone();
    app.deep_link().on_open_url(move |event| {
        handle(&h, event.urls().into_iter().map(|u| u.to_string()));
    });
}
