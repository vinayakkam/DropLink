// A few DOM helpers. Text always goes in through textContent, never
// innerHTML, so peer names and file names cannot inject markup.

/**
 * h("button", { class: "btn", onclick: fn }, "LAUNCH")
 * @param {string} tag
 * @param {Record<string, any> | null} [attrs]
 * @param {...(Node | string | null | undefined | false)} children
 */
export function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs ?? {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k.startsWith("on") && typeof v === "function") el.addEventListener(k.slice(2), v);
    else if (k === "class") el.className = v;
    else if (k === "dataset") Object.assign(el.dataset, v);
    else if (v === true) el.setAttribute(k, "");
    else el.setAttribute(k, String(v));
  }
  for (const c of children) {
    if (c === null || c === undefined || c === false) continue;
    el.append(typeof c === "string" ? document.createTextNode(c) : c);
  }
  return el;
}

/** Replaces el's children. */
export function render(el, ...children) {
  el.replaceChildren(...children.filter((c) => c !== null && c !== undefined && c !== false));
}

/** A short notice in the corner; it goes away by itself. */
export function toast(text, kind = "info") {
  let box = document.getElementById("toasts");
  if (!box) {
    box = h("div", { id: "toasts", class: "toasts", role: "status", "aria-live": "polite" });
    document.body.append(box);
  }
  const t = h("div", { class: `toast toast--${kind}` }, text);
  box.append(t);
  setTimeout(() => t.remove(), 4000);
}
