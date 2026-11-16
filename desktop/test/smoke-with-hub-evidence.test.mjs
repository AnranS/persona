import assert from "node:assert/strict";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { createServer, request } from "node:http";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { startGateway } from "../scripts/smoke-gateway.mjs";
import { recordScenarioEvidence, smokeScenarioSummary } from "../scripts/smoke-evidence.mjs";

test("gateway strangers is a query-free diagnostic, not process attribution", async t => {
  const directory = await mkdtemp(join(tmpdir(), "persona-gateway-strangers-"));
  const upstream = createServer();
  let gateway;
  t.after(async () => {
    await gateway?.close();
    if (upstream.listening) await new Promise((done, fail) => { upstream.closeAllConnections(); upstream.close(error => error ? fail(error) : done()); });
    await rm(directory, { recursive: true, force: true });
  });
  await mkdir(join(directory, "dist"));
  await writeFile(join(directory, "dist", "index.html"), '<base href="/" />');
  await new Promise(done => upstream.listen(0, "127.0.0.1", done));
  gateway = await startGateway({ dist: join(directory, "dist"), mockPort: upstream.address().port, prefix: "/persona" });
  const response = await new Promise((done, reject) => {
    const req = request({ host: "127.0.0.1", port: gateway.port, path: "/outside?credential=not-recorded" }, res => {
      res.resume();
      res.once("end", () => done(res));
    });
    req.once("error", reject);
    req.end();
  });
  assert.equal(response.statusCode, 404);
  assert.deepEqual(gateway.strangers(), ["GET /outside (request)"]);
  assert.deepEqual(gateway.outside(), ["GET /outside (request)"]);
  assert.ok(!JSON.stringify({ strangers: gateway.strangers(), requests: gateway.requests() }).includes("not-recorded"));
});

test("metadata-free in-prefix 404 and malformed encoding remain bounded diagnostics", async t => {
  const directory = await mkdtemp(join(tmpdir(), "persona-gateway-request-errors-"));
  const upstream = createServer();
  let gateway;
  t.after(async () => {
    await gateway?.close();
    if (upstream.listening) await new Promise((done, fail) => { upstream.closeAllConnections(); upstream.close(error => error ? fail(error) : done()); });
    await rm(directory, { recursive: true, force: true });
  });
  await mkdir(join(directory, "dist"));
  await writeFile(join(directory, "dist", "index.html"), '<base href="/" />');
  await new Promise(done => upstream.listen(0, "127.0.0.1", done));
  gateway = await startGateway({ dist: join(directory, "dist"), mockPort: upstream.address().port, prefix: "/persona" });
  const missing = await new Promise((done, reject) => {
    const req = request({ host: "127.0.0.1", port: gateway.port, path: "/persona/private-local-path.txt?token=unrecorded" }, res => {
      res.resume();
      res.once("end", () => done(res));
    });
    req.once("error", reject);
    req.end();
  });
  assert.equal(missing.statusCode, 404);
  assert.deepEqual(gateway.strangers(), ["GET /persona/private-local-path.txt (request)"]);
  assert.ok(!JSON.stringify(gateway.strangers()).includes("unrecorded"));
  const malformed = await new Promise((done, reject) => {
    const req = request({ host: "127.0.0.1", port: gateway.port, path: "/persona/%E0%A4%A" }, res => {
      res.resume();
      res.once("end", () => done(res));
    });
    req.once("error", reject);
    req.end();
  });
  assert.equal(malformed.statusCode, 400);
  assert.equal(gateway.strangers().length, 2);
  assert.deepEqual(gateway.requests().at(-1), { method: "GET", path: "/persona/%E0%A4%A", status: 400, closed: true, complete: true });
  const apiMalformed = await fetch(`${gateway.url}api/v1/%E0%A4%A`);
  assert.equal(apiMalformed.status, 400);
  assert.deepEqual(gateway.requests().at(-1), { method: "GET", path: "/persona/api/v1/%E0%A4%A", status: 400, closed: true, complete: true });
});

test("upgrade requests remain in the query-free gateway journal", async t => {
  const directory = await mkdtemp(join(tmpdir(), "persona-gateway-upgrades-"));
  const upstream = createServer();
  let gateway;
  t.after(async () => {
    await gateway?.close();
    if (upstream.listening) await new Promise((done, fail) => { upstream.closeAllConnections(); upstream.close(error => error ? fail(error) : done()); });
    await rm(directory, { recursive: true, force: true });
  });
  await mkdir(join(directory, "dist"));
  await writeFile(join(directory, "dist", "index.html"), '<base href="/" />');
  await new Promise(done => upstream.listen(0, "127.0.0.1", done));
  gateway = await startGateway({ dist: join(directory, "dist"), mockPort: upstream.address().port, prefix: "/persona" });
  const { connect } = await import("node:net");
  const response = await new Promise((done, reject) => {
    const socket = connect(gateway.port, "127.0.0.1", () => socket.end("GET /persona/private-upgrade HTTP/1.1\r\nHost: localhost\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n"));
    let text = "";
    socket.on("data", chunk => { text += chunk; });
    socket.on("end", () => done(text));
    socket.on("error", reject);
  });
  assert.match(response, /^HTTP\/1\.1 404/);
  assert.deepEqual(gateway.requests(), [{ method: "UPGRADE", path: "/persona/private-upgrade", status: 404, closed: true, complete: true }]);
});

test("outer failures preserve the inner verdict, failed steps and screenshot index", () => {
  const inner = {
    ok: false,
    error: "notification permission denied",
    steps: [{ name: "signed in", ok: true }, { name: "replacement presence", ok: false }],
    screenshots: [{ name: "01-sign-in.png" }],
    presenceDiagnostics: [{ kind: "ipc-result", reply: { active: false } }],
  };
  const result = recordScenarioEvidence(inner, new Error("outer observation failed"));
  assert.equal(result.ok, false);
  assert.equal(result.error, "outer observation failed");
  assert.equal(result.outerFailure, "outer observation failed");
  assert.deepEqual(result.innerResult, inner);
  assert.deepEqual(result.steps, inner.steps);
  assert.deepEqual(result.presenceDiagnostics, inner.presenceDiagnostics);
  const withCleanup = { ...result, cleanupFailures: ["gateway close failed"] };
  assert.deepEqual(smokeScenarioSummary(withCleanup, "hub-change"), {
    ok: false,
    error: "outer observation failed",
    steps: inner.steps,
    screenshots: ["hub-change/01-sign-in.png"],
    innerResult: inner,
    presenceDiagnostics: inner.presenceDiagnostics,
    cleanupFailures: ["gateway close failed"],
    outerFailure: "outer observation failed",
    evidence: withCleanup,
  });
});

test("aggregate smoke evidence retains execution and gateway diagnostics", () => {
  const scenario = {
    ok: false, error: "Electron failed", steps: [{ name: "chat", ok: false }], screenshots: [{ name: "failure.png" }],
    outsidePrefix: ["GET /outside (request)"], otherLocalRequests: ["GET /persona/private-local-path.txt (request)"],
    exit: { code: 1, signal: null }, headless: false, hubStartMs: 42, hubStartAttempts: 2,
  };
  assert.deepEqual(smokeScenarioSummary(scenario, "root"), { ...scenario, screenshots: ["root/failure.png"], evidence: scenario });
});

test("legacy screenshot paths remain stable and arrays cannot crash aggregation", () => {
  assert.deepEqual(smokeScenarioSummary({ screenshots: "not-an-array" }, "root").screenshots, []);
  assert.deepEqual(smokeScenarioSummary({ screenshots: ["root/failure.png"] }, "root").screenshots, ["root/failure.png"]);
  assert.deepEqual(smokeScenarioSummary({ screenshots: [{ name: "root/failure.png" }] }, "root").screenshots, ["root/failure.png"]);
});

test("a missing inner result is an explicit failure, not a fabricated pass", () => {
  const result = recordScenarioEvidence(undefined, "observer failed before launch");
  assert.equal(result.ok, false);
  assert.equal(result.innerResult.ok, false);
  assert.deepEqual(result.steps, []);
  assert.deepEqual(smokeScenarioSummary(result).screenshots, []);
});
