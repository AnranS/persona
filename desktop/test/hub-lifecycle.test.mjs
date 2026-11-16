import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { cpSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import test from "node:test";

const root = join(import.meta.dirname, "..", "..");
const built = spawnSync(process.platform === "win32" ? "npm.cmd" : "npm", ["--prefix", join(root, "desktop"), "run", "build"], { encoding: "utf8", timeout: 120000, shell: process.platform === "win32" });
assert.equal(built.status, 0, `${built.stdout}\n${built.stderr}`);
cpSync(join(root, "desktop", "dist"), join(root, "desktop", "dist-test", "src"), { recursive: true });
const main = join(root, "desktop", "dist-test", "src", "main.js");
const loader = join(root, "desktop", "test", "fixtures", "lifecycle-register.mjs");

async function run(name) {
  const script = `
    const electron = await import("./fixtures/electron-lifecycle.mjs");
    ${name === "prefix-root" || name === "tray-failure" ? "electron.scenario.oldSucceeds = true;" : ""}
    await electron.importMain();
    ${name === "stale" ? `
      await electron.waitFor(() => electron.controls.has("reject-old"), "the initial old-hub load");
      await electron.app.emit("second-instance", {}, ["persona://link"]);
      const setup = await electron.waitFor(() => electron.windows.find(window => window.options.title === "Persona setup"), "the setup window");
      const response = await electron.connect({sender: setup.webContents, senderFrame: setup.webContents}, "https://new.example/");
      if (!response.ok) throw new Error("hub replacement failed");
      electron.controls.get("reject-old")(new Error("ERR_ABORTED: intentionally destroyed stale load"));
    ` : name === "connect-failure" ? `
      electron.scenario.failNew = true;
      await electron.waitFor(() => electron.controls.has("reject-old"), "the initial old-hub load");
      await electron.app.emit("second-instance", {}, ["persona://link"]);
      const setup = await electron.waitFor(() => electron.windows.find(window => window.options.title === "Persona setup"), "the setup window");
      const response = await electron.connect({sender: setup.webContents, senderFrame: setup.webContents}, "https://new.example/");
      if (response.ok || response.error !== "synthetic hub connection failure") throw new Error("the real connection error was not preserved: " + JSON.stringify(response));
      if (setup.destroyed) throw new Error("setup closed before a failed connection recovered");
      const store = await import("./fixtures/lifecycle-store.mjs");
      const address = async () => (await electron.hubGet({sender: setup.webContents, senderFrame: setup.webContents})).address;
      if (await address() !== "") throw new Error("a failed first connection retained a hub owner");
      if (setup.destroyed) throw new Error("setup closed before a failed connection recovered");
      setup.close();
      await electron.waitFor(() => electron.windows.some(window => window.options.title === "Persona setup" && !window.destroyed), "setup recovery after closing the failed connection");
      if (await address() !== "") throw new Error("closing the failed connection retained a hub owner");
      for (let i = 0; i < 20 && !electron.windows.some(window => window.options.title === "Persona setup" && !window.destroyed); i++) await electron.settle();
      if (electron.windows.some(window => window.options.title === "Persona" && !window.destroyed && !window.url)) throw new Error("a blank hubless main window was created: " + JSON.stringify(electron.events));
      if (!electron.windows.some(window => window.options.title === "Persona setup" && !window.destroyed)) throw new Error("setup recovery did not occur: " + JSON.stringify(electron.events));
      const recovered = await electron.waitFor(() => electron.windows.find(window => window.options.title === "Persona setup" && !window.destroyed), "the recovered setup window");
      electron.scenario.failNew = false;
      const retry = await electron.connect({sender: recovered.webContents, senderFrame: recovered.webContents}, "https://new.example/");
      if (!retry.ok) throw new Error("recovered setup could not connect: " + JSON.stringify(retry));
      if (electron.windows.some(window => window.options.title === "Persona" && window.destroyed && !window.url && !electron.events.some(event => event.loading === window.number))) throw new Error("a retired blank hubless window was left: " + JSON.stringify(electron.events));
    ` : name === "prefix-root" ? `
      await electron.waitFor(() => electron.events.includes("tray-click-registered"), "the initial old-hub load and tray startup");
      await electron.app.emit("second-instance", {}, ["persona://link"]);
      const setup = await electron.waitFor(() => electron.windows.find(window => window.options.title === "Persona setup"), "the setup window");
      const response = await electron.connect({sender: setup.webContents, senderFrame: setup.webContents}, "https://old.example/");
      if (!response.ok) throw new Error("prefix-to-root replacement failed");
    ` : name === "destroyed-load" ? `
      const window = await electron.waitFor(() => electron.windows[0], "the initial hub window");
      await electron.waitFor(() => electron.controls.has("reject-old"), "the initial old-hub load");
      window.destroy();
      electron.controls.get("reject-old")(new Error("ERR_ABORTED: destroyed during a current-generation load"));
    ` : name === "geometry-failure" ? `
      const window = await electron.waitFor(() => electron.windows[0], "the initial hub window");
      (await import("./fixtures/lifecycle-store.mjs")).scenario.failWindowSave = true;
      window.emit("resize");
    ` : name === "tray-failure" ? `
      await electron.waitFor(() => electron.events.includes("tray-click-registered"), "the tray click handler");
      const window = await electron.waitFor(() => electron.windows[0], "the initial hub window");
      window.destroy();
      electron.scenario.failNext = true;
      const tray = electron.Tray.instances.at(-1);
      if (!tray) throw new Error("tray was not created: " + JSON.stringify(electron.events));
      tray.emit("click");
      for (let i = 0; i < 20 && !electron.windows.some(window => window.options.title === "Persona setup" && !window.destroyed); i++) await electron.settle();
    ` : name === "pending-close" ? `
      electron.scenario.failNew = true;
      await electron.waitFor(() => electron.controls.has("reject-old"), "the initial old-hub load");
      await electron.app.emit("second-instance", {}, ["persona://link"]);
      const setup = await electron.waitFor(() => electron.windows.find(window => window.options.title === "Persona setup"), "the setup window");
      const connecting = electron.connect({sender: setup.webContents, senderFrame: setup.webContents}, "https://new.example/");
      await electron.waitFor(() => electron.events.some(event => event.loading && event.url === "https://new.example/"), "the pending new-hub load");
      setup.close();
      const recovered = await electron.waitFor(() => electron.windows.find(window => window.options.title === "Persona setup" && !window.destroyed), "the local setup reopened while a load was pending");
      const response = await connecting;
      if (response.ok) throw new Error("a closed pending connection succeeded: " + JSON.stringify(response));
      await electron.settle();
      const store = await import("./fixtures/lifecycle-store.mjs");
      if (await store.currentHub() === undefined) throw new Error("the late load catch cleared the hub belonging to local setup");
      electron.scenario.failNew = false;
      const retry = await electron.connect({sender: recovered.webContents, senderFrame: recovered.webContents}, "https://new.example/");
      if (!retry.ok) throw new Error("the reopened local setup could not connect: " + JSON.stringify(retry));
    ` : `
      await electron.importMain();
      await electron.waitFor(() => electron.controls.has("reject-old"), "the initial old-hub load");
      electron.controls.get("reject-old")(new Error("synthetic current-load failure"));
    `}
    await electron.settle();
    process.stdout.write(JSON.stringify({events: electron.events, unhandled: electron.scenario.unhandled, updates: (await import("./fixtures/lifecycle-store.mjs")).updates, windows: electron.windows.map(window => ({title: window.options.title, url: window.url, destroyed: window.destroyed, partition: window.options.webPreferences.partition}))}));
    process.exit(0);
  `;
  const child = spawn(process.execPath, ["--import", pathToFileURL(loader).href, "--input-type=module", "--eval", script], { cwd: join(root, "desktop", "test"), env: { ...process.env, PERSONA_TEST_MAIN: pathToFileURL(main).href, PERSONA_DESKTOP_TEST_NO_PROTOCOL_REGISTRATION: "1" }, stdio: ["ignore", "pipe", "pipe"] });
  let stdout = "", stderr = "";
  child.stdout.on("data", chunk => { stdout += chunk; });
  child.stderr.on("data", chunk => { stderr += chunk; });
  const code = await new Promise(done => child.once("close", done));
  assert.equal(code, 0, stderr);
  return JSON.parse(stdout);
}

test("a deliberately destroyed old hub load cannot quit the app after its replacement succeeds", async () => {
  const { events, windows } = await run("stale");
  assert.equal(events.includes("app.quit"), false, JSON.stringify(events));
  assert.equal(events.some(value => value.title === "Persona could not start"), false);
  assert.equal(windows.at(-1).url, "https://new.example/");
  assert.equal(windows.at(-1).destroyed, false);
  assert.notEqual(windows.at(-1).partition, windows[0].partition);
});

test("a genuine current-generation load failure still rejects and destroys its window", async () => {
  const { events, windows } = await run("current");
  assert.equal(events.some(value => value.title === "Persona could not start"), true);
  assert.equal(events.includes("app.quit"), true);
  assert.equal(windows[0].destroyed, true);
});

test("destroying a current-generation pending load cannot hide its failure", async () => {
  const { events } = await run("destroyed-load");
  assert.equal(events.some(value => value.title === "Persona could not start"), true, JSON.stringify(events));
  assert.equal(events.includes("app.quit"), true);
});

test("a geometry-save rejection stays local and does not become an unhandled rejection", async () => {
  const { unhandled } = await run("geometry-failure");
  assert.deepEqual(unhandled, []);
});

test("same-origin prefix-to-root replaces the window, partition and saved hub", async () => {
  const { windows, updates } = await run("prefix-root");
  assert.equal(windows.at(-1).url, "https://old.example/");
  assert.notEqual(windows.at(-1).partition, windows[0].partition);
  assert.equal(windows[0].destroyed, true);
  assert.deepEqual(updates, [{ hub: "https://old.example/" }]);
});

test("a failed first-time connection preserves setup and reports the actual error", async () => {
  const { windows, events } = await run("connect-failure");
  const setup = windows.find(window => window.title === "Persona setup" && !window.destroyed);
  assert.equal(setup, undefined, JSON.stringify(events));
  assert.equal(windows.some(window => window.title === "Persona" && !window.destroyed && window.url === "https://new.example/"), true, JSON.stringify(events));
  assert.equal(windows.some(window => window.title === "Persona" && !window.destroyed && !window.url), false, JSON.stringify(events));
});

test("closing setup retires its pending load without clearing the local hub owner", async () => {
  const { windows, events, unhandled } = await run("pending-close");
  assert.deepEqual(unhandled, []);
  assert.equal(windows.at(-1).url, "https://new.example/", JSON.stringify(events));
  assert.equal(windows.at(-1).destroyed, false, JSON.stringify(events));
});

test("a failed tray load is handled and returns to setup", async () => {
  const { unhandled, windows, events } = await run("tray-failure");
  assert.deepEqual(unhandled, []);
  assert.equal(windows.some(window => window.title === "Persona setup" && !window.destroyed), true, JSON.stringify({events, windows}));
});
