// Tags a permission request by whether the action leaves the local machine: a push or other remote write, a deploy or
// publish, or a message/external action sent as the user. The structured tool input the permission card already shows
// is the whole input; shell commands are normalized past common obfuscation (split or quoted command names, $IFS)
// before the rules in ./outbound-actions.json run.
//
// Fail-toward-warning is the default, not the exception: a shell line is confidently local (null) only when EVERY
// segment's command resolves to a name in the known-local set, or to a network-capable, multi-mode tool whose argv a
// policy proves read-only/local. A readable but unrecognized command name (ssh, aws, a custom binary…) is "unknown",
// so a new outward CLI can never take the quiet one-tap Allow path just because its name parses. Explicit rules still
// win first to assign the precise push/deploy/send kind to shapes they understand.
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
/** Non-shell tools that only touch local files / the local workspace. */
const LOCAL_TOOLS = new Set([...SHELL_TOOLS, "Read", "Edit", "MultiEdit", "Write", "NotebookEdit", "Grep", "Glob"]);
/** Command names that only wrap the command that follows. */
const WRAPPERS = new Set(["sudo", "env", "time", "nice", "nohup", "command", "xargs", "stdbuf", "runuser"]);
/** Wrapper options that take a separate value argument, so the value is not mistaken for the next command. */
const WRAPPER_OPTS_WITH_ARG = new Set([
  "-u", "--user", "-g", "--group", "-C", "--chdir", "-n", "--adjustment", "-o", "--output", "-I", "--replace",
  "-r", "--role", "-s", "--shell", "-c", "--command", "-w", "--supplement-groups", "-S", "--split-string",
  "-d", "--delimiter", "-E", "--max-args", "-L", "--max-lines", "-P", "--max-procs", "-a", "--arg-file",
  "-i", "--input", "-e", "--error", "--process-slot-var",
]);
/** Wrapper options that stand alone; an option in neither wrapper set fails closed instead of being consumed. */
const WRAPPER_BARE_OPTS = new Set([
  "-i", "-0", "-p", "-v", "-V", "-E", "-H", "-h", "-K", "-k", "-l", "-n", "-P", "-S", "-b", "-t", "-r", "-x",
  "--null", "--ignore-environment", "--preserve-environment", "--login", "--set-home", "--non-interactive",
  "--stdin", "--background", "--bell", "--reset-timestamp", "--remove-timestamp", "--preserve-groups",
  "--close-from", "--help", "--version", "--debug", "--verbose", "--portability", "--append", "--posix",
  "--no-run-if-empty", "--open-tty", "--interactive",
]);
/** Shell interpreters; only a `-c`/`-lc` form carries the command in a later word. */
const SHELLS = new Set(["sh", "bash", "zsh", "dash", "fish"]);
/**
 * Shell reserved words. A condition keyword (`if`, `while`, `until`, `then`, `do`…) introduces the command that
 * follows, so the rest of the segment is classified recursively; pure header/closer words carry no command of their
 * own. The body after `;`/newline is a separate segment and goes through the same allowlist on its own.
 */
const SHELL_KEYWORD_RECURSE = new Set(["if", "then", "else", "elif", "while", "until", "do", "!"]);
const SHELL_KEYWORD_LOCAL = new Set(["fi", "done", "esac", "{", "}", "function"]);
/** Loop/case headers name a variable or a word, not a command; their bodies arrive in later `do`/`;;` segments. */
const SHELL_KEYWORD_HEADER = new Set(["for", "foreach", "case", "select", "in"]);
/** Language runtimes that can execute code passed inline; the code text is not a static command the parser can read. */
const INTERPRETERS = new Set(["python", "python2", "python3", "node", "nodejs", "deno", "bun", "ruby", "perl", "php", "lua", "Rscript", "r"]);
/** The flag each runtime uses to take the code to run. */
const INLINE_CODE_FLAGS = new Set(["-c", "-e", "-r"]);
const MAX_DEPTH = 8;

/**
 * Basenames that cannot leave the machine on their own: read-only inspection, local filesystem mutation, process
 * control, build/test/format task runners, archives and shell builtins. Multi-mode, network-capable tools are NOT here;
 * they go through TOOL_POLICIES.
 */
const KNOWN_LOCAL_BINARIES = new Set([
  // read-only inspection
  "ls", "cat", "head", "tail", "less", "more", "grep", "egrep", "fgrep", "rg", "find", "wc", "echo", "printf", "pwd",
  "printenv", "which", "whoami", "id", "uname", "stat", "file", "basename", "dirname", "realpath", "sort", "uniq",
  "cut", "diff", "jq", "tree", "du", "df", "ps", "date", "tty", "seq", "tr", "tac", "rev", "nl", "paste", "join",
  "comm", "fold", "col", "column", "od", "hexdump", "xxd", "strings", "md5sum", "sha1sum", "sha256sum", "sha512sum",
  "shasum", "sum", "cksum", "base32", "base64", "pathchk", "readlink", "logname", "groups", "uptime", "free", "vmstat",
  "iostat", "lscpu", "lspci", "lsusb", "lsof", "fuser", "dmesg", "journalctl", "top", "htop", "pgrep", "strace",
  "ltrace", "hostname", "sleep", "yes", "expr", "getopt", "logger",
  // local filesystem mutation and archives
  "rm", "rmdir", "cp", "mv", "mkdir", "touch", "ln", "chmod", "chown", "chgrp", "umask", "truncate", "unlink", "link",
  "mktemp", "install", "tee", "split", "csplit", "dd", "flock", "tar", "zip", "unzip", "gzip", "gunzip", "bzip2",
  "bunzip2", "xz", "unxz", "zstd", "unzstd", "cpio", "ar", "patch",
  // local process control
  "kill", "killall", "pkill", "renice", "timeout", "watch", "jobs", "bg", "fg", "wait",
  // build / compile / task runners (no network subcommand of their own)
  "make", "cmake", "ninja", "gcc", "g++", "cc", "c++", "clang", "clang++", "ld", "as", "javac", "tsc", "esbuild",
  "webpack", "rollup", "vite", "parcel", "gulp", "grunt", "just", "rake", "ant", "shfmt",
  // test, format and lint runners
  "pytest", "py.test", "tox", "nox", "vitest", "jest", "mocha", "jasmine", "ava", "ctest", "bats", "pre-commit",
  "prettier", "black", "ruff", "rustfmt", "gofmt", "gofumpt", "clang-format", "stylua", "eslint", "tslint",
  "stylelint", "biome", "golangci-lint", "flake8", "mypy", "pyright",
  // VCS and ops tools whose outward verbs are caught by rules, everything else being local
  "hg", "jj", "svn", "bzr", "ansible-vault", "ansible-galaxy", "ansible-doc", "ansible-inventory",
  // local desktop helpers
  "open", "xdg-open", "pbcopy", "pbpaste",
  // shell builtins that show up as segment words
  "cd", "export", "unset", "alias", "unalias", "set", "return", "exit", "trap", "read", "mapfile", "readarray",
  "test", "[", "true", "false", "pushd", "popd", "dirs", "history", "type", "hash", "builtin", "caller", "let",
  "declare", "local", "typeset", "shift", "getopts", "ulimit", "times", "compgen", "complete", "source",
]);

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
    if (ch === "\n" || ch === ";" || ch === "|" || ch === "(" || ch === ")") {
      if (cur.trim()) out.push(cur);
      cur = "";
      continue;
    }
    if (ch === "&") {
      // fd duplication (2>&1, 2<&1) and an &>-style redirect keep the & as part of the operator, not a delimiter
      const prev = line[i - 1];
      const next = line[i + 1];
      if (prev === ">" || prev === "<" || next === ">" || next === "<") {
        cur += ch;
        continue;
      }
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
/** A redirection token, possibly carrying its target or fd (`>f`, `2>&1`, `&>>`, `<<<`, `>|`). */
const REDIRECTION = /^(?:\d+)?(?:[<>]{1,3}|&[<>])[>&|-]?\d*$|^(?:\d+)?[<>]\S*$|^&$/u;
/** A redirection operator without a glued file target, so the word that follows is the target to drop. */
const REDIRECTION_BARE = /^(?:\d+)?(?:[<>]{1,3}|&[<>])[>&|-]?\d*$/u;

/** Drops leading VAR=value assignments and shell redirection tokens (with their targets), returning the command words. */
function commandWords(segment: string): string[] {
  const raw = tokenize(segment).map(unquote).filter(Boolean);
  const out: string[] = [];
  for (let i = 0; i < raw.length; i++) {
    const w = raw[i]!;
    if (REDIRECTION.test(w)) {
      // A bare operator ("> f", "2> err", "<< EOF") consumes the next word; a glued target (">f", "2>&1") does not.
      if (w !== "&" && REDIRECTION_BARE.test(w) && !/[>&]\d+$/u.test(w)) i++;
      continue;
    }
    out.push(w);
  }
  while (out.length && /^[A-Za-z_][A-Za-z0-9_]*=/u.test(out[0]!)) out.shift();
  return out;
}

/** The first non-flag word after the binary, skipping flags that take a separate value; `--` ends flag scanning. */
function subcommand(words: string[], flagsWithValue: ReadonlySet<string>): string | undefined {
  for (let i = 1; i < words.length; i++) {
    const w = words[i]!;
    if (w === "--") return words[i + 1];
    if (!w.startsWith("-")) return w;
    if (w.includes("=")) continue; // --flag=value carries its own value
    if (flagsWithValue.has(w)) i++; // skip the value argument
  }
  return undefined;
}

/** The word right after the subcommand, for tools whose local forms depend on it (`git remote list`, `helm repo list`). */
function secondWord(words: string[], flagsWithValue: ReadonlySet<string>): string | undefined {
  const sub = subcommand(words, flagsWithValue);
  if (sub === undefined) return undefined;
  const at = words.indexOf(sub);
  return words[at + 1];
}

const GIT_FLAGS_WITH_VALUE = new Set(["-C", "-c", "--git-dir", "--work-tree", "--namespace", "--super-prefix", "--config-env", "--attr-source", "-f", "--file"]);
const GIT_LOCAL = new Set(["status", "log", "show", "diff", "diff-tree", "commit", "add", "rm", "mv", "restore", "stash",
  "checkout", "switch", "branch", "merge", "rebase", "cherry-pick", "revert", "reset", "reflog", "tag", "config", "grep",
  "ls-files", "ls-tree", "cat-file", "rev-parse", "describe", "bisect", "worktree", "sparse", "init", "blame",
  "shortlog", "fsck", "gc", "prune", "notes", "replace", "maintenance", "count-objects", "name-rev", "var",
  "verify-pack", "whatchanged", "apply", "help", "version", "archive", "fetch", "pull", "remote", "submodule"]);
const GIT_REMOTE_LOCAL = new Set(["list", "-v", "--verbose", "get-url", "show-url"]);
const GIT_SUBMODULE_LOCAL = new Set(["status", "init", "summary", "foreach"]);

const PACKAGE_LOCAL = new Set(["test", "run", "run-script", "exec", "install", "i", "add", "ci", "up", "build", "start",
  "ls", "config", "get", "outdated", "audit", "view", "info", "root", "bin", "prefix", "rebuild", "doctor", "clean",
  "pack", "help", "version", "uninstall", "remove", "update", "link", "prune", "dedupe", "dedup", "why"]);
const GO_LOCAL = new Set(["build", "test", "vet", "fmt", "mod", "run", "env", "version", "list", "doc", "clean", "work", "fix"]);
const GO_MOD_LOCAL = new Set(["init", "edit", "graph", "why", "vendor", "verify"]);
const GO_FLAGS_WITH_VALUE = new Set(["-C", "-tags", "-p", "-count", "-run", "-bench", "-coverpkg", "-o", "-modfile", "-ldflags"]);
const KUBECTL_LOCAL = new Set(["get", "describe", "logs", "log", "top", "explain", "version", "config", "cluster-info",
  "api-resources", "api-versions", "auth", "options", "completion", "kustomize", "diff", "events", "wait", "plugin"]);
const KUBECTL_FLAGS_WITH_VALUE = new Set(["-n", "--namespace", "--context", "-f", "--filename", "-k", "--kubeconfig", "-o", "--output"]);
const KUBECTL_AUTH_LOCAL = new Set(["can-i", "whoami"]);
const TERRAFORM_LOCAL = new Set(["init", "validate", "fmt", "plan", "show", "console", "output", "graph", "providers",
  "version", "workspace", "get", "metadata", "clean"]);
const TERRAFORM_STATE_LOCAL = new Set(["list", "pull", "show"]);
const TERRAFORM_WORKSPACE_LOCAL = new Set(["list", "show", "select"]);
const HELM_LOCAL = new Set(["list", "status", "show", "history", "get", "template", "lint", "version", "env",
  "completion", "plugin", "repo", "dependency", "pull", "search"]);
const HELM_REPO_LOCAL = new Set(["list"]);
const HELM_DEPENDENCY_LOCAL = new Set(["list"]);
const HELM_PLUGIN_LOCAL = new Set(["list", "version"]);
const HOSTED_READ = new Set(["list", "view", "diff", "checks", "status", "watch", "search", "pr", "issue", "repo",
  "run", "workflow", "release", "gist", "labels", "label", "extension", "api", "help", "version", "mr", "pipeline",
  "ci", "snippet", "calendar"]);
const HOSTED_AUTH_LOCAL = new Set(["status"]);
const HOSTED_CONFIG_LOCAL = new Set(["get", "list"]);
const HOSTED_LIST_ONLY = new Set(["labels", "label", "extension", "secret", "variable", "project", "cache", "ssh-key", "gpg-key"]);
const DOCKER_LOCAL = new Set(["build", "images", "image", "ps", "logs", "log", "inspect", "info", "version", "stop",
  "start", "restart", "kill", "rm", "rmi", "tag", "save", "load", "cp", "stats", "top", "diff", "port", "wait",
  "attach", "network", "volume", "config", "context", "events", "system", "container", "node", "buildx", "compose",
  "history", "commit", "pause", "unpause", "rename", "export", "import"]);
const DOCKER_FLAGS_WITH_VALUE = new Set(["-H", "--host", "-c", "--context", "-l", "--log-level", "--config", "-f", "--file", "-p", "--project-name"]);
const CARGO_LOCAL = new Set(["build", "check", "test", "fmt", "clippy", "run", "doc", "tree", "metadata",
  "verify-project", "loc", "clean", "init", "new", "bench", "package"]);
const PIP_LOCAL = new Set(["freeze", "list", "show", "check", "debug", "verify", "completion"]);
const POETRY_LOCAL = new Set(["build", "check", "run", "version", "env", "info"]);
const GEM_LOCAL = new Set(["build", "contents", "dependency", "list", "environment"]);
const TWINE_LOCAL = new Set(["check"]);
const JVM_BUILD_LOCAL = new Set(["build", "test", "check", "clean", "assemble", "package", "compile", "javadoc",
  "dependencies", "tasks", "properties", "verify", "dependency:tree"]);
const PACKAGE_MANAGER_LOCAL = new Set(["list", "show", "search", "info", "version", "services", "config"]);
const WRANGLER_LOCAL = new Set(["dev", "build", "whoami", "ls", "tail", "logs"]);
const FLY_LOCAL = new Set(["status", "list", "info", "logs", "version", "doctor"]);
const VERCEL_LOCAL = new Set(["ls", "inspect", "pull", "whoami", "logs", "build", "dev", "link"]);
const PULUMI_LOCAL = new Set(["preview", "config", "version", "stack", "cancel"]);
const SAFE_HTTP_VERBS = new Set(["GET", "HEAD", "OPTIONS"]);
const WRITE_HTTP_VERBS = new Set(["POST", "PUT", "PATCH", "DELETE"]);

/** True when an HTTP client's argv carries an explicit write verb, a body/form/upload flag, or an unreadable config. */
function httpWrites(words: string[], base: string): boolean {
  const line = words.join(" ");
  // A config/input file can carry verbs or bodies the command line does not show.
  if (base === "curl" && /(?:^|\s)(?:-K\b|--config(?:=|\s))/u.test(line)) return true;
  if (base === "wget" && /(?:^|\s)(?:-i\b|--input-file(?:=|\s)|--config(?:=|\s)|-e\b|--execute(?:=|\s))/u.test(line)) return true;
  for (let i = 0; i < words.length; i++) {
    const w = words[i]!;
    let m: RegExpMatchArray | null;
    if ((m = w.match(/^--(?:request|method)(?:=(.*))?$/u))) {
      const verb = m[1] ?? words[i + 1] ?? "";
      if (m[1] === undefined) i++;
      if (!SAFE_HTTP_VERBS.has(verb.toUpperCase())) return true;
      continue;
    }
    if ((m = w.match(/^-X(.*)$/u))) {
      const verb = m[1] || words[i + 1] || "";
      if (!m[1]) i++;
      if (!SAFE_HTTP_VERBS.has(verb.toUpperCase())) return true;
      continue;
    }
    // glued short body flags are curl's spelling only: wget's -d/-T mean --debug/--timeout, not data/upload
    if (base === "curl" && /^-[A-Za-z]*[dFT](?![A-Za-z])/u.test(w)) return true;
    if (/^--(?:data|form|upload-file|json|post-data|post-file|body-data|body-file)/u.test(w)) return true;
  }
  if (base === "http" || base === "httpie" || base === "xh") {
    const first = words.slice(1).find((w) => !w.startsWith("-"));
    if (first && WRITE_HTTP_VERBS.has(first.toUpperCase())) return true;
  }
  return false;
}

/** Per-tool predicates for network-capable, multi-mode commands: true only for the read-only/local forms. */
const TOOL_POLICIES: Record<string, (words: string[]) => boolean> = {
  git: (w) => {
    const sub = subcommand(w, GIT_FLAGS_WITH_VALUE);
    if (sub === undefined || !GIT_LOCAL.has(sub)) return false;
    if (sub === "remote") {
      const next = secondWord(w, GIT_FLAGS_WITH_VALUE);
      return next === undefined || GIT_REMOTE_LOCAL.has(next);
    }
    if (sub === "submodule") {
      const next = secondWord(w, GIT_FLAGS_WITH_VALUE);
      return next === undefined || GIT_SUBMODULE_LOCAL.has(next);
    }
    return true;
  },
  npm: (w) => PACKAGE_LOCAL.has(subcommand(w, new Set()) ?? ""),
  pnpm: (w) => PACKAGE_LOCAL.has(subcommand(w, new Set()) ?? ""),
  yarn: (w) => PACKAGE_LOCAL.has(subcommand(w, new Set()) ?? ""),
  bun: (w) => PACKAGE_LOCAL.has(subcommand(w, new Set()) ?? ""),
  go: (w) => {
    const sub = subcommand(w, GO_FLAGS_WITH_VALUE);
    if (sub === undefined || !GO_LOCAL.has(sub)) return false;
    if (sub === "mod") {
      const next = secondWord(w, GO_FLAGS_WITH_VALUE);
      // `go mod tidy/download` fetch and rewrite dependencies; not provably local.
      return next === undefined || GO_MOD_LOCAL.has(next);
    }
    if (sub === "tool") return secondWord(w, GO_FLAGS_WITH_VALUE) !== "install";
    return true;
  },
  swift: (w) => subcommand(w, new Set()) === "test",
  docker: (w) => {
    const sub = subcommand(w, DOCKER_FLAGS_WITH_VALUE);
    return sub !== undefined && DOCKER_LOCAL.has(sub) && !w.includes("--push");
  },
  kubectl: (w) => {
    const sub = subcommand(w, KUBECTL_FLAGS_WITH_VALUE);
    if (sub === undefined || !KUBECTL_LOCAL.has(sub)) return false;
    if (sub === "auth") {
      const next = secondWord(w, KUBECTL_FLAGS_WITH_VALUE);
      return next === undefined || KUBECTL_AUTH_LOCAL.has(next);
    }
    return true;
  },
  terraform: (w) => terraformLocal(w),
  tofu: (w) => terraformLocal(w),
  pulumi: (w) => PULUMI_LOCAL.has(subcommand(w, new Set()) ?? ""),
  helm: (w) => {
    const sub = subcommand(w, new Set(["--namespace", "-n", "--kube-context", "--kubeconfig"]));
    if (sub === undefined || !HELM_LOCAL.has(sub)) return false;
    if (sub === "repo") return HELM_REPO_LOCAL.has(secondWord(w, new Set()) ?? "");
    if (sub === "dependency") return HELM_DEPENDENCY_LOCAL.has(secondWord(w, new Set()) ?? "");
    if (sub === "plugin") return HELM_PLUGIN_LOCAL.has(secondWord(w, new Set()) ?? "");
    return true;
  },
  gh: (w) => hostedLocal(w),
  glab: (w) => hostedLocal(w),
  hub: (w) => hostedLocal(w),
  tea: (w) => hostedLocal(w),
  curl: (w) => !httpWrites(w, "curl"),
  wget: (w) => !httpWrites(w, "wget"),
  http: (w) => !httpWrites(w, "http"),
  httpie: (w) => !httpWrites(w, "httpie"),
  xh: (w) => !httpWrites(w, "xh"),
  cargo: (w) => CARGO_LOCAL.has(subcommand(w, new Set()) ?? ""),
  pip: (w) => PIP_LOCAL.has(subcommand(w, new Set()) ?? ""),
  pip3: (w) => PIP_LOCAL.has(subcommand(w, new Set()) ?? ""),
  uv: (w) => PIP_LOCAL.has(subcommand(w, new Set()) ?? ""),
  poetry: (w) => POETRY_LOCAL.has(subcommand(w, new Set()) ?? ""),
  gem: (w) => GEM_LOCAL.has(subcommand(w, new Set()) ?? ""),
  twine: (w) => TWINE_LOCAL.has(subcommand(w, new Set()) ?? ""),
  mvn: (w) => JVM_BUILD_LOCAL.has(subcommand(w, new Set()) ?? ""),
  gradle: (w) => JVM_BUILD_LOCAL.has(subcommand(w, new Set()) ?? ""),
  apt: (w) => PACKAGE_MANAGER_LOCAL.has(subcommand(w, new Set()) ?? ""),
  "apt-get": (w) => PACKAGE_MANAGER_LOCAL.has(subcommand(w, new Set()) ?? ""),
  dnf: (w) => PACKAGE_MANAGER_LOCAL.has(subcommand(w, new Set()) ?? ""),
  yum: (w) => PACKAGE_MANAGER_LOCAL.has(subcommand(w, new Set()) ?? ""),
  apk: (w) => PACKAGE_MANAGER_LOCAL.has(subcommand(w, new Set()) ?? ""),
  zypper: (w) => PACKAGE_MANAGER_LOCAL.has(subcommand(w, new Set()) ?? ""),
  brew: (w) => PACKAGE_MANAGER_LOCAL.has(subcommand(w, new Set()) ?? ""),
  wrangler: (w) => WRANGLER_LOCAL.has(subcommand(w, new Set()) ?? ""),
  fly: (w) => FLY_LOCAL.has(subcommand(w, new Set()) ?? ""),
  flyctl: (w) => FLY_LOCAL.has(subcommand(w, new Set()) ?? ""),
  vercel: (w) => VERCEL_LOCAL.has(subcommand(w, new Set()) ?? ""),
  // Local service management only; -H/--host hands the command to another machine.
  systemctl: (w) => !w.some((x) => x === "-H" || x === "--host" || x.startsWith("--host=")),
  // A copy is local only with no host specifier: a colon path, :: module or rsync:// URL reaches another machine.
  rsync: (w) => !w.slice(1).some((x) => !x.startsWith("-") && (x.includes(":") || x.startsWith("rsync://"))),
  // A bare local-to-local rclone invocation is fine; any remote:path reference fails closed.
  rclone: (w) => {
    const sub = subcommand(w, new Set());
    if (sub === undefined) return false;
    return !w.slice(1).filter((x) => !x.startsWith("-") && x !== sub).some((p) => p.includes(":"));
  },
};

function terraformLocal(words: string[]): boolean {
  const sub = subcommand(words, new Set());
  if (sub === undefined || !TERRAFORM_LOCAL.has(sub)) return false;
  if (sub === "state") return TERRAFORM_STATE_LOCAL.has(secondWord(words, new Set()) ?? "");
  if (sub === "workspace") {
    const next = secondWord(words, new Set());
    return next === undefined || TERRAFORM_WORKSPACE_LOCAL.has(next);
  }
  return true;
}

/** A hosted dev CLI is local only on a read subcommand, or an `api` call that carries no write verb/field. */
function hostedLocal(words: string[]): boolean {
  const sub = subcommand(words, new Set(["-R", "--repo", "--hostname", "--clobber"]));
  if (sub === undefined || !HOSTED_READ.has(sub)) return false;
  if (sub === "api") {
    return !words.some((x) => /^-(?:X|f|F)\b/.test(x) || x.startsWith("--method") || x.startsWith("--field")
      || x.startsWith("--raw-field") || x.startsWith("--input"));
  }
  if (sub === "auth") return HOSTED_AUTH_LOCAL.has(secondWord(words, new Set(["-R", "--repo"])) ?? "");
  if (sub === "config") return HOSTED_CONFIG_LOCAL.has(secondWord(words, new Set()) ?? "");
  // Groups whose write verbs share the group name: only the explicit list form is local.
  if (HOSTED_LIST_ONLY.has(sub)) return secondWord(words, new Set(["-R", "--repo"])) === "list";
  return true;
}

/** A download piped into a shell, a command built at runtime, or an unreadable name: none can be proven local. */
function segmentVerdict(segment: string, depth = 0): "local" | "unknown" {
  if (depth > MAX_DEPTH) return "unknown";
  const words = commandWords(segment);
  if (!words.length) return "local"; // redirection/assignment-only segment is a no-op
  const first = words[0]!;
  if (first.startsWith("$") || first.startsWith("`") || /[\x00-\x1f]/u.test(first)) return "unknown";
  const base = first.split("/").pop()!;

  if (SHELLS.has(base)) {
    // `sh -lc 'line'`: split the inline line and require every inner segment to be local.
    const flagAt = words.findIndex((w) => /^-[A-Za-z]*c/.test(w));
    const inner = flagAt >= 0 ? words[flagAt + 1] : undefined;
    if (inner) return splitSegments(inner).every((s) => segmentVerdict(s, depth + 1)) ? "local" : "unknown";
    // `sh deploy.sh` runs a file whose contents the parser cannot read.
    return words.slice(1).some((w) => isScriptReference(w)) ? "unknown" : "local";
  }
  if (base === "eval" || base === "exec" || base === ".") {
    const arg = words[1] ?? "";
    if (!arg || arg.startsWith("$") || arg.startsWith("`")) return "unknown";
    return segmentVerdict(arg, depth + 1);
  }
  if (INTERPRETERS.has(base)) {
    // Inline code (`python3 -c`, `node -e`, `deno eval`, `node --eval`) can open sockets; named script files are local.
    const inline = words.slice(1).some((w) => INLINE_CODE_FLAGS.has(w) || /^-[A-Za-z]*[cer]$/u.test(w)
      || w === "--eval" || w === "--execute" || (base === "deno" && w === "eval"));
    return inline ? "unknown" : "local";
  }
  if (SHELL_KEYWORD_LOCAL.has(base)) return "local";
  if (SHELL_KEYWORD_HEADER.has(base)) {
    // `for i in $(curl …)` hides a command inside the header rather than in a body segment.
    return words.slice(1).some((w) => w.startsWith("$") || w.startsWith("`")) ? "unknown" : "local";
  }
  if (SHELL_KEYWORD_RECURSE.has(base)) {
    const rest = words.slice(1);
    return rest.length ? segmentVerdict(rest.join(" "), depth + 1) : "local";
  }
  if (WRAPPERS.has(base)) {    const rest = words.slice(1);
    while (rest.length && rest[0]!.startsWith("-")) {
      const opt = rest.shift()!;
      if (opt === "--") break;
      if (WRAPPER_BARE_OPTS.has(opt)) continue;
      if (WRAPPER_OPTS_WITH_ARG.has(opt)) {
        rest.shift(); // separate value argument
        continue;
      }
      if (opt.includes("=")) {
        if (WRAPPER_OPTS_WITH_ARG.has(opt.split("=")[0]!)) continue;
        return "unknown";
      }
      // glued value form, e.g. -uroot: a known option prefix carries its own value
      if ([...WRAPPER_OPTS_WITH_ARG].some((o) => opt.startsWith(o) && opt.length > o.length)) continue;
      return "unknown"; // unrecognized wrapper option: fail closed rather than treating it as the command
    }
    return segmentVerdict(rest.join(" "), depth + 1);
  }

  // A path/script invocation is local only when its basename is itself a known-local command or policy tool (bin/ls).
  if (isScriptReference(first) && !KNOWN_LOCAL_BINARIES.has(base) && !TOOL_POLICIES[base]) return "unknown";
  if (KNOWN_LOCAL_BINARIES.has(base)) return "local";
  const policy = TOOL_POLICIES[base];
  return policy && policy(words) ? "local" : "unknown";
}

/** The command strings a tool's structured input carries, in the shape the permission card shows them. */
function commandTexts(tool: string, input: Record<string, unknown>): string[] {
  if (!SHELL_TOOLS.has(tool)) return [];
  const raw = input.command ?? input.cmd;
  if (typeof raw === "string") return raw.trim() ? [raw] : [];
  // An argv array is one command line: join it so its flags and subcommand are read together, not token per element.
  if (Array.isArray(raw)) {
    const joined = raw.map((a) => String(a)).join(" ");
    return joined.trim() ? [joined] : [];
  }
  return [];
}

/**
 * The outward-facing category an approval would let through, or null for a confidently local-only request. A rule
 * match wins wherever it sits in the input; otherwise a shell line is local only when every segment passes the
 * known-local allowlist, so an unparseable or merely unrecognized request never takes the quiet one-tap Allow path.
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
    if (!commands.length) return { kind: "unknown" };
    if (commands.some((c) => REMOTE_SHELL.test(unquote(c)))) return { kind: "unknown" };
    return commands.every((c) => splitSegments(c).every((s) => segmentVerdict(s) === "local"))
      ? null
      : { kind: "unknown" };
  }
  return LOCAL_TOOLS.has(name) ? null : { kind: "unknown" };
}
