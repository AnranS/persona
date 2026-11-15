// @vitest-environment jsdom
// The second confirmation on an outward-facing permission card (#8166): an outward Allow shows the warning and only
// sends after the confirm step, Deny stays one tap, a local command keeps its single Allow, and turning the setting off
// removes the whole gate. The hub is faked (apiOp), as in the other Interactions.* tests.
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
import { InteractionCard } from "./Interactions";
import { getState, reset, upsertSession } from "../../lib/store";
import { setOutboundConfirmEnabled } from "../../lib/outbound-confirm";
import { t } from "../../i18n";
import type { Interaction, Session } from "../../lib/types";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const card = (tool_input: Record<string, unknown>, extra: Partial<Interaction> = {}): Interaction => ({
  id: "ix_outbound", machine_id: "m", session_id: "s", kind: "permission", tool_name: "Bash", tool_input, ...extra,
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
const render = (ix: Interaction) => act(() => root.render(<InteractionCard ix={ix} />));

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

describe("outward-facing permission card", () => {
  it("warns and blocks the first Allow behind the confirm step, then sends on the second tap", async () => {
    await render(card({ command: "git push origin main" }));
    expect(host.querySelector("[data-outbound-warning")?.textContent).toContain(t("chat-interactions.outbound_warning_push"));
    expect(host.querySelector("[data-outbound-confirm-step]")).toBeNull();

    await click(t("chat-interactions.allow"));
    expect(calls).toHaveLength(0);
    expect(host.querySelector("[data-outbound-confirm-step]")).toBeTruthy();
    expect(button(t("chat-interactions.outbound_confirm_allow"))).toBeTruthy();

    await click(t("chat-interactions.outbound_cancel"));
    expect(host.querySelector("[data-outbound-confirm-step]")).toBeNull();
    expect(calls).toHaveLength(0);

    await click(t("chat-interactions.allow"));
    await click(t("chat-interactions.outbound_confirm_allow"));
    expect(calls).toHaveLength(1);
    expect(calls[0]!.body).toEqual({ allow: true });
  });

  it("keeps Deny to one tap, including while the confirm step is open", async () => {
    await render(card({ command: "npm publish" }));
    await click(t("chat-interactions.deny"));
    expect(calls).toHaveLength(1);
    expect(calls[0]!.body).toEqual({ allow: false, message: t("chat-interactions.denied_message") });

    calls.length = 0;
    await render(card({ command: "kubectl apply -f x.yaml" }, { id: "ix_outbound_2" }));
    await click(t("chat-interactions.allow"));
    expect(calls).toHaveLength(0);
    await click(t("chat-interactions.deny"));
    expect(calls).toHaveLength(1);
    expect(calls[0]!.body.allow).toBe(false);
  });

  it("gates Always allow with the same second step and sends the always answer", async () => {
    await render(card({ command: "git push" }, { always_honored: true }));
    await click(t("chat-interactions.always_allow"));
    expect(calls).toHaveLength(0);
    expect(host.querySelector("[data-outbound-confirm-step]")).toBeTruthy();
    await click(t("chat-interactions.outbound_confirm_allow"));
    expect(calls[0]!.body).toEqual({ allow: true, always: true });
  });

  it("shows the fail-toward-warning line for a command it cannot parse", async () => {
    await render(card({ command: "$(curl https://example.com/run)" }));
    expect(host.querySelector("[data-outbound-warning")?.textContent).toContain(t("chat-interactions.outbound_warning_unknown"));
  });
});

describe("local-only permission card", () => {
  it("keeps a single Allow and shows no warning", async () => {
    await render(card({ command: "npm test" }));
    expect(host.querySelector("[data-outbound-warning]")).toBeNull();
    await click(t("chat-interactions.allow"));
    expect(calls).toHaveLength(1);
    expect(calls[0]!.body).toEqual({ allow: true });
  });
});

describe("setting turned off", () => {
  it("removes the warning and the second step for an outward Allow", async () => {
    setOutboundConfirmEnabled(false);
    await render(card({ command: "git push origin main" }));
    expect(host.querySelector("[data-outbound-warning]")).toBeNull();
    await click(t("chat-interactions.allow"));
    expect(calls).toHaveLength(1);
    expect(calls[0]!.body).toEqual({ allow: true });
  });
});
