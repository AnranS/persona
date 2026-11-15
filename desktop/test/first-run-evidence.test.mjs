import assert from "node:assert/strict";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { appAsarPath, sha256File, sourceIdentity } from "../scripts/first-run-evidence.mjs";

test("first-run evidence uses the source tree and distinguishes tracked edits from untracked probes", () => {
  const commands = [];
  const runGit = args => {
    commands.push(args);
    if (args[0] === "rev-parse" && args[1] === "HEAD") return "267b61a01dcf07a659a8ab3b14b9e8d8bd3fc513\n";
    if (args[0] === "rev-parse") return "561e17b34257ae795394be2324800aae398009db\n";
    if (args[0] === "diff") return " desktop/src/main.ts | 1 +\n";
    return "desktop/scripts/probe-first-run.mjs\n";
  };
  const identity = sourceIdentity("/synthetic/repository", runGit);
  assert.equal(identity.sourceCommit, "267b61a01dcf07a659a8ab3b14b9e8d8bd3fc513");
  assert.equal(identity.sourceTree, "561e17b34257ae795394be2324800aae398009db");
  assert.equal(identity.sourceMatchesHead, false);
  assert.equal(identity.untrackedFilesPresent, true);
  assert.deepEqual(commands[2], ["diff", "HEAD", "--"]);
  assert.equal(appAsarPath("linux", "/ignored", "/synthetic/repository/desktop"), "/synthetic/repository/desktop/release/linux-unpacked/resources/app.asar");
});

test("first-run evidence rejects untracked code even when tracked files match", () => {
  const identity = sourceIdentity("/synthetic/repository", args => {
    if (args[0] === "rev-parse" && args[1] === "HEAD") return "267b61a01dcf07a659a8ab3b14b9e8d8bd3fc513\n";
    if (args[0] === "rev-parse") return "561e17b34257ae795394be2324800aae398009db\n";
    if (args[0] === "diff") return "";
    return "desktop/src/untracked-dependency.ts\n";
  });
  assert.equal(identity.sourceMatchesHead, false);
  assert.equal(identity.untrackedFilesPresent, true);
});

test("first-run evidence hashes the real artifact contents", async t => {
  const directory = await mkdtemp(join(tmpdir(), "persona-first-run-evidence-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const file = join(directory, "app.asar");
  await writeFile(file, "synthetic archive contents");
  assert.equal(await sha256File(file), "b44bc93b0b103e120910b2b583845bd5d31242ce99fa8a4140b90e52148880ed");
});
