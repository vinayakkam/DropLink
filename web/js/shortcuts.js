// Keyboard shortcuts. keyToAction is pure (easy to test); bindShortcuts
// wires it to the window and calls the matching handler. Shortcuts never
// fire while you are typing in a field or a modal is open, and never when
// a modifier (Ctrl/Alt/Meta) is held, so browser and OS keys still work.

/** The shortcuts, for the help overlay. */
export const SHORTCUTS = [
  { keys: "1–4", does: "Switch section (Radar, Rooms, Log, Config)" },
  { keys: "F", does: "Pick files" },
  { keys: "D", does: "Pick folder" },
  { keys: "V", does: "Send clipboard" },
  { keys: "Enter", does: "Launch the loaded cargo" },
  { keys: "C", does: "Clear the loaded cargo" },
  { keys: "?", does: "Show this help" },
];

const BY_DIGIT = { 1: "view:home", 2: "view:rooms", 3: "view:history", 4: "view:settings" };
const BY_KEY = { f: "pick-files", d: "pick-folder", v: "send-clip", c: "clear", enter: "launch" };

/**
 * Maps a keydown to an action name, or null to ignore it.
 * @param {{ key: string, ctrlKey?: boolean, metaKey?: boolean, altKey?: boolean }} e
 * @param {{ typing?: boolean, modal?: boolean }} [ctx]
 * @returns {string | null}
 */
export function keyToAction(e, ctx = {}) {
  if (ctx.typing || ctx.modal) return null;
  if (e.ctrlKey || e.metaKey || e.altKey) return null;
  const k = e.key.toLowerCase();
  if (k === "?" || (e.key === "/" && /** @type {any} */ (e).shiftKey)) return "help";
  if (BY_DIGIT[k]) return BY_DIGIT[k];
  if (BY_KEY[k]) return BY_KEY[k];
  return null;
}

/** True when focus is in a text field, so typing is not hijacked. */
function isTyping(el) {
  if (!el) return false;
  const tag = el.tagName;
  return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || el.isContentEditable;
}

/**
 * @param {Document} doc
 * @param {Window} win
 * @param {Record<string, () => void>} handlers keyed by action name
 * @param {() => boolean} [modalOpen]
 * @returns {() => void} unbind
 */
export function bindShortcuts(doc, win, handlers, modalOpen = () => !!doc.querySelector(".modal-backdrop")) {
  const onKey = (e) => {
    const action = keyToAction(e, { typing: isTyping(doc.activeElement), modal: modalOpen() });
    if (!action) return;
    const fn = handlers[action];
    if (!fn) return;
    e.preventDefault();
    fn();
  };
  win.addEventListener("keydown", onKey);
  return () => win.removeEventListener("keydown", onKey);
}
