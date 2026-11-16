export const scenario = { hub: "https://old.example/old/", failWindowSave: false };
export const updates = [];
export const currentHub = async () => scenario.hub;
export class JSONStore {
  static inUserData() { return new JSONStore(); }
  async read() { return { hub: scenario.hub }; }
  async update(patch) { updates.push(patch); scenario.hub = patch.hub ?? scenario.hub; }
  async saveWindow() { if (scenario.failWindowSave) throw new Error("synthetic geometry save failure"); }
}
