import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { createReadStream } from "node:fs";
import { join } from "node:path";

export async function sha256File(file) {
  const hash = createHash("sha256");
  for await (const chunk of createReadStream(file)) hash.update(chunk);
  return hash.digest("hex");
}

/** Record the source identity used for this build without treating unrelated untracked files as source changes. */
export function sourceIdentity(root, runGit = args => execFileSync("git", args, { cwd: root, encoding: "utf8" })) {
  const head = runGit(["rev-parse", "HEAD"]).trim();
  const sourceTree = runGit(["rev-parse", "HEAD^{tree}"]).trim();
  const trackedDiff = runGit(["diff", "HEAD", "--"]);
  const untracked = runGit(["ls-files", "--others", "--exclude-standard"]);
  return { sourceCommit: head, sourceTree, sourceMatchesHead: trackedDiff.length === 0 && untracked.length === 0, untrackedFilesPresent: untracked.length > 0 };
}

export function appAsarPath(platform, file, desktop) {
  if (platform === "darwin") return join(file, "..", "..", "Resources", "app.asar");
  if (platform === "win32") return join(file, "..", "resources", "app.asar");
  return join(desktop, "release", "linux-unpacked", "resources", "app.asar");
}
