import { createServer, request as httpRequest } from "node:http";
import { connect } from "node:net";
import { readFile, stat } from "node:fs/promises";
import { extname, join, resolve, sep } from "node:path";

/**
 * Development-only stand-in for how the hub serves the web app, for the desktop smoke: the built web client's files,
 * `index.html` for every page route, and `/api/` (HTTP and WebSocket) forwarded to the synthetic hub.
 *
 * With a prefix it behaves like a reverse proxy that mounts the hub below the origin root: only paths inside the
 * prefix exist, the prefix is stripped before forwarding, and `index.html` gets `<base href="/<prefix>/">` as the hub
 * writes it for `X-Forwarded-Prefix` (docs/web/web.md "Subpaths"). Every request outside the prefix is answered 404
 * and recorded without its query. The isolated acceptance scenario fails closed on any such request; the loopback
 * listener cannot establish from optional headers whether Electron or another local client sent it.
 */
const TYPES = {
  ".html": "text/html; charset=utf-8", ".js": "text/javascript", ".css": "text/css", ".json": "application/json",
  ".svg": "image/svg+xml", ".png": "image/png", ".ico": "image/x-icon", ".woff2": "font/woff2", ".wasm": "application/wasm",
  ".webmanifest": "application/manifest+json", ".txt": "text/plain; charset=utf-8", ".br": "application/octet-stream",
};

export async function startGateway({ dist, mockPort, prefix = "" }) {
  if (prefix && !/^\/[A-Za-z0-9._~-]+$/.test(prefix)) throw new Error("The smoke prefix must be one clean path segment");
  const root = resolve(dist);
  const index = (await readFile(join(root, "index.html"), "utf8"));
  if (!index.includes('<base href="/" />')) throw new Error("web/dist/index.html has no <base href=\"/\" /> to rewrite");
  const page = Buffer.from(prefix ? index.replace('<base href="/" />', `<base href="${prefix}/" />`) : index, "utf8");
  const outside = [];
  const requests = [];
  const strangers = [];
  /** A request outside the prefix fails acceptance even without browser metadata. Optional headers are recorded only
   *  as diagnostic labels, not as evidence of which local process sent the request. */
  const escaped = (req, method) => {
    const label = req.headers["sec-fetch-dest"] ?? (req.headers.origin ? "socket" : "request");
    const entry = `${method} ${(req.url ?? "").split("?")[0]} (${label})`;
    outside.push(entry);
  };
  const sockets = new Set();
  const metadataFree = req => !req.headers["sec-fetch-dest"] && !req.headers["sec-fetch-mode"] && !req.headers.origin;
  const stranger = (req, method) => {
    if (metadataFree(req)) strangers.push(`${method} ${(req.url ?? "").split("?")[0]} (request)`);
  };

  /** The path inside the hub, or undefined for a request the proxy would never forward. */
  const inner = url => {
    if (!prefix) return url;
    if (url === prefix || url.startsWith(`${prefix}?`)) return `/${url.slice(prefix.length)}`;
    return url.startsWith(`${prefix}/`) ? url.slice(prefix.length) : undefined;
  };

  /** A recoverable API response is evidence only with its exact path and parsed public error category. */
  const upstreamCategory = (path, body) => {
    const endpoint = path.split("?")[0];
    const apiPath = prefix && endpoint.startsWith(`${prefix}/`) ? endpoint.slice(prefix.length) : endpoint;
    if (apiPath !== "/api/v1/inbox/done/days") return undefined;
    try {
      const value = JSON.parse(body);
      if (value && Object.keys(value).length === 1 && value.error === "tz must be a time zone name") return "unsupported-time-zone";
    } catch {}
    return undefined;
  };

  const server = createServer(async (req, res) => {
    const path = inner(req.url ?? "/");
    const entry = { method: req.method, path: (req.url ?? "/").split("?")[0] };
    requests.push(entry);
    stranger(req, req.method);
    const clientCancellation = () => {
      if (req.aborted || !res.writableFinished) {
        entry.clientAborted = true;
        entry.status = "client-aborted";
      }
    };
    res.on("finish", () => { entry.status = res.statusCode; });
    res.once("close", () => {
      clientCancellation();
      entry.closed = true;
      entry.complete = res.writableFinished;
    });
    req.once("aborted", () => { entry.clientAborted = true; entry.status = "client-aborted"; });
    if (path === undefined) {
      escaped(req, req.method);
      res.writeHead(404, { "Content-Type": "text/plain" }).end("outside the hub prefix");
      return;
    }
    try { decodeURIComponent(path.split("?")[0]); }
    catch {
      res.writeHead(400, { "Content-Type": "text/plain" }).end("malformed URL encoding");
      return;
    }
    if (path.startsWith("/api/")) {
      const upstream = httpRequest({ host: "127.0.0.1", port: mockPort, path, method: req.method, headers: { ...req.headers, host: `127.0.0.1:${mockPort}` } }, answer => {
        answer.on("aborted", () => { entry.upstreamError = true; res.destroy(); });
        answer.on("error", () => { entry.upstreamError = true; res.destroy(); });
        const chunks = [];
        answer.on("data", chunk => { chunks.push(chunk); });
        answer.on("end", () => {
          if (answer.statusCode === 400 && answer.headers["content-type"]?.startsWith("application/json")) {
            entry.responseCategory = upstreamCategory(path, Buffer.concat(chunks).toString("utf8")) ?? "unclassified";
          }
        });
        res.writeHead(answer.statusCode ?? 502, answer.headers);
        answer.pipe(res);
      });
      upstream.on("error", error => {
        if (!entry.clientAborted) entry.upstreamError = error.message;
        if (!res.headersSent) res.writeHead(502);
        res.end();
      });
      upstream.on("close", () => {
        // A close after a response has begun is settled by the response/downstream close handlers. Before that it is
        // an upstream failure even if Node lets the downstream socket finish an empty default response.
        if (!res.headersSent && !entry.clientAborted) { entry.upstreamError ??= "upstream closed before a response"; res.destroy(); }
      });
      // A client that closes before a complete response has cancelled its read. Record that from the response's
      // terminal state too: Node does not emit the legacy request `aborted` event for every such cancellation.
      req.once("aborted", () => { entry.clientAborted = true; entry.status = "client-aborted"; });
      req.pipe(upstream);
      return;
    }
    let pathname;
    try { pathname = decodeURIComponent(path.split("?")[0]); }
    catch {
      res.writeHead(400, { "Content-Type": "text/plain" }).end("malformed URL encoding");
      return;
    }
    const file = resolve(join(root, pathname));
    let body = page, type = TYPES[".html"];
    if (file.startsWith(root + sep) && extname(file) && (await stat(file).catch(() => undefined))?.isFile() && file !== join(root, "index.html")) {
      body = await readFile(file);
      type = TYPES[extname(file)] ?? "application/octet-stream";
    } else if (extname(pathname) && pathname !== "/index.html") {
      res.writeHead(404, { "Content-Type": "text/plain" }).end("not found");
      return;
    }
    res.writeHead(200, { "Content-Type": type, "Content-Length": body.length, "Cache-Control": "no-cache", "X-Content-Type-Options": "nosniff" }).end(body);
  });

  server.on("upgrade", (req, socket, head) => {
    stranger(req, "UPGRADE");
    const path = inner(req.url ?? "/");
    const entry = { method: "UPGRADE", path: (req.url ?? "").split("?")[0], status: 404, closed: false, complete: false };
    requests.push(entry);
    if (path === undefined || !path.startsWith("/api/")) {
      if (path === undefined) escaped(req, "UPGRADE");
      socket.end("HTTP/1.1 404 Not Found\r\n\r\n");
      entry.closed = true;
      entry.complete = true;
      return;
    }
    try { decodeURIComponent(path.split("?")[0]); }
    catch {
      entry.status = 400;
      socket.end("HTTP/1.1 400 Bad Request\r\n\r\n");
      entry.closed = true;
      entry.complete = true;
      return;
    }
    entry.status = "connecting";
    let upgraded = false;
    const upstream = connect(mockPort, "127.0.0.1", () => {
      entry.status = "connecting";
      const headers = [];
      for (let i = 0; i < req.rawHeaders.length; i += 2) {
        headers.push(`${req.rawHeaders[i]}: ${req.rawHeaders[i].toLowerCase() === "host" ? `127.0.0.1:${mockPort}` : req.rawHeaders[i + 1]}`);
      }
      upstream.write(`GET ${path} HTTP/1.1\r\n${headers.join("\r\n")}\r\n\r\n`);
      let response = "";
      upstream.on("data", chunk => {
        if (upgraded) return;
        response += chunk.toString("latin1");
        const end = response.indexOf("\r\n\r\n");
        if (end < 0) return;
        const status = Number(response.slice(9, 12));
        entry.status = Number.isInteger(status) ? status : "invalid-response";
        if (status === 101) upgraded = true;
      });
      if (head.length) upstream.write(head);
      upstream.pipe(socket);
      socket.pipe(upstream);
    });
    for (const end of [socket, upstream]) {
      sockets.add(end);
      end.on("error", () => { entry.clientDisconnected = true; socket.destroy(); upstream.destroy(); });
      end.on("close", () => {
        entry.closed = true;
        entry.complete = entry.status === 101 && upgraded && !entry.upstreamError;
        sockets.delete(end);
        socket.destroy();
        upstream.destroy();
      });
    }
  });

  await new Promise((done, fail) => { server.once("error", fail); server.listen(0, "127.0.0.1", done); });
  const port = server.address().port;
  let closed;
  const close = () => {
    if (!closed) {
      closed = (async () => {
        for (const socket of sockets) socket.destroy();
        server.closeAllConnections();
        await new Promise((done, fail) => server.close(error => { if (error) fail(error); else done(); }));
      })();
    }
    return closed;
  };
  return {
    port,
    url: `http://127.0.0.1:${port}${prefix}/`,
    /** Unattributed requests that reached this test-owned listener, as "METHOD path (diagnostic label)" without their query. */
    outside: () => [...outside],
    /** Query-free requests without browser metadata, as diagnostics only; their local-process identity is unknown. */
    strangers: () => [...strangers],
    /** Method, query-free path and response status only; never credentials, bodies or device tokens. */
    requests: () => [...requests],
    async close() { await close(); },
  };
}
