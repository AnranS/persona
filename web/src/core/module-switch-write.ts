// Writing this browser's module switch (Settings › Labs › Features, core/module-labs.ts): loaded with Labs rather than
// on the first screen, which only reads and subscribes to the switch (core/module-switch.ts).
import { forgetModuleSwitches, MODULE_SWITCH_KEY, moduleSwitchedOff, readModuleSwitches } from "./module-switch";

/** Switches a module on or off here; false when the browser does not keep it. */
export function setModuleSwitchedOff(id: string, value: boolean): boolean {
  const next = value ? [...new Set([...readModuleSwitches(), id])] : readModuleSwitches().filter((x) => x !== id);
  try {
    if (next.length) localStorage.setItem(MODULE_SWITCH_KEY, JSON.stringify(next));
    else localStorage.removeItem(MODULE_SWITCH_KEY);
    forgetModuleSwitches();
    if (moduleSwitchedOff(id) !== value) return false;
  } catch {
    return false;
  }
  forgetModuleSwitches(true);
  return true;
}
