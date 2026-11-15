// Tags a permission request by whether the action leaves the local machine: a push or other remote write, a deploy or
// publish, or a message/external action sent as the user. The structured tool input the permission card already shows
// is the whole input; shell commands are normalized past common obfuscation (split or quoted command names, $IFS)
// before the rules in ./outbound-actions.json run. Anything that cannot be confidently read as local-only errs toward
// a warning rather than a silent single Allow.
import rules from "./outbound-actions.json";

/** What makes the request outward-facing; "unknown" is the fail-toward-warning bucket for input we cannot parse. */
export type OutboundKind = "push" | "deploy" | "send" | "unknown";

export interface OutboundRisk {
  kind: OutboundKind;
  /** The rule that matched, when a rule did; the "unknown" bucket carries none. */
  ruleId?: string;
}

interface Rule {
  id: string;
  kind: OutboundKind;
  re: RegExp;
}

const RULES: Rule[] = rules.actions.map((a) => ({ id: a.id, kind: a.kind as OutboundKind, re: new RegExp(a.pattern, "u") }));

/** Tools whose input is a shell command line. */
const SHELL_TOOLS = new Set(["Bash", "exec_command", "shell"]);
/** Tools that act on local files or the local workspace, so a call of them is local-only when no rule matches. */
const LOCAL_TOOLS = new Set([...SHELL_TOOLS, "Read", "Edit", "MultiEdit", "Write", "NotebookEdit", "Grep", "Glob"]);
/** Command names that only wrap the command that follows. */
const WRAPPERS = new Set(["sudo", "env", "time", "nice", "nohup", "command", "xargs", "stdbuf", "runuser"]);
/** Shell interpreters; only `sh -c <line>` carries the command in a later word. */
const SHELLS = new Set(["sh", "bash", "zsh", "dash", "fish"]);
/** Read-only inspection binaries that stay local even when invoked through a path. */
const LOCAL_READONLY_BINARIES = new Set([
  "ls", "cat", "head", "tail", "less", "more", "grep", "egrep", "fgrep", "rg",
  "find", "wc", "echo", "printf", "pwd", "printenv", "which", "whoami", "id",
  "uname", "stat", "file", "basename", "dirname", "realpath", "sort", "uniq",
  "cut", "diff", "jq", "tree", "du", "df", "ps",
]);
const MAX_DEPTH = 8;

/** A path reference or *.sh script: the parser cannot see what the file does. */
function isScriptReference(word: string): boolean {
  return word.includes("/") || word.endsWith(".sh");
}

/**
 * The words the shell would run once line continuations, $IFS-style word breaks, backslashes and quotes are gone:
 * g""it, g\it and git$IFS all read as git.
 */
function unquote(text: string): string {
  return text
    .replace(/\\\r?\n/gu, "")
    .replace(/\$\{IFS\}|\$IFS(?![A-Za-z0-9_])|\$\{?[@*#]\}?/gu, " ")
    .replace(/\\(.)/gu, "$1")
    .replace(/["']/gu, "");
}

/** Splits one command line into the segments a shell would run separately, without crossing quotes. */
function splitSegments(line: string): string[] {
  const out: string[] = [];
  let cur = "";
  let quote: string | null = null;
  for (let i = 0; i < line.length; i++) {
    const ch = line[i]!;
    if (quote) {
      cur += ch;
      if (ch === quote) quote = null;
      continue;
    }
    if (ch === '"' || ch === "'") {
      quote = ch;
      cur += ch;
      continue;
    }
    // && and || are one operator and end the segment exactly like ;
    if ((ch === "&" || ch === "|") && line[i + 1] === ch) {
      if (cur.trim()) out.push(cur);
      cur = "";
      i++;
      continue;
    }
    // subshell/grouping parens only delimit words, they never name a command
    if (ch === "\n" || ch === ";" || ch === "&" || ch === "|" || ch === "(" || ch === ")") {
      if (cur.trim()) out.push(cur);
      cur = "";
      continue;
    }
    cur += ch;
  }
  if (cur.trim()) out.push(cur);
  return out;
}

/** Splits one segment into words on whitespace, keeping the quotes so a later unquote sees the original shape. */
function tokenize(line: string): string[] {
  const out: string[] = [];
  let cur = "";
  let quote: string | null = null;
  for (const ch of line) {
    if (quote) {
      cur += ch;
      if (ch === quote) quote = null;
      continue;
    }
    if (ch === '"' || ch === "'") {
      quote = ch;
      cur += ch;
      continue;
    }
    if (/\s/u.test(ch) || ch === "(" || ch === ")") {
      if (cur) {
        out.push(cur);
        cur = "";
      }
      continue;
    }
    cur += ch;
  }
  if (cur) out.push(cur);
  return out;
}

/** A download piped straight into a shell: the words that run arrive over the network, so the line is not parseable. */
const REMOTE_SHELL = /(?:curl|wget)\b[^|;&\n]*\|\s*(?:\S+\s+)*(?:sudo\s+)?(?:sh|bash|zsh|dash)\b/u;

/** The static command name a segment starts from, or null when the name is built at runtime and cannot be read. */
function segmentName(segment: string, depth = 0): string | null {
  if (depth > MAX_DEPTH) return null;
  let words = tokenize(segment);
  // leading FOO=bar assignments do not name the command
  while (words.length && /^[A-Za-z_][A-Za-z0-9_]*=/u.test(unquote(words[0]!))) words = words.slice(1);
  if (!words.length) return null;
  const first = unquote(words[0]!);
  if (!first || first.startsWith("$") || first.startsWith("`") || /[\x00-\x1f]/u.test(first)) return null;
  const base = first.split("/").pop()!;
  if (SHELLS.has(base)) {
    const at = words.findIndex((w) => unquote(w) === "-c");
    const inner = at >= 0 ? words[at + 1] : undefined;
    if (!inner) {
      // `sh deploy.sh` runs a file whose contents the parser cannot read
      return words.slice(1).some((w) => isScriptReference(unquote(w))) ? null : base;
    }
    const names = splitSegments(unquote(inner)).map((s) => segmentName(s, depth + 1));
    return names.length && names.every((n) => n !== null) ? names[0] : null;
  }
  if (base === "eval" || base === "exec" || base === "source" || base === ".") {
    const arg = words[1] ? unquote(words[1]) : "";
    if (!arg || arg.startsWith("$") || arg.startsWith("`")) return null;
    return segmentName(arg, depth + 1);
  }
  if (WRAPPERS.has(base)) return segmentName(words.slice(1).join(" "), depth + 1);
  // An executable path or *.sh script may do anything; only known read-only binaries stay local.
  if (isScriptReference(first) && !LOCAL_READONLY_BINARIES.has(base)) return null;
  return base;
}

/** Whether a command line has a part whose command name cannot be confidently read. */
function looksDynamic(command: string): boolean {
  if (REMOTE_SHELL.test(unquote(command))) return true;
  const segments = splitSegments(command);
  if (!segments.length) return true;
  return segments.some((s) => segmentName(s) === null);
}

/** The command strings a tool's structured input carries, in the shape the permission card shows them. */
function commandTexts(tool: string, input: Record<string, unknown>): string[] {
  if (!SHELL_TOOLS.has(tool)) return [];
  const raw = input.command ?? input.cmd;
  if (typeof raw === "string") return raw.trim() ? [raw] : [];
  if (Array.isArray(raw)) return raw.map((a) => String(a)).filter((s) => s.trim() !== "");
  return [];
}

/**
 * The outward-facing category an approval would let through, or null for a confidently local-only request. A rule
 * match wins wherever it sits in the input; otherwise a shell line whose command name is built at runtime (a
 * substitution, an eval of dynamic text, a download piped into a shell) and any tool outside the local set warn as
 * "unknown", so an unparseable request never takes the quiet single-Allow path.
 */
export function outboundAction(tool: string | undefined, input: Record<string, unknown> | undefined): OutboundRisk | null {
  const name = tool ?? "";
  const inp = input ?? {};
  const commands = commandTexts(name, inp);
  const haystack = [name, ...commands].join("\n");
  if (haystack.trim()) {
    const plain = unquote(haystack);
    for (const rule of RULES) {
      if (rule.re.test(haystack) || rule.re.test(plain)) return { kind: rule.kind, ruleId: rule.id };
    }
  }
  if (SHELL_TOOLS.has(name)) {
    // nothing readable to prove the call stays local: the warning bucket, not a quiet Allow
    if (!commands.length) return { kind: "unknown" };
    return commands.some(looksDynamic) ? { kind: "unknown" } : null;
  }
  return LOCAL_TOOLS.has(name) ? null : { kind: "unknown" };
}
