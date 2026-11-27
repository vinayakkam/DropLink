// Hash routing between the four views. No library: the hash is the state.

export const VIEWS = ["home", "rooms", "history", "settings"];

/**
 * "#/rooms?code=ABC234" → { view: "rooms", query: { code: "ABC234" } }.
 * Unknown views fall back to home.
 * @param {string} hash
 */
export function parseRoute(hash) {
  const raw = (hash || "").replace(/^#\/?/, "");
  const [path, qs = ""] = raw.split("?");
  const view = VIEWS.includes(path) ? path : "home";
  /** @type {Record<string, string>} */
  const query = {};
  for (const [k, v] of new URLSearchParams(qs)) query[k] = v;
  return { view, query };
}

/** @param {string} view @param {Record<string,string>} [query] */
export function href(view, query = {}) {
  const qs = new URLSearchParams(query).toString();
  return `#/${view === "home" ? "" : view}${qs ? `?${qs}` : ""}`;
}

/**
 * Shows the section for the current route and marks its tab.
 * @param {ParentNode} root
 * @param {{ view: string }} route
 */
export function showView(root, route) {
  for (const el of root.querySelectorAll("[data-view]")) {
    el.hidden = el.getAttribute("data-view") !== route.view;
  }
  for (const tab of root.querySelectorAll("[data-tab]")) {
    if (tab.getAttribute("data-tab") === route.view) tab.setAttribute("aria-current", "page");
    else tab.removeAttribute("aria-current");
  }
}

/**
 * Keeps the store's route in sync with location.hash.
 * @param {Window} win
 * @param {{ set: (p: any) => void }} store
 */
export function watchRoute(win, store) {
  const update = () => store.set({ route: parseRoute(win.location.hash) });
  win.addEventListener("hashchange", update);
  update();
  return () => win.removeEventListener("hashchange", update);
}
