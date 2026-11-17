// @vitest-environment jsdom
// UI version 10 as the web draws it (docs/extensions/extensions.md "UI version 10", #9727): a view's `graph` as an
// interactive canvas — the visible nodes as buttons in reading order, the edges in one SVG, the group frames, the
// fold controls, the selected node's ring; a node presses its action, folding is the person's and kept for the
// `key`, a redraw that changes only states keeps the position, a compact card draws the still preview that opens
// Full screen, and the keyboard walks the nodes in reading order. Its pixels at each width are checked in a real
// browser (web/e2e/extension-graph.mjs); the layout itself is lib/graph-layout.test.ts.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { readView, type UIAction } from "../../lib/extension-ui";
import { t } from "../../i18n";
import { INERT, ViewSections } from "./ViewBody";
import { forgetGraphStores, GraphCanvas } from "./ViewBody10";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
});

const doc = (html: string) => new DOMParser().parseFromString(`<body>${html}</body>`, "text/html");

// A small work graph: a root with two parts, the first with a grouped pair of ways and a step with a check, the
// second folded with two steps of its own.
const WORK = {
  sections: [{
    title: "Work",
    items: [{ type: "graph", key: "work", nodes: [
      { id: "root", title: "Ship it", subtitle: "2 parts", icon: "list", action: { id: "open", values: { id: "root" } } },
      { id: "a", title: "Part A", group: "Two ways", action: { id: "open", values: { id: "a" } } },
      { id: "b", title: "Part B" },
      { id: "way1", title: "Smallest", group: "Two ways" },
      { id: "way2", title: "Remove", group: "Two ways" },
      { id: "c", title: "Check", tone: "positive", badge: { text: "Done", tone: "positive" }, progress: 0.4 },
      { id: "s1", title: "Step 1", subtitle: "2 suites" },
      { id: "s2", title: "Step 2" },
    ], edges: [
      { from: "root", to: "a" }, { from: "root", to: "b" }, { from: "a", to: "way1" }, { from: "a", to: "way2" },
      { from: "way1", to: "c" }, { from: "b", to: "s1" }, { from: "b", to: "s2" },
    ], folded: ["b"], selected: "c" }],
  }],
};

/** The node ids the canvas shows, in reading order, with `b` folded away. */
const VISIBLE = ["root", "a", "way1", "c", "way2", "b"];

const draw = (view: unknown) => readView(view as object).sections[0].items[0];

let host: HTMLDivElement;
let root: Root;
// the item's key remounts the canvas (a different key is a different graph, as the page's redraws keep it)
const mount = async (item: Parameters<typeof GraphCanvas>[0]) => {
  await act(async () => root.render(<GraphCanvas key={item.item.key ?? ""} {...item} />));
};

describe("UI 10: the graph canvas", () => {
  beforeEach(() => {
    forgetGraphStores(); // a test's clean slate: no key keeps another test's pan or folds
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });
  afterEach(() => {
    act(() => root.unmount());
    host.remove();
    document.querySelectorAll("[role=dialog]").forEach((d) => d.remove());
  });

  const mountView = async (view: unknown, opts?: { compact?: boolean; onPress?: (a: UIAction) => void }) => {
    const pressed: UIAction[] = [];
    await mount({ item: draw(view), compact: opts?.compact, busy: "", onPress: opts?.onPress ?? ((a) => pressed.push(a)) });
    return { pressed };
  };

  it("draws the visible nodes as buttons in reading order, the edges in one SVG behind them", () => {
    // static markup: b is folded, so its own steps are hidden; the rest shows in reading order
    const d = doc(renderToStaticMarkup(<GraphCanvas item={draw(WORK)} busy="" onPress={() => {}} />));
    const nodes = [...d.querySelectorAll("[data-graph-node]")].map((e) => e.getAttribute("data-graph-node"));
    expect(nodes).toEqual(VISIBLE);
    // a node with an action is a button; without one it is still read (its label) but not focusable
    const buttons = [...d.querySelectorAll("[data-graph-node]")].filter((e) => e.tagName === "BUTTON");
    expect(buttons.map((b) => b.getAttribute("aria-label"))).toEqual([
      "Ship it，2 parts", // a root: nothing under it
      `Part A，${t("extensions-viewbody.graph_under", { title: "Ship it" })}`,
    ]);
    expect(d.querySelector('[data-graph-node="c"]')!.tagName).not.toBe("BUTTON");
    expect(d.querySelectorAll("svg path[data-graph-edge]")).toHaveLength(5); // b's two edges are folded away
    expect(d.querySelector("[data-graph-frame]")?.getAttribute("data-graph-frame")).toBe("Two ways");
    expect(d.querySelector("[data-graph-selected]")).not.toBeNull(); // c is the selected node
  });

  it("reads a node out with its badge, subtitle, progress and parent, and a fold as Fold or the hidden count", () => {
    const d = doc(renderToStaticMarkup(<GraphCanvas item={draw(WORK)} busy="" onPress={() => {}} />));
    const c = d.querySelector('[data-graph-node="c"]')!;
    expect(c.getAttribute("aria-label")).toBe(`Check，Done，40%，${t("extensions-viewbody.graph_under", { title: "Smallest" })}`);
    const fold = d.querySelector('[data-graph-fold="b"]')!;
    expect(fold.getAttribute("aria-label")).toBe(t("extensions-viewbody.graph_show_hidden", { count: 2 }));
    expect(fold.textContent).toContain("+2");
    // an open fold says Fold
    const rootFold = d.querySelector('[data-graph-fold="root"]')!;
    expect(rootFold.getAttribute("aria-label")).toBe(t("extensions-viewbody.graph_fold"));
  });

  it("presses a node's action and not the fold control beside it", async () => {
    const pressed: UIAction[] = [];
    await mountView(WORK, { onPress: (a) => pressed.push(a) });
    act(() => host.querySelector<HTMLButtonElement>('[data-graph-node="root"]')!.click());
    expect(pressed).toEqual([{ id: "open", values: { id: "root" } }]);
    act(() => host.querySelector<HTMLButtonElement>('[data-graph-fold="b"]')!.click());
    expect(pressed).toHaveLength(1); // folding never presses a node
  });

  it("folds and unfolds by the person's choice, kept across redraws of the same key", async () => {
    await mountView(WORK);
    expect(host.querySelectorAll("[data-graph-node]")).toHaveLength(VISIBLE.length); // b folded by the view
    act(() => host.querySelector<HTMLButtonElement>('[data-graph-fold="b"]')!.click());
    await act(async () => {}); // the fold's state settles
    expect(host.querySelectorAll("[data-graph-node]")).toHaveLength(VISIBLE.length + 2); // b's steps are back
    // a redraw of the same key with the view's own folded list: the person's choice wins
    const again = structuredClone(WORK);
    (again.sections[0].items[0] as { folded: string[] }).folded = ["b"];
    await mount({ item: draw(again), compact: false, busy: "", onPress: () => {} });
    expect(host.querySelectorAll("[data-graph-node]")).toHaveLength(VISIBLE.length + 2); // still the person's: b open
    // a different key takes the view's folds again
    const otherKey = structuredClone(WORK);
    (otherKey.sections[0].items[0] as { key: string }).key = "other";
    await mount({ item: draw(otherKey), compact: false, busy: "", onPress: () => {} });
    expect(host.querySelectorAll("[data-graph-node]")).toHaveLength(VISIBLE.length);
  });

  it("keeps the pan and zoom across a redraw that changes only states, and re-fits while untouched", async () => {
    const spy = vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(900);
    vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(500);
    await mountView(WORK);
    const stage = host.querySelector("[data-graph-stage]")!;
    const layer = host.querySelector("[data-graph-layer]") as HTMLElement;
    const before = layer.style.transform;
    expect(before).not.toBe(""); // it laid itself out
    // the person zooms: Ctrl + wheel over the canvas
    act(() => stage.dispatchEvent(new WheelEvent("wheel", { deltaY: -100, ctrlKey: true, bubbles: true, cancelable: true })));
    const moved = layer.style.transform;
    expect(moved).not.toBe(before);
    // a redraw with the same shape but a state moved on: the position stays
    const again = structuredClone(WORK);
    const nodes = (again.sections[0].items[0] as { nodes: { id: string; badge?: { text: string } }[] }).nodes;
    nodes.find((n) => n.id === "c")!.badge = { text: "Running" };
    await mount({ item: draw(again), compact: false, busy: "", onPress: () => {} });
    expect((host.querySelector("[data-graph-layer]") as HTMLElement).style.transform).toBe(moved);
    // while the person never moves it, a redraw takes the opening view again
    const otherKey = structuredClone(WORK);
    (otherKey.sections[0].items[0] as { key: string }).key = "fresh";
    await mount({ item: draw(otherKey), compact: false, busy: "", onPress: () => {} });
    expect((host.querySelector("[data-graph-layer]") as HTMLElement).style.transform).toBe(before);
    spy.mockRestore();
  });

  it("in a card, draws the still preview that opens Full screen, with no buttons or node presses", async () => {
    const pressed: UIAction[] = [];
    await mountView(WORK, { compact: true, onPress: (a) => pressed.push(a) });
    const preview = host.querySelector(".graph-preview")!;
    expect(preview).not.toBeNull();
    // compact: no zoom buttons, no node presses — the only button is the preview itself
    const labels = [...host.querySelectorAll("button")].map((b) => b.getAttribute("aria-label"));
    expect(labels).toEqual([t("extensions-viewbody.graph_full_screen")]);
    expect(host.querySelector("[data-graph-stage]")!.getAttribute("tabindex")).toBe("-1");
    expect(host.querySelector('[data-graph-node="root"]')!.tagName).not.toBe("BUTTON");
    act(() => (preview as HTMLButtonElement).click());
    await act(async () => {});
    const overlay = document.querySelector("[role=dialog]")!;
    expect(overlay.getAttribute("aria-modal")).toBe("true");
    expect(overlay.querySelector("[data-graph-stage]")).not.toBeNull(); // the same canvas inside
    // Esc closes it (ui/overlay's stack)
    act(() => void window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" })));
    await act(async () => {});
    expect(document.querySelector("[role=dialog]")).toBeNull();
    expect(pressed).toEqual([]); // nothing was pressed on the way
  });

  it("walks the nodes with Tab in reading order and presses with Enter", async () => {
    const pressed: UIAction[] = [];
    await mount({ item: draw(WORK), compact: false, busy: "", onPress: (a) => pressed.push(a) });
    // every node with an action is a button in the DOM's reading order; a node without one is not focusable
    const withAction = VISIBLE.filter((id) => id === "root" || id === "a");
    const focusables = withAction.map((id) => host.querySelector<HTMLButtonElement>(`[data-graph-node="${id}"]`)!);
    for (const [i, b] of focusables.entries()) {
      expect(b.tagName).toBe("BUTTON");
      if (i > 0) {
        const now = host.querySelector(`[data-graph-node="${withAction[i]}"]`)!;
        const before = host.querySelector(`[data-graph-node="${withAction[i - 1]}"]`)!;
        // `now` follows `before` in the DOM's reading order
        expect(now.compareDocumentPosition(before) & Node.DOCUMENT_POSITION_PRECEDING).toBeTruthy();
      }
    }
    const a = host.querySelector<HTMLButtonElement>('[data-graph-node="a"]')!;
    a.focus(); // Tab reaches it in the reading order above
    act(() => a.click()); // Enter or Space presses a focused button (the browser's own button behavior)
    expect(pressed).toEqual([{ id: "open", values: { id: "a" } }]);
  });

  it("grows only through design tokens: no new pixel font sizes or hex colors in what it draws", () => {
    // the per-file ratchet (scripts/ci/web_font_ratchet.py) counts the source; here the markup carries no raw
    // hex color of its own — sizes may still come from shared components (a badge's Pill)
    const html = renderToStaticMarkup(<GraphCanvas item={draw(WORK)} busy="" onPress={() => {}} />);
    expect(html).not.toMatch(/#[0-9a-f]{3,8}\b/i);
  });
});

describe("UI 10: a graph in a view", () => {
  beforeEach(() => {
    forgetGraphStores(); // a test's clean slate: no key keeps another test's pan or folds
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });
  afterEach(() => {
    act(() => root.unmount());
    host.remove();
  });

  it("loads the canvas from its own chunk into the page", async () => {
    await act(async () => root.render(<ViewSections view={readView(WORK)} {...INERT} onPress={() => {}} />));
    await vi.waitFor(() => expect(host.querySelectorAll("[data-graph-node]")).toHaveLength(VISIBLE.length), { timeout: 30_000 });
    expect(host.querySelector(".graph-canvas")).not.toBeNull(); // the page keeps its height meanwhile
  }, 60_000);
});
