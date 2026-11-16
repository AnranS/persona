import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdir, readFile, rename, stat, writeFile } from "node:fs/promises";
import { homedir, platform } from "node:os";
import { dirname, posix, relative, resolve, sep, win32 } from "node:path";

export const PAIR_INPUT = "persona://link?";
export const SERVICE_SENTINEL = "real Link smoke: systemctl access is unavailable";

function windowsComparable(value) {
  const text = String(value).replace(/^\\\\[?.]\\(?:UNC\\)?/i, match => match.toLowerCase().startsWith("\\\\?\\unc\\") ? "//" : "").replace(/^\/\/[?.]\/(?:UNC\/)?/i, match => match.toLowerCase().startsWith("//?/unc/") ? "//" : "");
  return text;
}

function windowsComparablePath(value) {
  return windowsComparable(value).replaceAll("\\", "/");
}

function trimWindowsTrailing(value) {
  return /^[A-Za-z]:[\\/]?$/.test(value) ? value.slice(0, 2).toLowerCase() + "\\" : value.replace(/[\\/]+$/, "");
}

function trimPosixTrailing(value) {
  return value.replace(/\/+$/, "") || "/";
}

/**
 * Path redaction safety contract: only descendants of an explicitly attributed absolute root survive. Windows drives,
 * UNC, namespace and POSIX roots are compared in their own family; unresolved or ambiguous expressions become `<path>`.
 * Lexical identity only: this never authorizes recursive deletion through symlinks.
 */
export function classifyOwnedPath(run, candidate) {
  if (typeof run !== "string" || typeof candidate !== "string") return "outside";
  const embedded = candidate.slice(1).includes("//?/") || candidate.slice(1).includes("//./");
  if (embedded) return "outside";
  const windows = value => /^(?:[A-Za-z]:[\\/]|\\\\|\/\/[?.]\/)/.test(value) && !/^\/\/[?.]\/UNC\//i.test(value);
  const unc = value => /^\/\/[^/]+[\\/][^/]+[\\/]/.test(value) && !/^\/\/[?.]\//.test(value);
  const uncNamespace = value => /^\/\/[?.]\/UNC\//i.test(value);
  if (windows(run) !== windows(candidate) || (unc(run) !== unc(candidate) && !(uncNamespace(run) && unc(candidate)) && !(unc(run) && uncNamespace(candidate)))) return "outside";
  if (windows(run) || windows(candidate) || unc(run) || unc(candidate) || uncNamespace(run) || uncNamespace(candidate)) {
    const rootPath = trimWindowsTrailing(windowsComparable(run)).toLowerCase();
    const path = trimWindowsTrailing(win32.resolve(windowsComparable(candidate))).toLowerCase();
    const descendant = win32.relative(rootPath, path);
    if (descendant === "") return "root";
    if (descendant === ".." || descendant.startsWith(`..${win32.sep}`) || win32.isAbsolute(descendant)) return "outside";
    return "descendant";
  }
  if (!run.startsWith(sep) || !candidate.startsWith(sep)) return "outside";
  if (candidate.includes(";") || candidate.split("/").includes("..")) return "outside";
  const root = trimPosixTrailing(resolve(run));
  const normalized = trimPosixTrailing(resolve(candidate));
  if (root === normalized) return "root";
  const descendant = relative(root, normalized);
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
      const root = trimWindowsTrailing(windowsComparable(run));
      const normalized = trimWindowsTrailing(windowsComparable(path));
      const rest = windowsComparablePath(normalized).slice(root.length).replace(/^\/+/, "");
      replacement = `<run>/${rest}`;
    }
    return { path, replacement };
  });
}

function ownedReplacement(run, candidate) {
  if (typeof candidate !== "string" || candidate.includes(";") || /(^|[\\/])\.\.([\\/]|$)/.test(candidate)) return undefined;
  if (/[A-Za-z]:(?![\\/])/.test(candidate)) return undefined;
  const kind = classifyOwnedPath(run, candidate);
  if (kind === "root") return "<run>";
  if (kind !== "descendant") return undefined;
  if (/^[A-Za-z]:[\\/]|^\\\\|^\/{2}/.test(candidate)) {
    const rootPath = trimWindowsTrailing(windowsComparable(run)).toLowerCase();
    const path = trimWindowsTrailing(win32.resolve(windowsComparable(candidate)));
    return `<run>/${windowsComparablePath(path).slice(rootPath.length).replace(/^\/+/, "")}`;
  }
  return `<run>/${relative(trimPosixTrailing(resolve(run)), trimPosixTrailing(resolve(candidate)))}`;
}

/**
 * Replace exactly one path token. `text` starts at the token's first character.
 * Returns `{ replacement, consumed }` or `undefined` when no path starts here.
 */
function redactOnePath(text, run) {
  const windowsRoot = /^(?:[A-Za-z]:[\\/]|\\\\|\/\/)/.test(run);
  const windowsToken = /^(?:\\\\[?.]\\|\/\/[?.]\/|[A-Za-z]:[\\/]|[A-Za-z]:[^/\\\s]+\\|\\\\[^\\]+[\\/]|\/\/[^/]+[\\/])[^\r\n"'`]*/.exec(text);
  const posixToken = !windowsToken && /^\/(?:[^\s"'`;]+\/?)+/.test(text) ? /^\/[^\r\n"'`]*/.exec(text) : undefined;
  if (/[A-Za-z]:[^\\/\s]+\\/.test(text.slice(1)) || /[A-Za-z]:[^\s\\/]+\\/.test(text)) {
    if (process.env.PERSONA_REDACT_DEBUG === "1") console.error(JSON.stringify({ input: text, relativeDrive: true, consumed: text.length }));
    return { replacement: "<path>", consumed: text.length };
  }
  const token = windowsToken ?? posixToken;
  if (!token) return undefined;
  const windows = windowsToken !== null;
  const embedded = windows ? token[0].slice(1).search(/\/\/[?.]\/|\/\/[^/]+\/[^/]+\//) : -1;
  const first = embedded >= 1 ? token[0].slice(0, embedded) : token[0];
  const structural = first.search(/[\r\n"'`\[\](){}<>]/);
  const pathText = structural >= 0 ? first.slice(0, structural) : first;
  const trailing = trimTrailing(pathText).replace(/[,:]+$/, "");
  const core = trailing.replace(/[\\/]+$/, "") || trailing;
  if (!core) return { replacement: first, consumed: first.length };

  // Try the full path field first, then walk back to component boundaries. This preserves spaces in real paths while
  // allowing a following diagnostic or an independently scanned absolute path to survive.
  if (windowsToken === null && token[0].includes(";")) {
    const separator = token[0].search(/;\s/);
    if (separator < 0) {
      const consumed = token[0].search(/\s+(?:[A-Za-z][\w.-]*=|(?:failed|error|cannot|denied|unexpected|fatal|exception)\b)/i);
      const end = consumed < 0 ? token[0].length : consumed;
      if (process.env.PERSONA_REDACT_DEBUG === "1") console.error(JSON.stringify({ input: text, token: token[0], consumed: end, remainder: token[0].slice(end) }));
      return { replacement: "<path>", consumed: Math.max(1, end) };
    }
    token[0] = token[0].slice(0, separator);
  }
  const nextAbsolute = /[\s,;][\w.-]*=[\[(]?(?=(?:[A-Za-z]:[\\/]|\\\\|\/\/|\/))|[\s,;]\[?(?=(?:[A-Za-z]:[\\/]|\\\\|\/\/|\/))/.exec(first);
  const candidates = [first, ...Array.from(first.matchAll(/[\s,;:<>{}\[\]()]/g), match => first.slice(0, match.index)).reverse()];
  for (const candidate of candidates) {
    if (nextAbsolute && candidate.length > nextAbsolute.index) continue;
    if (structural >= 0 && candidate.length > structural) continue;
    if (structural >= 0 && candidate.length === first.length && first[structural] === "<") continue;
    const trimmed = candidate.replace(/[,:]+$/, "");
    const candidateEnd = candidate.length;
    const replacement = ownedReplacement(run, trimmed) ?? ownedReplacement(run, candidate);
    if (replacement) {
      if (process.env.PERSONA_REDACT_DEBUG === "1") console.error(JSON.stringify({ input: text, candidate, replacement, consumed: candidate.length }));
      const trailing = candidate.slice(trimmed.length);
      const consumed = candidateEnd;
      if (windowsToken === null && first[candidate.length] === ";" && !/^\s/.test(first[candidate.length + 1] ?? "")) return { replacement: "<path>", consumed: first.length };
      return { replacement: replacement + trailing, consumed };
    }
  }
  const field = /(?:^|\s)[A-Za-z][\w.-]*=/.exec(first);
  const phrase = /\s+(?:failed|error|cannot|denied|unexpected|fatal|exception)\b/i.exec(first);
  const boundary = Math.min(field?.index ?? first.length, phrase?.index ?? first.length);
  const nextPath = /\s+(?=(?:[A-Za-z]:[\\/]|\\\\|\/\/|\/))/.exec(first);
  const consumed = Math.min(boundary, nextPath?.index ?? first.length);
  const bracket = [")", "]", "}", ">"].includes(first[consumed - 1]);
  if (process.env.PERSONA_REDACT_DEBUG === "1") console.error(JSON.stringify({ input: text, token: token[0], first, boundary, nextPath: nextPath?.index, consumed }));
  return { replacement: "<path>", consumed: Math.max(1, consumed - (bracket ? 1 : 0)) };
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
      const group = await observeOwnedGroup(child, closeGrace);
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
