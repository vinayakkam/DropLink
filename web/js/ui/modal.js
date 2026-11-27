// Pixel modal dialogs. One at a time; Escape and the backdrop cancel;
// focus moves into the dialog and back to where it was.
import { h } from "./dom.js";

let current = null;

/**
 * @param {{ title: string, body: (Node|string)[], actions: { label: string, value: any, kind?: string, primary?: boolean }[], cancelValue?: any, className?: string }} opts
 * @returns {Promise<any>} the value of the action chosen
 */
export function openModal(opts) {
  current?.close(opts.cancelValue);
  return new Promise((resolve) => {
    const before = document.activeElement;
    const close = (value) => {
      if (!current || current.el !== backdrop) return;
      current = null;
      backdrop.remove();
      document.removeEventListener("keydown", onKey, true);
      if (before instanceof HTMLElement) before.focus();
      resolve(value);
    };
    const onKey = (e) => {
      if (e.key === "Escape") {
        e.preventDefault();
        close(opts.cancelValue);
      }
    };
    const buttons = opts.actions.map((a) =>
      h("button", { class: `btn ${a.kind ? `btn--${a.kind}` : ""}`, onclick: () => close(a.value) }, a.label),
    );
    const dialog = h(
      "div",
      { class: `panel modal ${opts.className ?? ""}`, role: "dialog", "aria-modal": "true", "aria-label": opts.title },
      h("div", { class: "panel__label" }, opts.title),
      h("div", { class: "modal__body" }, ...opts.body),
      h("div", { class: "modal__actions" }, ...buttons),
    );
    const backdrop = h("div", { class: "modal-backdrop", onclick: (e) => e.target === backdrop && close(opts.cancelValue) }, dialog);
    document.body.append(backdrop);
    document.addEventListener("keydown", onKey, true);
    current = { el: backdrop, close };
    const primary = opts.actions.findIndex((a) => a.primary);
    (dialog.querySelector("input, textarea, select") ?? buttons[primary >= 0 ? primary : 0])?.focus();
  });
}

/** Closes the open modal (e.g. its offer was answered elsewhere). */
export function closeModal(value) {
  current?.close(value);
}
