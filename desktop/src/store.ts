import { dirname, join, resolve } from "node:path";
import { mkdir, readFile, rename, unlink, writeFile } from "node:fs/promises";
import type { WindowState } from "./window-state.js";

/** The state file contains geometry only, never a hub credential or web storage. */
export class JSONStore {
  private static pendingWrites = new Map<string, Promise<void>>();
  private static writeSequence = 0;

  constructor(private readonly file: string) {}
  static inUserData(userData: string): JSONStore { return new JSONStore(join(userData, "desktop-state.json")); }

  async read<T>(): Promise<T | undefined> {
    try {
      const value: unknown = JSON.parse(await readFile(this.file, "utf8"));
      return value && typeof value === "object" && !Array.isArray(value) ? value as T : undefined;
    } catch { return undefined; }
  }

  /** Return false after the queued read when the caller's generation or close/quit state has retired the write. */
  async update(patch: Record<string, unknown>, whenCurrent?: () => boolean): Promise<boolean> {
    // Facades are lightweight, but their canonical state path shares one read-modify-rename queue.
    const file = resolve(this.file);
    const previousWrite = JSONStore.pendingWrites.get(file) ?? Promise.resolve();
    const operation = previousWrite.then(async (): Promise<boolean> => {
      if (whenCurrent && !whenCurrent()) return false;
      const previous = await this.read<Record<string, unknown>>() ?? {};
      if (whenCurrent && !whenCurrent()) return false;
      const next = { ...previous, ...patch };
      const temporary = `${file}.${process.pid}.${++JSONStore.writeSequence}.tmp`;
      try {
        await mkdir(dirname(file), { recursive: true });
        await writeFile(temporary, `${JSON.stringify(next, null, 2)}\n`, { mode: 0o600 });
        await rename(temporary, file);
      } finally {
        // A failed write or rename must not leave a temporary file that can be mistaken for state.
        await unlink(temporary).catch(() => {});
      }
      return true;
    });
    const queued: Promise<void> = operation.then(() => {}, () => {});
    JSONStore.pendingWrites.set(file, queued);
    try { return await operation; } finally { if (JSONStore.pendingWrites.get(file) === queued) JSONStore.pendingWrites.delete(file); }
  }

  async saveWindow(state: WindowState): Promise<void> { await this.update({ window: state }); }
}
