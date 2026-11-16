import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdir, readFile, rename, stat, writeFile } from "node:fs/promises";
import { homedir, platform } from "node:os";
import { dirname, posix, relative, resolve, sep } from "node:path";

export const PAIR_INPUT = "persona://link?";
export const SERVICE_SENTINEL = "real Link smoke: systemctl access is unavailable";

function windowsComparable(value) {
  const text = String(value).replace(/^\\\\[?.]\\(?:UNC\\)?/i, match => match.toLowerCase().startsWith("\\\\?\\unc\\") ? "//" : "").replace(/^\/\/[?.]\/(?:UNC\/)?/i, match => match.toLowerCase().startsWith("//?/unc/") ? "//" : "").replaceAll("/", "\\");
  return text.replaceAll("\\", "/");
}

/**
 * Path redaction safety contract (explicit, bounded; replaces the previous incremental scanner):
 *
 * - Only real descendants of an explicitly attributed root survive as `<run>/…`.
 *   Roots: POSIX absolute paths, `C:\…` drives, `\\server\share\…` UNC.
 * - Namespace (`\\?\`, `//?/`) input is normalized before classification; when the
 *   normalized form is unambiguously under the run root it is preserved, otherwise
 *   the whole token becomes `<path>`.
 * - Ambiguous, unresolved, sparse-separator, mismatched-UNC or drive-relative
 *   inputs collapse to `<path>`. No private path, sibling, or namespace device
 *   path may leak.
 * - Non-path diagnostic text (e.g. `detail=keep`, ` unexpected exception`) is
 *   preserved. Path tokens are terminated by structural separators
 *   (`"'`, backtick, `;`, newlines, `[`, `]`), not by spaces or commas: a
 *   space or comma that continues an unambiguous owned path stays inside the
 *   replacement; one that would extend an outside path is treated as ordinary
 *   evidence text.
 *
 * Lexical identity only: this never authorizes recursive deletion through symlinks.
 */
export function classifyOwnedPath(run, candidate) {
  if (typeof run !== "string" || typeof candidate !== "string") return "outside";
  // An embedded `//?/` or `//./` namespace device after the leading prefix is a
  // separate, unowned path that happens to follow an owned one; it must not be
  // treated as an owned descendant. A leading `//?/` is the candidate's own
  // namespace prefix and is normalized below.
  const embedded = candidate.slice(1).includes("//?/") || candidate.slice(1).includes("//./");
  if (embedded) return "outside";
  const windows = value => /^(?:[A-Za-z]:[\\/]|\\\\|\/\/[?.]\/)/.test(value) && !/^\/\/[?.]\/UNC\//i.test(value);
  const unc = value => /^\/\/[^/]+[\\/][^/]+[\\/]/.test(value) && !/^\/\/[?.]\//.test(value);
  const uncNamespace = value => /^\/\/[?.]\/UNC\//i.test(value);
  // A UNC namespace path and a plain UNC path are the same ownership family.
  if (windows(run) !== windows(candidate) || (unc(run) !== unc(candidate) && !(uncNamespace(run) && unc(candidate)) && !(unc(run) && uncNamespace(candidate)))) return "outside";
  if (windows(run) || windows(candidate) || unc(run) || unc(candidate) || uncNamespace(run) || uncNamespace(candidate)) {
    const root = windowsComparable(run).replace(/^\/\/[?.]\/(?:UNC\/)?/i, match => match.toLowerCase().startsWith("//?/unc/") ? "//" : "").toLowerCase();
    const path = windowsComparable(candidate).replace(/^\/\/[?.]\/(?:UNC\/)?/i, match => match.toLowerCase().startsWith("//?/unc/") ? "//" : "").toLowerCase();
    if (root === path) return "root";
    return path.startsWith(root.endsWith("/") ? root : `${root}/`) ? "descendant" : "outside";
  }
  if (!run.startsWith(sep) || !candidate.startsWith(sep)) return "outside";
  // A semicolon is a shell command separator, not a filename character in this
  // receipt contract. Any path expression containing it is unowned evidence.
  if (candidate.includes(";")) return "outside";
  const descendant = relative(resolve(run), resolve(candidate));
  if (descendant === "") return "root";
  if (descendant === ".." || descendant.startsWith(`..${sep}`) || descendant.startsWith(sep)) return "outside";
  return "descendant";
}

/** Strip trailing separators for comparison, but keep `/` and drive roots valid. */
function trimTrailing(value) {
  return value.replace(/[\\/]+$/, "") || value;
}

/** Map each listed path to its receipt replacement under the explicit safety contract. */
export function redactPaths(run, paths = []) {
  return [run, ...paths.filter(Boolean), homedir()].map(path => {
    const kind = classifyOwnedPath(run, path);
    let replacement = "<path>";
    if (kind === "root") replacement = "<run>";
    else if (kind === "descendant") {
      const root = trimTrailing(windowsComparable(run));
      const rest = trimTrailing(windowsComparable(path)).slice(root.length).replace(/^\/+/, "");
      replacement = `<run>/${rest}`;
    }
    return { path, replacement };
  });
}

/**
 * Replace exactly one path token. `text` starts at the token's first character.
 * Returns `{ replacement, consumed }` or `undefined` when no path starts here.
 */
function redactOnePath(text, run) {
  // Structural separators always end a path token. Spaces and commas may be
  // ordinary filename characters; they are handled below by checking whether
  // the text after them continues the path or starts a new token.
  const terminator = /[\r\n"'`;]/;
  const isWindowsRoot = /^(?:[A-Za-z]:[\\/]|\\\\|\/\/)/.test(run);
  const windowsToken = /^(?:\\\\[?.]\\|\/\/[?.]\/|[A-Za-z]:[\\/]|[A-Za-z]:[^/\\]+\\|\\\\|\/\/[^/]+[\\/])/.exec(text);
  let tokenText;
  if (windowsToken) {
    const rest = text.slice(windowsToken[0].length);
    const end = terminator.exec(rest)?.index ?? rest.length;
    tokenText = windowsToken[0] + rest.slice(0, end);
    // A space or comma followed by an absolute path token (optionally wrapped in
    // brackets) is a field boundary, not part of this filename. Truncate before
    // it so the next path is seen.
    const boundary = /[ ,]\[?(?=(?:[A-Za-z]:[\\/]|\\\\|\/\/))/.exec(rest);
    if (boundary) tokenText = windowsToken[0] + rest.slice(0, boundary.index);
    // An embedded namespace device (`//?/`, `//./`) or UNC (`//server/share`)
    // is a separate path token, not a component of this one. Split before it so
    // each token is classified independently: the owned prefix survives, the
    // unowned tail collapses.
    const embedded = rest.search(/\/\/[?.]\/|\/\/[^/]+\/[^/]+\//);
    if (embedded !== -1) tokenText = windowsToken[0] + rest.slice(0, embedded);
  } else if (text.startsWith("/")) {
    if (isWindowsRoot) return undefined;
    // `/` may itself be the run root. Replace the bare root only when it is not
    // immediately followed by more path components; `/tmp/run` must stay one token.
    if (trimTrailing(run) === "/" && !text.startsWith("//") && !/^\/[^\s"'`;,:)\]}]/.test(text)) return { replacement: "<run>", consumed: 1 };
    // POSIX filenames may contain spaces, commas and brackets. The token ends at
    // a diagnostic boundary (` key=value`, ` failed`) or at a new absolute path;
    // ordinary spaces inside the path are kept. A semicolon followed by a path
    // component is part of the same path expression, not a command separator.
    const match = /^\/(?:[^\s"'`;)]+\/?)+/.exec(text);
    if (!match) return undefined;
    tokenText = match[0];
    for (;;) {
      const next = /^[;]([^\s"'`;,\[\]]+)/.exec(text.slice(tokenText.length));
      if (!next) break;
      tokenText += next[0];
    }
  } else return undefined;

  const raw = trimTrailing(tokenText);
  if (!raw) return { replacement: tokenText, consumed: tokenText.length };
  // Receipt punctuation (`root,`, `root:`, `root;`) is not part of the path.
  // Strip it for classification and emit it after the replacement.
  const core = raw.replace(/[,:]+$/, "");
  const suffix = raw.slice(core.length);
  const kind = classifyOwnedPath(run, core);

  if (kind === "root" || kind === "descendant") {
    // The core path is owned. Extend over spaces/commas only while the result
    // remains unambiguously under the run root (e.g. `run/Private,Customer/x`).
    // A following absolute path token is never part of this one: it is the next
    // diagnostic field and must be redacted separately.
    let extended = tokenText;
    for (;;) {
      const match = /^[ ,]([^\s"'`;,\[\]]+)/.exec(text.slice(extended.length));
      if (!match) break;
      if (/^(?:[A-Za-z]:[\\/]|\\\\|\/\/)/.test(match[1])) break;
      // Do not extend into a suffix that repeats the run root: that is a new
      // path field, not a filename component of the current one.
      if (match[1].includes(run)) break;
      const candidate = trimTrailing(extended + match[0]);
      const candidateCore = candidate.replace(/[,:]+$/, "");
      if (classifyOwnedPath(run, candidateCore) === "descendant") extended += match[0];
      else break;
    }
    const extendedRaw = trimTrailing(extended);
    const extendedCore = extendedRaw.replace(/[,:]+$/, "");
    const extendedSuffix = extendedRaw.slice(extendedCore.length);
    if (classifyOwnedPath(run, extendedCore) === "root") return { replacement: `<run>${extendedSuffix}`, consumed: extended.length };
    const normalizedRoot = trimTrailing(windowsComparable(run));
    const normalized = trimTrailing(windowsComparable(extendedCore));
    const rest = normalized.slice(normalizedRoot.length).replace(/^\/+/, "");
    return { replacement: `<run>/${rest}${extendedSuffix}`, consumed: extended.length };
  }

  // The path is outside the run root. A drive/UNC/namespace token is wholly
  // opaque: spaces and commas belong to its private components, so the entire
  // token collapses. A POSIX token stops at the first whitespace or comma; any
  // suffix there is ordinary diagnostic text and must survive.
  if (windowsToken) {
    const diagnostics = /[ ,](?=[A-Za-z][\w.-]*=|(?:failed|error|cannot|denied|unexpected)\b)/i;
    const match = diagnostics.exec(tokenText);
    const consumed = match ? match.index : tokenText.length;
    // A trailing bracket is receipt punctuation, not part of the private path.
    const bracket = tokenText.endsWith("]") && consumed === tokenText.length;
    return { replacement: bracket ? "<path>]" : "<path>", consumed };
  }
  // POSIX paths may contain commas and spaces as ordinary filename characters.
  // Extend over spaces that continue the path; stop at a diagnostic boundary
  // (` key=value`, ` failed`) or at a new absolute path token.
  let extended = tokenText;
  for (;;) {
    const next = /^[ ]([^\s"'`;,\[\]]+)/.exec(text.slice(extended.length));
    if (!next) break;
    if (/^(?:[A-Za-z]:[\\/]|\\\\|\/\/)/.test(next[1])) break;
    if (/^[A-Za-z][\w.-]*=/.test(next[1])) break;
    if (/^(?:failed|error|cannot|denied|unexpected)\b/i.test(next[1])) break;
    if (next[1].startsWith("/")) break;
    extended += next[0];
  }
  const diagnostics = /[\s,](?=[A-Za-z][\w.-]*=|(?:failed|error|cannot|denied|unexpected)\b)/i;
  const match = diagnostics.exec(extended);
  return { replacement: "<path>", consumed: match ? match.index : extended.length };
}

/** Redact every path token in a receipt string; arbitrary encodings are outside this contract. */
function redactPathString(text, run) {
  let output = "";
  for (let start = 0; start < text.length;) {
    const result = redactOnePath(text.slice(start), run);
    if (result) {
      output += result.replacement;
      start += result.consumed;
    } else output += text[start++];
  }
  return output;
}

/** Redact only the path-bearing text that this receipt owns; arbitrary encodings are outside this contract. */
export function sanitize(value, run, paths = []) {
  let text = String(value ?? "")
    .replace(/(?:persona|jarvis):\/\/(?:link|pair)\?[^\s"']*/gi, "<pairing-link>");
  text = redactPathString(text, run)
    .replace(/\b(?:PERSONA_|JARVIS_)?[A-Z0-9_]*(?:TOKEN|PASSWORD|SECRET|CODE|FP)=\S+/g, match => `${match.slice(0, match.indexOf("=") + 1)}<redacted>`)
    .replace(/([?&](?:token|code|fp|password)=)[^\s&"']*/gi, "$1<redacted>")
    .replace(/Bearer\s+[\x21-\x7e]+/g, "Bearer <redacted>")
    .replace(/(?:persona|jarvis):\/\/(?:link|pair)\?[^\s"']*/gi, "<pairing-link>")
    .replace(/([?&](?:token|code|fp|password)=)[^\s&"']*/gi, "$1<redacted>")
    .replace(/Bearer\s+[\x21-\x7e]+/g, "Bearer <redacted>");
  if (text.length > 128 * 1024) {
    const marker = "\n<evidence truncated after sanitization>";
    text = `${text.slice(0, 128 * 1024 - marker.length)}${marker}`;
  }
  return text.replace(/[\u001b\u009b][[\]()#;?]*(?:(?:(?:[a-zA-Z\d]*(?:;[-a-zA-Z\d\/#&.:=?%@~_]+)*)?\u0007)|(?:(?:\d{1,4}(?:;\d{0,4})*)?[\dA-PR-TZcf-nq-uy=><~]))/g, "");
}

export async function atomicWriteJSON(file, value) {
  await mkdir(dirname(file), { recursive: true });
  const temporary = `${file}.${process.pid}.tmp`;
  await writeFile(temporary, `${JSON.stringify(value, null, 2)}\n`, { mode: 0o600 });
  await rename(temporary, file);
}

/** Redact each string before serializing; never truncate a complete JSON document into invalid syntax. */
export function sanitizeJSON(value, run, paths = []) {
  if (typeof value === "string") return sanitize(value, run, paths);
  if (Array.isArray(value)) return value.map(item => sanitizeJSON(item, run, paths));
  if (value && typeof value === "object") {
    const result = {};
    for (const [key, item] of Object.entries(value)) {
      const redactedKey = sanitize(key, run, paths);
      if (["__proto__", "prototype", "constructor"].includes(redactedKey) || Object.hasOwn(result, redactedKey)) throw new Error("redaction would merge or shadow distinct receipt keys");
      result[redactedKey] = sanitizeJSON(item, run, paths);
    }
    return result;
  }
  return value;
}

export async function writeSanitizedJSON(file, value, run, paths = []) {
  await atomicWriteJSON(file, sanitizeJSON(value, run, paths));
}

export async function assertSensitiveTextAbsent(files, secrets) {
  for (const file of files) {
    if (!await exists(file)) continue;
    const text = await readFile(file, "utf8");
    for (const secret of secrets.filter(Boolean)) {
      if (text.includes(secret) || text.includes(JSON.stringify(secret).slice(1, -1))) throw new Error(`Unredacted test-only or private value in ${file}`);
    }
  }
}

/**
 * Replace a previous verdict even when final provenance or privacy verification fails.
 * This helper only writes a failure receipt; the caller must retain its owned run root on failure.
 */
function provenanceFailure(provenance, expected) {
  if (!expected || typeof expected.runID !== "string" || !expected.runID || typeof expected.binary !== "string" || typeof expected.electron !== "string" || typeof expected.go !== "string" || !expected.dist || !expected.scripts || Object.keys(expected.dist).length === 0 || Object.keys(expected.scripts).length === 0) return true;
  if (!provenance || typeof provenance !== "object" || Array.isArray(provenance) || typeof provenance.runID !== "string" || !provenance.runID || !provenance.source) return true;
  if (expected.runID !== undefined && provenance.runID !== expected.runID) return true;
  for (const name of ["binary", "electron", "go"]) {
    if (expected[name] !== undefined && provenance[name] !== expected[name]) return true;
  }
  for (const group of ["dist", "scripts"]) {
    if (!provenance[group] || typeof provenance[group] !== "object" || Array.isArray(provenance[group]) || Object.keys(provenance[group]).length === 0) return true;
    for (const [name, digest] of Object.entries(expected[group] ?? {})) if (provenance[group][name] !== digest) return true;
  }
  const values = Object.values({ binary: provenance.binary, electron: provenance.electron, go: provenance.go, ...provenance.dist, ...provenance.scripts });
  if (values.some(value => typeof value !== "string" || !/^[a-f0-9]{64}$/.test(value))) return true;
  if (Object.keys(expected).some(key => !["runID", "binary", "electron", "go", "dist", "scripts"].includes(key))) return true;
  return false;
}

export function receiptVerdict({ commands = [], provenance, expectedProvenance, cleanup } = {}) {
  const commandFailure = !Array.isArray(commands) || commands.length === 0 || commands.some(command => !command || command.closed !== true || command.code !== 0 || command.signal !== null || command.error || command.timedOut || command.closeTimedOut || command.escalationError);
  // Original-PGID emptiness is not proof that a detached descendant exited. Generic captured commands cannot
  // authorize removal of the whole run root; a different, separately proven descendant-liveness contract is required.
  const descendantProofMissing = true;
  const invalidProvenance = provenanceFailure(provenance, expectedProvenance);
  return {
    ok: false,
    mayRemoveRunRoot: false,
    descendantProofMissing,
    commandFailure,
    provenanceFailure: invalidProvenance,
    cleanupFailure: cleanup?.ok !== true,
  };
}

export async function readProvenance(file, expected) {
  let value;
  try { value = JSON.parse(await readFile(file, "utf8")); } catch { return undefined; }
  return receiptVerdict({ provenance: value, expectedProvenance: expected }).provenanceFailure ? undefined : value;
}

export async function writeFailureReceipt(file, { schema = 1, runID, error, artifacts = [], run, paths = [] }) {
  if (!error) throw new Error("a failure receipt requires an actual error");
  const result = {
    schema,
    ok: false,
    verdict: "Development receipt contract: FAIL",
    error: String(error instanceof Error ? error.message : error),
    artifacts,
    cleanup: { ok: false, removed: false, reason: "failure receipts preserve the owned run root" },
    ...(runID ? { runID } : {}),
  };
  if (!run) {
    // No paths are available for a safe persisted failure message; retain only the fixed verdict.
    result.error = "Receipt failed before path redaction was available";
    result.artifacts = [];
  }
  await writeSanitizedJSON(file, result, run ?? file, paths).catch(async () => {
    // Never repeat an unsanitized exception in a fallback. A failed fallback write remains a thrown publication error.
    await atomicWriteJSON(file, { schema, ok: false, verdict: "Development receipt contract: FAIL", error: "Receipt redaction failed", cleanup: { ok: false, removed: false } });
  });
  return result;
}

export function digest(value) {
  return createHash("sha256").update(value).digest("hex");
}

export async function fileDigest(file) {
  return digest(await readFile(file));
}

export async function exists(file) {
  return stat(file).then(() => true, () => false);
}

function termination(child, signal) {
  // The leader can exit while a descendant retains stdout/stderr. Until close, its owned group is still relevant.
  if (process.platform === "win32" && (child.exitCode !== null || child.signalCode !== null)) return;
  if (!Number.isInteger(child.pid) || child.pid <= 0) throw new Error("no owned process group PID");
  try {
    if (process.platform === "win32") child.kill(signal);
    else process.kill(-child.pid, signal);
  } catch (error) {
    if (error.code !== "ESRCH") throw error;
  }
}

/**
 * POSIX-only observation of one newly created process group. /proc is read only for this exact PGID;
 * no private process environment or unrelated process metadata is read. This proves only that the original process
 * group is empty; it cannot detect a descendant that detached into another process group.
 */
async function observeOwnedGroup(child, timeout) {
  if (platform() !== "linux" || !Number.isInteger(child.pid) || child.pid <= 0) {
    return { groupEmpty: false, error: "a Linux owned-process-group receipt is required" };
  }
  const group = child.pid;
  const source = `
    import { readdir, readFile } from 'node:fs/promises';
    import { argv, exit } from 'node:process';
    const group = Number(argv[1]);
    const deadline = Number(argv[2]);
    if (!Number.isSafeInteger(group) || group <= 0 || !Number.isFinite(deadline)) exit(2);
    const sleep = ms => new Promise(resolve => setTimeout(resolve, Math.min(ms, 20)));
    try {
      for (;;) {
        const entries = await readdir('/proc');
        const remaining = [];
        for (const entry of entries) {
          if (!/^\\d+$/.test(entry)) continue;
          const stat = await readFile('/proc/' + entry + '/stat', 'utf8').catch(() => '');
          const end = stat.lastIndexOf(')');
          const fields = stat.slice(end + 2).split(' ');
          if (end < 0 || fields.length < 3 || Number(fields[2]) !== group || fields[0] === 'Z') continue;
          remaining.push(Number(entry));
        }
        if (remaining.length === 0) { console.log(JSON.stringify({ groupEmpty: true, observedGroup: group })); exit(0); }
        if (Date.now() >= deadline) { console.log(JSON.stringify({ groupEmpty: false, remaining })); exit(3); }
        await sleep(deadline - Date.now());
      }
    } catch (error) { console.error(JSON.stringify({ groupEmpty: false, error: error.message })); exit(2); }
  `;
  return new Promise(resolve => {
    let watcher;
    try {
      watcher = spawn(process.execPath, ["--input-type=module", "-e", source, String(group), String(Date.now() + timeout)], {
        stdio: ["ignore", "pipe", "pipe"], shell: false, detached: true,
      });
    } catch (error) {
      resolve({ groupEmpty: false, error: `owned-group observer: ${error.message}` });
      return;
    }
    let stdout = "", stderr = "", timer, spawnError;
    watcher.stdout.on("data", chunk => { stdout += chunk; });
    watcher.stderr.on("data", chunk => { stderr += chunk; });
    watcher.on("error", error => {
      clearTimeout(timer);
      spawnError = error.message;
      resolve({ groupEmpty: false, error: `owned-group observer: ${spawnError}` });
    });
    const stop = signal => {
      if (watcher.exitCode !== null || watcher.signalCode !== null) return;
      try { process.kill(-watcher.pid, signal); } catch { /* the close event below decides */ }
    };
    watcher.once("close", (code, signal) => {
      clearTimeout(timer);
      if (spawnError) return;
      let receipt;
      try { receipt = JSON.parse(stdout); } catch { receipt = { groupEmpty: false }; }
      resolve({ ...receipt, groupEmpty: receipt.groupEmpty === true && code === 0 && signal === null, watcher: { code, signal, stderr: stderr.slice(0, 1000) } });
    });
    timer = setTimeout(() => { stop("SIGTERM"); setTimeout(() => stop("SIGKILL"), 100).unref(); }, timeout + 1000);
  });
}

/**
 * Capture a real child close, including its nonzero code or signal. POSIX uses a new owned process group for bounded
 * termination, including a leader that exited while a descendant retained the captured pipes. Windows terminates only
 * the direct child and cannot approve a group receipt. Detached descendants and PGID-reuse are outside what this
 * observation can prove; closing captured pipes and an empty original group are not proof all descendants exited.
 */
export function runCaptured(executable, args, options = {}) {
  const {
    timeout = 120_000, terminateGrace = 1000, closeGrace = 1000,
    input, ...spawnOptions
  } = options;
  for (const [name, value] of Object.entries({ timeout, terminateGrace, closeGrace })) {
    if (!Number.isInteger(value) || value < 0 || value > 2_147_483_647) throw new RangeError(`${name} must be a nonnegative supported timer duration`);
  }
  return new Promise(resolve => {
    let stdout = "", stderr = "", error, spawnError, stdinError, timedOut = false, closeTimedOut = false, escalationError;
    let settled = false;
    let terminateTimer, killTimer, closeTimer;
    const startedAt = new Date().toISOString();
    let child;
    try {
      child = spawn(executable, args, { shell: false, stdio: ["pipe", "pipe", "pipe"], ...spawnOptions, ...(process.platform === "win32" ? {} : { detached: true }) });
    } catch (cause) {
      resolve({ executable, args: [...args], code: null, signal: null, error: cause.message, timedOut: false, closeTimedOut: false, closed: false, stdout, stderr, startedAt, endedAt: new Date().toISOString() });
      return;
    }
    const finish = async (code, signal) => {
      if (settled) return;
      settled = true;
      for (const timer of [terminateTimer, killTimer, closeTimer]) clearTimeout(timer);
      let group = { groupEmpty: false, error: platform() === "linux" ? "group observation not completed" : "owned process-group observation is unavailable on this platform" };
      if (platform() === "linux") group = await observeOwnedGroup(child, closeGrace);
      const groupError = group.error ?? (group.groupEmpty ? undefined : "owned process group was not observed empty");
      escalationError = [escalationError, groupError].filter(Boolean).join("; ") || undefined;
      resolve({ executable, args: [...args], pid: child.pid, code, signal, error, spawnError, stdinError, timedOut, closeTimedOut, escalationError, closed: !closeTimedOut, groupEmpty: group.groupEmpty === true, group: { groupEmpty: group.groupEmpty === true, remaining: group.remaining, error: group.error }, stdout, stderr, startedAt, endedAt: new Date().toISOString() });
    };
    const stop = (signal, field) => {
      try { termination(child, signal); } catch (cause) {
        const message = `${field}: ${cause.message}`;
        escalationError = escalationError ? `${escalationError}; ${message}` : message;
      }
    };
    terminateTimer = setTimeout(() => {
      timedOut = true;
      stop("SIGTERM", "terminate");
      killTimer = setTimeout(() => {
        stop("SIGKILL", "kill");
        closeTimer = setTimeout(() => { closeTimedOut = true; finish(null, null); }, closeGrace);
      }, terminateGrace);
    }, timeout);
    child.stdout.on("data", chunk => { if (stdout.length < 128 * 1024) stdout += chunk.toString("utf8").slice(0, 128 * 1024 - stdout.length); });
    child.stderr.on("data", chunk => { if (stderr.length < 128 * 1024) stderr += chunk.toString("utf8").slice(0, 128 * 1024 - stderr.length); });
    child.on("error", value => { spawnError = value.message; error = [spawnError, stdinError].filter(Boolean).join("; "); });
    child.once("close", finish);
    child.stdin.on("error", value => { stdinError = `stdin: ${value.message}`; error = [spawnError, stdinError].filter(Boolean).join("; "); });
    if (input !== undefined) {
      try { child.stdin.end(input); } catch (cause) {
        stdinError = `stdin: ${cause.message}`;
        error = [spawnError, stdinError].filter(Boolean).join("; ");
        try { child.stdin.destroy(); } catch { /* the real close receipt is still required */ }
      }
    } else child.stdin.end();
  });
}

/** systemctl is a service-manager double, never a Link executable double. */
export async function writeSystemctlDouble(file, receipt) {
  const script = `#!/bin/sh
set -eu
printf '%s\\n' "$*" >> ${JSON.stringify(receipt)}
printf '%s\\n' ${JSON.stringify(SERVICE_SENTINEL)} >&2
exit 42
`;
  await writeFile(file, script, { mode: 0o700 });
}

export async function readServiceCalls(file) {
  if (!await exists(file)) return [];
  return (await readFile(file, "utf8")).split("\n").filter(Boolean);
}

/** Every expected status must have reached the double; a missing receipt never proves interception. */
export function serviceVerdict(calls) {
  const expected = 4;
  const onlyRead = calls.every(call => call === "--user show-environment");
  return {
    ok: calls.length === expected && onlyRead,
    calls,
    expectedCalls: expected,
    mutationAttempts: calls.filter(call => call !== "--user show-environment"),
    boundary: "PATH-selected read-only systemctl double; not an OS sandbox or proof against direct host reads",
  };
}
