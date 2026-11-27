//! System tray and close-to-tray.
//!
//! The tray menu shows or hides the window, toggles auto-accept for
//! trusted devices (the UI saves it to the sidecar's settings) and quits.
//! When "close to tray" is on, the window's close button hides it instead,
//! so DropLink keeps receiving in the background.

use std::sync::atomic::{AtomicBool, Ordering};

use tauri::menu::{CheckMenuItem, Menu, MenuItem, PredefinedMenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::{AppHandle, Emitter, Manager, Wry};

/// Window behaviour the UI controls through commands.
#[derive(Default)]
pub struct Behavior {
    close_to_tray: AtomicBool,
    auto_accept: AtomicBool,
}

impl Behavior {
    pub fn close_to_tray(&self) -> bool {
        self.close_to_tray.load(Ordering::Relaxed)
    }
    pub fn set_close_to_tray(&self, on: bool) {
        self.close_to_tray.store(on, Ordering::Relaxed)
    }
    pub fn auto_accept(&self) -> bool {
        self.auto_accept.load(Ordering::Relaxed)
    }
    pub fn set_auto_accept(&self, on: bool) {
        self.auto_accept.store(on, Ordering::Relaxed)
    }
}

/// Label of the show/hide item for the window's current visibility.
pub fn toggle_label(visible: bool) -> &'static str {
    if visible {
        "Hide DropLink"
    } else {
        "Show DropLink"
    }
}

/// What the close button does.
#[derive(Debug, PartialEq, Eq)]
pub enum OnClose {
    Hide,
    Quit,
}

pub fn on_close(close_to_tray: bool) -> OnClose {
    if close_to_tray {
        OnClose::Hide
    } else {
        OnClose::Quit
    }
}

/// Shows and focuses the main window, or hides it.
pub fn set_visible(app: &AppHandle, visible: bool) {
    if let Some(w) = app.get_webview_window("main") {
        if visible {
            let _ = w.unminimize();
            let _ = w.show();
            let _ = w.set_focus();
        } else {
            let _ = w.hide();
        }
    }
}

fn window_visible(app: &AppHandle) -> bool {
    app.get_webview_window("main")
        .and_then(|w| w.is_visible().ok())
        .unwrap_or(false)
}

/// Builds the tray icon and its menu.
pub fn install(app: &AppHandle) -> tauri::Result<()> {
    let toggle = MenuItem::with_id(app, "toggle", toggle_label(true), true, None::<&str>)?;
    let auto = CheckMenuItem::with_id(
        app,
        "auto_accept",
        "Auto-accept trusted devices",
        true,
        false,
        None::<&str>,
    )?;
    let quit = MenuItem::with_id(app, "quit", "Quit DropLink", true, None::<&str>)?;
    let menu: Menu<Wry> = Menu::with_items(
        app,
        &[&toggle, &auto, &PredefinedMenuItem::separator(app)?, &quit],
    )?;

    let (toggle_item, auto_item) = (toggle.clone(), auto.clone());
    TrayIconBuilder::with_id("droplink")
        .tooltip("DropLink")
        .icon(app.default_window_icon().cloned().expect("bundle icon"))
        .menu(&menu)
        .show_menu_on_left_click(false)
        .on_menu_event(move |app, event| match event.id().as_ref() {
            "toggle" => {
                let show = !window_visible(app);
                set_visible(app, show);
                let _ = toggle_item.set_text(toggle_label(show));
            }
            "auto_accept" => {
                let on = auto_item.is_checked().unwrap_or(false);
                app.state::<Behavior>().set_auto_accept(on);
                // The UI saves it to the sidecar's settings.
                let _ = app.emit("tray-auto-accept", on);
            }
            "quit" => app.exit(0),
            _ => {}
        })
        .on_tray_icon_event(|tray, event| {
            if let TrayIconEvent::Click {
                button: MouseButton::Left,
                button_state: MouseButtonState::Up,
                ..
            } = event
            {
                set_visible(tray.app_handle(), true);
            }
        })
        .build(app)?;

    // Keep the menu's check mark in step with settings changed in the UI.
    app.manage(TrayItems { auto });
    Ok(())
}

/// Menu items the UI's settings update.
pub struct TrayItems {
    auto: CheckMenuItem<Wry>,
}

/// Called by the UI when settings load or change.
#[tauri::command]
pub fn sync_window_settings(app: AppHandle, close_to_tray: bool, auto_accept: bool) {
    let b = app.state::<Behavior>();
    b.set_close_to_tray(close_to_tray);
    b.set_auto_accept(auto_accept);
    if let Some(items) = app.try_state::<TrayItems>() {
        let _ = items.auto.set_checked(auto_accept);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn toggle_label_follows_visibility() {
        assert_eq!(toggle_label(true), "Hide DropLink");
        assert_eq!(toggle_label(false), "Show DropLink");
    }

    #[test]
    fn close_button_hides_only_with_close_to_tray() {
        assert_eq!(on_close(true), OnClose::Hide);
        assert_eq!(on_close(false), OnClose::Quit);
    }

    #[test]
    fn behavior_defaults_off_and_updates() {
        let b = Behavior::default();
        assert!(!b.close_to_tray() && !b.auto_accept());
        b.set_close_to_tray(true);
        b.set_auto_accept(true);
        assert!(b.close_to_tray() && b.auto_accept());
        b.set_close_to_tray(false);
        assert!(!b.close_to_tray() && b.auto_accept());
    }
}
