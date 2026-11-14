import { join } from "node:path";
import { BrowserWindow, app, dialog, screen, shell, powerMonitor, Notification, protocol } from "electron";
import { parseDeepLink, parseHubAddress, externalBrowserURL, inHub } from "./urls.js";
import type { HubAddress } from "./urls.js";
import { appPairingURL } from "./pairing.js";
import { installDesktopIPC, DesktopNotifications } from "./ipc.js";
import { installSetupIPC } from "./setup-ipc.js";
import { JSONStore } from "./store.js";
import { restoreWindow, DEFAULT_WINDOW } from "./window-state.js";
import { closeToTray, installTray } from "./tray.js";
import type { DesktopTray } from "./tray.js";
import { desktopLocale, loadStrings } from "./strings.js";
import { updateMenu } from "./update-state.js";
import type { UpdateMenuItem } from "./update-state.js";
import { hubPartition } from "./sessions.js";
import { installSmokeCapture } from "./smoke.js";
import { installAutoUpdate } from "./updater.js";
import { desktopPresence } from "./presence.js";
import { installSetupProtocol, SETUP_SCHEME } from "./setup-protocol.js";
import { SETUP_URL } from "./setup-resources.js";
import { HubWindowLifecycle, sameHubWindow } from "./hub-window-lifecycle.js";

installSmokeCapture();
// Register the scheme before any ready callback. Its file handler is installed with the app's other ready-time setup.
protocol.registerSchemesAsPrivileged([{ scheme: SETUP_SCHEME, privileges: { standard: true, secure: true } }]);

let mainWindow: BrowserWindow | undefined;
let mainWindowHub: string | undefined;
let hub: HubAddress | undefined;
let quitting = false;
let pendingDeepLink: string | undefined;
let setupWindow: BrowserWindow | undefined;
let screenLocked = false;
let presenceTimer: ReturnType<typeof setInterval> | undefined;
const store = () => JSONStore.inUserData(app.getPath("userData"));
const hubWindow = new HubWindowLifecycle();

function navigateFromDeepLink(raw: string): void {
  const link = parseDeepLink(raw);
  if (!link) return;
  if (!hub) { pendingDeepLink = raw; void showSetup(); return; }
  if (link.kind === "open" && link.route) mainWindow?.webContents.send("desktop:notification-open", link.route);
  if (link.kind === "pair-app") {
    const pairing = appPairingURL(hub, raw);
    if (pairing) {
      void mainWindow?.loadURL(pairing);
    }
  }
  if (link.kind === "pair-link") {
    // Link pairing belongs to the separate local app, not to the hub-loaded page. No automatic pairing here.
    void showSetup();
  }
  mainWindow?.show();
}

function secureHubWindow(window: BrowserWindow): void {
  const contents = window.webContents;
  contents.setWindowOpenHandler(({ url }) => {
    const external = externalBrowserURL(url);
    if (external && (!hub || !inHub(hub, url))) void shell.openExternal(external);
    else if (hub && inHub(hub, url)) void window.loadURL(url);
    return { action: "deny" };
  });
  contents.on("will-navigate", (event, url) => {
    if (hub && inHub(hub, url)) return;
    event.preventDefault();
    const external = externalBrowserURL(url);
    if (external) void shell.openExternal(external);
  });
  contents.on("will-redirect", (event, url) => {
    if (hub && inHub(hub, url)) return;
    event.preventDefault();
    const external = externalBrowserURL(url);
    if (external) void shell.openExternal(external);
  });
  contents.on("will-attach-webview", event => event.preventDefault());
  contents.session.setPermissionRequestHandler((_contents, permission, callback) => {
    // The web notification preference still resolves; the OS notification itself comes from Electron.
    callback(permission === "notifications");
  });
  contents.session.setPermissionCheckHandler((_contents, permission) => permission === "notifications");
}

// A different hub needs a new window and its own persistent session, not navigation of the old window.
async function replaceMainWindow(next: HubAddress): Promise<void> {
  const previous = mainWindow;
  if (previous && !previous.isDestroyed() && mainWindowHub === next.url.href && sameHubWindow(next, previous.webContents.getURL())) {
    await hubWindow.show(async () => { previous.show(); });
    return;
  }
  // A replacement begins before the next creation, including a still-pending initial load. Never let the retired
  // operation borrow its old promise or install the next hub's IPC into the old immutable session.
  if (previous && !previous.isDestroyed()) {
    const closed = new Promise<void>(done => previous.once("closed", done));
    previous.destroy();
    await closed;
  }
  if (mainWindow === previous) {
    mainWindow = undefined;
    mainWindowHub = undefined;
  }
}

async function showMain(): Promise<void> {
  await hubWindow.show(async () => {
    const owner = hub;
    const generation = hubWindow.generation;
    if (!owner) {
      if (!setupWindow || setupWindow.isDestroyed()) await showSetup();
      return;
    }
    if (mainWindow && !mainWindow.isDestroyed() && mainWindowHub === owner?.url.href && sameHubWindow(owner, mainWindow.webContents.getURL())) {
      mainWindow.show();
      return;
    }
    if (mainWindow && !mainWindow.isDestroyed()) mainWindow.destroy();
    const state = restoreWindow((await store().read<{ window?: unknown }>())?.window, screen.getAllDisplays().map(d => d.workArea));
    if (owner !== hub || generation !== hubWindow.generation) return;
    const current = new BrowserWindow({
      ...DEFAULT_WINDOW,
      ...state,
      show: false,
      title: "Persona",
      autoHideMenuBar: true,
      backgroundColor: "#111111",
      webPreferences: {
        preload: join(import.meta.dirname, "preload.cjs"),
        contextIsolation: true,
        nodeIntegration: false,
        sandbox: true,
        webSecurity: true,
        allowRunningInsecureContent: false,
        devTools: !app.isPackaged,
        nodeIntegrationInWorker: false,
        nodeIntegrationInSubFrames: false,
        webviewTag: false,
        backgroundThrottling: false,
        spellcheck: true,
        ...(owner ? { partition: hubPartition(owner) } : {}),
      },
    });
  mainWindow = current;
  mainWindowHub = owner?.url.href;
  let usable = true;
  const discard = () => {
    if (!usable) return;
    usable = false;
    removeIPC();
    if (mainWindow === current) {
      mainWindow = undefined;
      mainWindowHub = undefined;
    }
    if (!current.isDestroyed()) current.destroy();
  };
  if (!app.isPackaged && process.env.PERSONA_DESKTOP_DEBUG === "1") {
    const log = (value: unknown) => {
      const line = `${typeof value === "string" ? value : JSON.stringify(value)}\n`;
      void import("node:fs/promises").then(({ appendFile }) => appendFile(process.env.PERSONA_DESKTOP_DEBUG_LOG ?? "/tmp/persona-desktop-debug.log", line, "utf8"));
    };
    current.webContents.openDevTools({ mode: "detach" });
    current.webContents.on("console-message", event => log(event.message));
    current.webContents.on("preload-error", (_event, _path, error) => log(error));
    current.webContents.on("render-process-gone", (_event, details) => log(details));
    current.webContents.on("did-finish-load", () => {
      void current.webContents.executeJavaScript("({title: document.title, text: document.body.innerText.slice(0, 500), html: document.body.innerHTML.slice(0, 500)})").then(value => log(value));
    });
  }
  secureHubWindow(current);
  closeToTray(current, () => quitting);
  const removeIPC = installDesktopIPC(current, {
    // The partition is immutable, so derive authorization from this window's actual origin, never a later global hub.
    hub: () => {
      const url = current.webContents.getURL();
      if (owner && inHub(owner, url)) return owner;
      return undefined;
    },
    presence: () => desktopPresence(powerMonitor.getSystemIdleTime(), screenLocked, Notification.isSupported()),
    openLocalSettings: () => { void showSetup(); },
    notifications: new DesktopNotifications(route => {
      current.show();
      current.focus();
      current.webContents.send("desktop:notification-open", route);
    }),
  });
  const save = () => {
    if (current.isDestroyed() || current.isMinimized()) return;
    const maximized = current.isMaximized();
    const bounds = maximized ? current.getNormalBounds() : current.getBounds();
    void store().saveWindow({ ...bounds, maximized });
  };
  current.on("resize", save);
  current.on("move", save);
  current.on("maximize", save);
  current.on("unmaximize", save);
  current.on("close", save);
  current.once("ready-to-show", () => {
    if (!usable || owner !== hub || generation !== hubWindow.generation || current.isDestroyed()) { discard(); return; }
    if (state.maximized) current.maximize();
    current.show();
    if (pendingDeepLink) navigateFromDeepLink(pendingDeepLink);
    pendingDeepLink = undefined;
  });
  current.once("closed", () => { discard(); });
  if (owner) {
    try {
      await current.loadURL(owner.url.href);
      if (!usable || owner !== hub || generation !== hubWindow.generation || current.isDestroyed()) discard();
    } catch (error) {
      const obsolete = !usable || owner !== hub || generation !== hubWindow.generation || current.isDestroyed();
      discard();
      // Destroying a retired window rejects its outstanding load. That is the replacement's expected cleanup, not a
      // startup failure. A current generation's genuine load error must still reach its caller.
      if (!obsolete) throw error;
    }
  }
  });
}

async function showSetup(): Promise<void> {
  if (setupWindow && !setupWindow.isDestroyed()) { setupWindow.show(); setupWindow.focus(); return; }
  const setup = new BrowserWindow({
    width: 720,
    height: 640,
    title: "Persona setup",
    autoHideMenuBar: true,
    webPreferences: {
      preload: join(import.meta.dirname, "setup-preload.cjs"),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
      webSecurity: true,
      allowRunningInsecureContent: false,
      devTools: !app.isPackaged,
      nodeIntegrationInWorker: false,
      nodeIntegrationInSubFrames: false,
      webviewTag: false,
    },
  });
  setupWindow = setup;
  setup.setMenu(null);
  setup.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
  setup.webContents.on("will-navigate", event => event.preventDefault());
  setup.once("closed", () => {
    removeIPC();
    if (setupWindow === setup) setupWindow = undefined;
    if (!mainWindow) {
      if (hub && !hubWindow.pending) void showMain().catch(() => {});
      else if (!hub) void showSetup().catch(() => {});
    }
  });
  let connecting = false;
  const removeIPC = installSetupIPC(setup, {
    hub: () => hub,
    connect: async (raw: string) => {
      if (connecting) return { ok: false, error: "Connecting to the hub…" };
      connecting = true;
      try {
        const next = parseHubAddress(raw);
        await replaceMainWindow(next);
        hub = next;
        hubWindow.replace();
        // Keep the setup window and its IPC alive until the new hub actually loaded. A network failure is recoverable
        // here, not a reason to strand the person with no window or misreport a valid address as malformed.
        await showMain();
        await store().update({ hub: next.url.href });
        setup.close();
        return { ok: true };
      } catch (error) {
        hub = undefined;
        hubWindow.replace();
        if (!mainWindow && !setup.isDestroyed()) void showSetup().catch(() => {});
        return { ok: false, error: error instanceof Error ? error.message : "Could not connect to the hub." };
      } finally { connecting = false; }
    },
  });
  await setup.loadURL(SETUP_URL);
}

const locked = app.requestSingleInstanceLock();
if (!locked) app.quit();
else {
  app.on("second-instance", (_event, argv) => {
    const raw = argv.find(value => /^(?:persona|jarvis):\/\//i.test(value));
    if (raw) navigateFromDeepLink(raw);
    mainWindow?.show();
    mainWindow?.focus();
  });
  app.on("open-url", (event, raw) => {
    event.preventDefault();
    if (mainWindow) navigateFromDeepLink(raw);
    else pendingDeepLink = raw;
  });
  app.whenReady().then(async () => {
    installSetupProtocol();
    const publishPresence = () => {
      if (!mainWindow || mainWindow.isDestroyed()) return;
      const presence = desktopPresence(powerMonitor.getSystemIdleTime(), screenLocked, Notification.isSupported());
      mainWindow.webContents.send("desktop:presence-changed", presence);
    };
    powerMonitor.on("lock-screen", () => { screenLocked = true; publishPresence(); });
    powerMonitor.on("unlock-screen", () => { screenLocked = false; publishPresence(); });
    powerMonitor.on("suspend", () => { screenLocked = true; publishPresence(); });
    // Resume by itself is not proof that the person unlocked the computer; only unlock-screen restores presence.
    powerMonitor.on("resume", publishPresence);
    presenceTimer = setInterval(publishPresence, 30_000);
    const strings = await loadStrings(join(import.meta.dirname, "locales"), desktopLocale(app.getLocale()));
    // The first check starts here, before any window loads; its state is kept until the tray exists to show it.
    let tray: DesktopTray | undefined;
    let updateItems: readonly UpdateMenuItem[] = [];
    const updates = installAutoUpdate({
      changed: state => { updateItems = updateMenu(state, strings); tray?.setUpdate(updateItems); },
      prepareQuit: () => { quitting = true; },
    });
    const saved = await store().read<{ hub?: string }>();
    try { hub = parseHubAddress(process.env.PERSONA_HUB_URL ?? saved?.hub ?? ""); } catch { hub = undefined; }
    if (process.env.PERSONA_DESKTOP_TEST_NO_PROTOCOL_REGISTRATION !== "1") {
      for (const scheme of ["persona", "jarvis"]) {
        if (!app.isDefaultProtocolClient(scheme)) app.setAsDefaultProtocolClient(scheme);
      }
    }
    if (hub) await showMain();
    else await showSetup();
    tray = installTray({ open: strings.tray_open ?? "Open Persona", quit: strings.tray_quit ?? "Quit Persona" }, () => {
      if (mainWindow && !mainWindow.isDestroyed()) { mainWindow.show(); mainWindow.focus(); }
      else if (hub) void showMain().catch(() => { if (!setupWindow || setupWindow.isDestroyed()) void showSetup().catch(() => {}); });
      else void showSetup().catch(() => {});
    }, () => { quitting = true; app.quit(); }, action => {
      if (action === "restart") updates?.restart();
      else updates?.check();
    });
    tray.setUpdate(updateItems);
  }).catch(error => {
    dialog.showErrorBox("Persona could not start", error instanceof Error ? error.message : String(error));
    app.quit();
  });
}
app.on("before-quit", () => { quitting = true; if (presenceTimer) clearInterval(presenceTimer); });
app.on("window-all-closed", () => {
  // Closing to the tray deliberately keeps the app alive; an explicit Quit exits on every platform.
});
