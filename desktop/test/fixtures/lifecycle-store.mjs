export const scenario = { hub: "https://old.example/old/", failWindowSave: false, holdUpdate: false, failUpdate: false };
export { scenario as storeScenario };
export const updates = [];
export const currentHub = async () => scenario.hub;
export class JSONStore {
  static inUserData() { return new JSONStore(); }
  async read() { return { hub: scenario.hub }; }
  async update(patch, whenCurrent) {
    if (scenario.holdUpdate) await new Promise(done => { scenario.releaseUpdate = done; });
    if (scenario.failUpdate) throw new Error("synthetic write failure during Quit");
    if (whenCurrent && !whenCurrent()) return false;
    updates.push(patch); scenario.hub = patch.hub ?? scenario.hub;
    return true;
  }
  async saveWindow() { if (scenario.failWindowSave) throw new Error("synthetic geometry save failure"); }
}
