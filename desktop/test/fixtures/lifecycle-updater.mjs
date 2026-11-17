import { EventEmitter } from "node:events";
import { app } from "electron";
export const autoUpdater = Object.assign(new EventEmitter(), {
  async checkForUpdates() {},
  quitAndInstall() {},
  autoDownload: false,
  autoInstallOnAppQuit: false,
});
export const appForUpdater = app;
export default { autoUpdater };
