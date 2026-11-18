// @vitest-environment jsdom
// Overview Computers shares one Machines-style /metrics read; only online, reporting computers grow into cards.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { resetManagerChoice } from "../lib/manager-choice";
import { loadPages } from "../lib/lazy-page";
import { resetRoster } from "../lib/managers";
import type { Machine, MachineMetrics } from "../lib/types";
import { OverviewMachineCards } from "./OverviewMachineCards";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  const kept = new Map<string, string>();
  g.localStorage = { getItem: (k: string) => kept.get(k) ?? null, setItem: (k: string, v: string) => void kept.set(k, v), removeItem: (k: string) => void kept.delete(k) };
});

const GB = 1024 ** 3;
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const sample: MachineMetrics["latest"] = {
  t: 1_800_000_000,
  cpu: 42.2,
  load: 2.5,
  mem_used: 32 * GB,
  mem_total: 64 * GB,
  disk_used: 750 * GB,
  disk_total: 1000 * GB,
  net_rx: 1024 * 1024,
  net_tx: 512 * 1024,
};
const machines: Machine[] = [
  { id: "mac", name: "Mac", online: true, system: { name: "macOS" } } as Machine,
  { id: "box", name: "Box", online: false, error: "Offline" } as Machine,
  { id: "new", name: "New", online: true } as Machine,
  { id: "pending", name: "Pending", online: true, trust: "pending" } as Machine,
];
const metrics: MachineMetrics[] = [
  { machine_id: "mac", online: true, latest: sample, points: [] },
  { machine_id: "box", online: false, latest: sample, points: [] },
  { machine_id: "new", online: true, points: [] },
  { machine_id: "pending", online: true, latest: sample, points: [] },
];
const json = (value: unknown) => new Response(JSON.stringify(value), { status: 200, headers: { "Content-Type": "application/json" } });

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 20; i++) await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
};

beforeEach(() => {
  resetRoster();
  resetManagerChoice();
  vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
  vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL) => {
    const path = String(input).split("?")[0];
    return Promise.resolve(path === "/api/v1/metrics" ? json({ machines: metrics }) : json({ error: "not found" }));
  }));
  history.replaceState(null, "", "/overview");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("Overview computer monitoring cards", () => {
  it("uses one section read and leaves offline, pending and never-reporting computers compact", async () => {
    await loadPages();
    act(() => root.render(<OverviewMachineCards machines={machines} agentCounts={new Map([["mac", 2]])} offlineText={(m) => m.error || "Offline"} />));
    await flush();

    const calls = vi.mocked(fetch).mock.calls.map(([input]) => String(input));
    expect(calls.filter((path) => path === "/api/v1/metrics")).toHaveLength(1);
    expect(calls.filter((path) => /\/machines\/[^/]+\/metrics/.test(path))).toHaveLength(0);
    expect(host.querySelectorAll("[data-machine-line]")).toHaveLength(4);

    const card = host.querySelector<HTMLAnchorElement>("a[href='/machines/mac']")!;
    const summary = card.querySelector("[data-testid='machine-metrics-summary']")!;
    expect(summary.getAttribute("data-compact")).toBe("true");
    expect(summary.querySelector("svg")).toBeNull();
    expect(summary.textContent).toContain("42%");
    expect(summary.textContent).toContain("50%");
    expect(summary.textContent).toContain("75%");
    expect(summary.textContent).toContain("1.00 MB/s");
    expect(card.textContent).toContain("2 个 Agent");
    expect(card.classList.contains("h-full")).toBe(true);

    expect(host.querySelector("a[href='/machines/box'] [data-testid='machine-metrics-summary']")).toBeNull();
    expect(host.querySelector("a[href='/machines/new'] [data-testid='machine-metrics-summary']")).toBeNull();
    expect(host.querySelector("a[href='/machines/pending'] [data-testid='machine-metrics-summary']")).toBeNull();
    expect(host.querySelector("a[href='/machines/box'] [data-machine-line]")?.textContent).toBe("Offline");
    expect(host.querySelector("a[href='/machines/box']")?.classList.contains("self-start")).toBe(true);
  });

  it("opens the computer from its metrics as well as its header", async () => {
    act(() => root.render(<OverviewMachineCards machines={machines.slice(0, 1)} offlineText={() => "Offline"} />));
    await flush();
    const value = host.querySelector("a[href='/machines/mac'] [data-testid='machine-metrics-summary'] > div > div")!;
    act(() => { value.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true, button: 0 })); });
    expect(location.pathname).toBe("/machines/mac");
  });
});
