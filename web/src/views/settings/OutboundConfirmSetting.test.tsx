// @vitest-environment jsdom
// The "Confirm outward-facing actions" setting (#8166): on by default, reversible through the switch on the settings row.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { OutboundConfirmRow } from "./OutboundConfirmSetting";
import { outboundConfirmEnabled, setOutboundConfirmEnabled } from "../../lib/outbound-setting";
import { t } from "../../i18n";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  localStorage.clear();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("outbound confirm setting", () => {
  it("is on by default and flips reversibly in storage", () => {
    expect(outboundConfirmEnabled()).toBe(true);
    setOutboundConfirmEnabled(false);
    expect(localStorage.getItem("persona.outboundConfirm")).toBe("0");
    expect(outboundConfirmEnabled()).toBe(false);
    setOutboundConfirmEnabled(true);
    expect(localStorage.getItem("persona.outboundConfirm")).toBeNull();
    expect(outboundConfirmEnabled()).toBe(true);
  });

  it("renders the row and toggles through its switch", async () => {
    await act(async () => root.render(<OutboundConfirmRow />));
    const sw = host.querySelector('[role="switch"]') as HTMLButtonElement;
    expect(host.textContent).toContain(t("outbound-confirm.setting_outbound_confirm"));
    expect(sw.getAttribute("aria-checked")).toBe("true");
    await act(async () => sw.click());
    expect(sw.getAttribute("aria-checked")).toBe("false");
    expect(outboundConfirmEnabled()).toBe(false);
    await act(async () => sw.click());
    expect(sw.getAttribute("aria-checked")).toBe("true");
    expect(outboundConfirmEnabled()).toBe(true);
  });
});
