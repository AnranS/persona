// "Confirm outward-facing actions" (Settings, on by default, kept in the browser): while on, an Allow for an action the
// classifier reads as leaving the local machine asks for one extra confirmation on its permission card. Turning it off
// is reversible and returns every card to a single Allow.
import { useSyncExternalStore } from "react";

const KEY = "persona.outboundConfirm";
const listeners = new Set<() => void>();

/** Whether the extra confirmation is on (the default; only an explicit "0" turns it off). */
export function outboundConfirmEnabled(): boolean {
  try {
    return localStorage.getItem(KEY) !== "0";
  } catch {
    return true;
  }
}

export function setOutboundConfirmEnabled(on: boolean): void {
  try {
    if (on) localStorage.removeItem(KEY);
    else localStorage.setItem(KEY, "0");
  } catch {
    // storage unavailable: nothing is kept, the default (on) stays
  }
  listeners.forEach((l) => l());
}

function subscribe(l: () => void) {
  listeners.add(l);
  window.addEventListener("storage", l);
  return () => {
    listeners.delete(l);
    window.removeEventListener("storage", l);
  };
}

/** The setting for the settings page's switch and the permission card's gate. */
export function useOutboundConfirm(): boolean {
  return useSyncExternalStore(subscribe, outboundConfirmEnabled, () => true);
}
