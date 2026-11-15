// The "Confirm outward-facing actions" setting, kept off the cold first screen. This module is part of the lazy
// confirm/settings chunks: the first-screen facade (./outbound-facade) stays dependency-free and simply holds whichever
// classifier this registers. With the gate on (the default) it registers the real classifier; turned off it registers a
// no-op that answers "local-only" for everything, so the one-tap Allow returns. Until a lazy chunk first runs this, the
// facade's fail-toward-warning "unknown" bucket is in effect — the safe, over-warning default, never a quiet allow.
import { setOutboundClassifier } from "./outbound-facade";
import { outboundAction } from "./outbound-actions.classifier";

const OUTBOUND_KEY = "persona.outboundConfirm";
const alwaysLocal = (): null => null;

/** Whether the extra confirmation is on (the default; only an explicit "0" turns it off). */
export function outboundConfirmEnabled(): boolean {
  try {
    return localStorage.getItem(OUTBOUND_KEY) !== "0";
  } catch {
    return true;
  }
}

/** Persist the choice and swap the facade's classifier so the change is immediate, including on an already-open card. */
export function setOutboundConfirmEnabled(on: boolean): void {
  try {
    if (on) localStorage.removeItem(OUTBOUND_KEY);
    else localStorage.setItem(OUTBOUND_KEY, "0");
  } catch {
    // storage unavailable: nothing is kept, the default (on) stays
  }
  setOutboundClassifier(on ? outboundAction : alwaysLocal);
}

/** Register the classifier matching the stored setting once a lazy confirm/settings chunk first evaluates. */
export function applyOutboundSetting(): void {
  setOutboundClassifier(outboundConfirmEnabled() ? outboundAction : alwaysLocal);
}
