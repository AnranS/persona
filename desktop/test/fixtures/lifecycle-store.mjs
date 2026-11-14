export const scenario = { hub: "https://old.example/old/" };
export const updates = [];
export class JSONStore {
  static inUserData() { return new JSONStore(); }
  async read() { return { hub: scenario.hub }; }
  async update(patch) { updates.push(patch); }
  async saveWindow() {}
}
