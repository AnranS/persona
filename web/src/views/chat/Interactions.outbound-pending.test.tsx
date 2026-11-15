// @vitest-environment jsdom
// The fail-toward-warning path while the card's lazy chunk has not given a local-only answer (#8166). The warning/
// confirm module is mocked to report every request as the "unknown" warning bucket, which is exactly the state the
// first-screen card is in while its real chunk is still loading: Allow and Always allow never send in one tap, and
// Deny stays one tap. The loaded path with the real classifier is covered by Interactions.outbound-confirm.test.tsx.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
const calls = vi.hoisted(() => [] as { path: string; body: Record<string, unknown> }[]);
vi.mock("../../lib/ops", () => ({
  apiOp: vi.fn(async (path: string, init: { json: Record<string, unknown> }) => {
    calls.push({ path, body: init.json });
    return {};
  }),
}));
// Stands in for the lazy chunk while it cannot yet answer: the warning bucket for every request, and the warning and
// confirm-step markers the card renders from it.
vi.mock("./OutboundConfirm", () => ({
  outboundRisk: () => ({ kind: "unknown" as const }),
  OutboundWarning: () => <div data-outbound-warning />,
  OutboundConfirmStep: () => <div data-outbound-confirm-step />,
  OutboundConfirmFooter: ({ onDeny }: { onDeny: () => void }) => (
    <button type="button" data-stub-deny onClick={onDeny}>stub deny</button>
  ),
}));
import { InteractionCard } from "./Interactions";
import { getState, reset, upsertSession } from "../../lib/store";
import { setOutboundConfirmEnabled } from "../../lib/quick-answers";
import { t } from "../../i18n";
import type { Interaction, Session } from "../../lib/types";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const card = (tool_input: Record<string, unknown>, extra: Partial<Interaction> = {}): Interaction => ({
  id: "ix_pending", machine_id: "m", session_id: "s", kind: "permission", tool_name: "Bash", tool_input, ...extra,
});

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); });
};
const button = (label: string) =>
  [...host.querySelectorAll("button")].find((b) => (b.textContent ?? "").trim() === label) as HTMLButtonElement | undefined;
const click = async (label: string) => {
  const b = button(label);
  expect(b, label).toBeTruthy();
  await act(async () => b!.click());
  await flush();
};
const render = async (ix: Interaction) => {
  act(() => root.render(<InteractionCard ix={ix} />));
  // The mocked chunk still arrives through the dynamic-import pipeline (a macrotask), so wait for its commit rather
  // than a fixed number of microtask turns.
  for (let i = 0; i < 100 && !host.querySelector("[data-outbound-warning]"); i++) {
    await act(async () => { await new Promise((r) => setTimeout(r, 5)); });
  }
  await flush();
};

beforeEach(() => {
  localStorage.clear();
  calls.length = 0;
  setOutboundConfirmEnabled(true);
  upsertSession({ machine_id: "m", session_id: "s", title: "Demo", state: "needs_you" } as Session);
  Object.assign(getState(), { interactions: [], answered: {} });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
});

describe("permission card while its lazy chunk only has the warning answer", () => {
  it("gates even a local-only command and shows the warning marker", async () => {
    // "npm test" is confidently local once the real classifier answers; in this pending state it reads "unknown".
    await render(card({ command: "npm test" }));
    expect(host.querySelector("[data-outbound-warning]")).toBeTruthy();
  });

  it("never sends an Allow from the card's first Allow tap", async () => {
    await render(card({ command: "npm test" }));
    await click(t("chat-interactions.allow"));
    expect(calls).toHaveLength(0);
    expect(host.querySelector("[data-outbound-confirm-step]")).toBeTruthy();
    expect(host.querySelector("[data-stub-deny]")).toBeTruthy();
  });

  it("keeps Deny to one tap", async () => {
    await render(card({ command: "npm test" }));
    await click(t("chat-interactions.deny"));
    expect(calls).toHaveLength(1);
    expect(calls[0]!.body.allow).toBe(false);
  });

  it("sends no always-answer from Always allow", async () => {
    await render(card({ command: "npm test" }, { always_honored: true }));
    await click(t("chat-interactions.always_allow"));
    expect(calls).toHaveLength(0);
    expect(host.querySelector("[data-outbound-confirm-step]")).toBeTruthy();
    // the second step's Deny (here the stub) still answers deny, never an allow body
    await act(async () => host.querySelector<HTMLButtonElement>("[data-stub-deny]")!.click());
    await flush();
    expect(calls).toHaveLength(1);
    expect(calls[0]!.body.allow).toBe(false);
  });
});
