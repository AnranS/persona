export interface HubAddress {
  readonly url: URL;
  /** Path prefix, including a trailing slash; empty at the root. */
  readonly prefix: string;
}

/** The hub address a person entered. Never accepts a credential, fragment, non-network scheme or opaque URL. */
export function parseHubAddress(input: string): HubAddress {
  const trimmed = input.trim();
  if (!trimmed || trimmed.length > 2048 || /[\u0000- \u007f]/.test(trimmed)) throw new Error("Enter the hub's web address.");
  const withScheme = /^[a-z][a-z\d+.-]*:/i.test(trimmed) ? trimmed : `https://${trimmed}`;
  const url = new URL(withScheme);
  if (url.protocol !== "https:" && url.protocol !== "http:") throw new Error("The hub address must use HTTP or HTTPS.");
  if (!url.hostname || url.username || url.password || url.hash) throw new Error("The hub address cannot contain credentials or a fragment.");
  url.pathname = `${url.pathname.replace(/\/+$/, "")}/`;
  url.search = "";
  return { url, prefix: url.pathname === "/" ? "" : url.pathname };
}

/** A page of the configured hub, including when a reverse proxy mounts it below the origin root. */
export function inHub(hub: HubAddress, candidate: string | URL): boolean {
  let url: URL;
  try { url = typeof candidate === "string" ? new URL(candidate) : candidate; } catch { return false; }
  return url.origin === hub.url.origin && url.pathname.startsWith(hub.url.pathname);
}

/** An external link is opened only by the system browser, never given a new Electron window. */
export function externalBrowserURL(candidate: string): string | undefined {
  try {
    const url = new URL(candidate);
    return (url.protocol === "https:" || url.protocol === "http:") && !url.username && !url.password ? url.href : undefined;
  } catch { return undefined; }
}

export interface DesktopDeepLink {
  readonly kind: "open" | "pair-link" | "pair-app";
  readonly url: string;
  readonly route?: string;
}

function hasUnsafeRouteCharacters(value: string): boolean {
  return value.includes("\\") || /[\u0000-\u001f\u007f]/.test(value);
}

function validDeepLinkRoute(route: string): boolean {
  if (!route.startsWith("/") || route.startsWith("//") || hasUnsafeRouteCharacters(route)) return false;
  try { return new URL(route, "https://desktop.invalid/").origin === "https://desktop.invalid"; } catch { return false; }
}

/** The view-state keys that docs/protocol/deep-links.md allows for a canonical route, plus the web's older forms. */
function deepLinkQueryKeys(url: URL): ReadonlySet<string> {
  const path = url.pathname;
  if (/^\/persona\/[^/]+$/.test(path) && path !== "/persona/new") return new Set(["machine", "manager", "at"]);
  if (/^\/s\/[^/]+\/[^/]+$/.test(path)) return new Set(["at", "view", "work"]);
  if (/^\/t\/[^/]+\/[^/]+\/[^/]+\/[^/]+$/.test(path)) return new Set(["at", "view", "work"]);
  if (/^\/inbox\/[^/]+$/.test(path)) return new Set(["manager"]);
  if (path === "/todos") return new Set(["view", "mode", "date", "id"]); // id is the older to-do form.
  if (/^\/machines\/[^/]+$/.test(path)) return new Set(["range"]);
  if (path === "/settings") {
    let item: string | undefined;
    switch (url.searchParams.get("tab")) {
      case "models": item = "purpose"; break;
      case "channels": item = "channel"; break;
      case "extensions": item = "extension"; break;
      case "plugins": item = "plugin"; break;
      case "connectors": item = "connector"; break;
    }
    return new Set(item ? ["tab", "manager", item] : ["tab", "manager"]);
  }
  if (/^\/(?:extension|plugin)\/[^/]+\/[^/]+$/.test(path)) return new Set(["state"]);
  return new Set();
}

function canonicalDeepLinkRoute(url: URL): string | undefined {
  let decodedPath: string;
  try { decodedPath = decodeURIComponent(url.pathname); } catch { return undefined; }
  if (!decodedPath.startsWith("/") || decodedPath.startsWith("//") || hasUnsafeRouteCharacters(decodedPath)) return undefined;
  for (const [key, value] of url.searchParams) {
    if (hasUnsafeRouteCharacters(key) || hasUnsafeRouteCharacters(value)) return undefined;
  }
  if (!validDeepLinkRoute(`${url.pathname}${url.search}`)) return undefined;
  const allowed = deepLinkQueryKeys(url);
  const query = new URLSearchParams();
  for (const [key, value] of url.searchParams) {
    if (key !== "hub" && allowed.has(key)) query.append(key, value);
  }
  return `${url.pathname}${query.size ? `?${query}` : ""}`;
}

/** Deep links are data, never automatic pairing or navigation to an arbitrary web origin. */
export function parseDeepLink(raw: string): DesktopDeepLink | undefined {
  let url: URL;
  try { url = new URL(raw); } catch { return undefined; }
  if (url.protocol !== "persona:" && url.protocol !== "jarvis:") return undefined;
  if (url.username || url.password || url.port) return undefined;
  if (url.host === "link") return { kind: "pair-link", url: raw };
  if (url.host === "pair") return { kind: "pair-app", url: raw };
  if (url.host !== "open" || url.hash) return undefined;
  if (url.searchParams.size === 1 && url.searchParams.has("route")) {
    const route = url.searchParams.get("route");
    if (!route || !validDeepLinkRoute(route)) return undefined;
    return { kind: "open", url: raw, route };
  }
  if (!url.pathname) return undefined;
  const route = canonicalDeepLinkRoute(url);
  return route ? { kind: "open", url: raw, route } : undefined;
}

export function hubRoute(hub: HubAddress, route: string): string {
  const target = new URL(hub.url);
  const clean = route.replace(/^\/+/, "");
  target.pathname = `${target.pathname}${clean}`;
  return target.href;
}
