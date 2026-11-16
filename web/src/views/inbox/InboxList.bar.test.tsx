// @vitest-environment jsdom
// The Inbox's section bar (docs/assistant/inbox.md "Section bar", #9053): it opens on 需要你, each choice shows its
// section alone with its title as a heading, the counts are in their tones and spoken with the names, the arrow keys
// move along it, a search shows 全部 and clearing it goes back, a choice with nothing in it says so in its own words,
// and the grouping is its trailing menu. The rules themselves are replayed from spec/fixtures/inbox (lib/inbox-bar.test.ts).
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});

const hub = vi.hoisted(() => ({ items: [] as unknown[], waiting: true }));
vi.mock("../../lib/api", async (orig) => ({
  ...(await orig<typeof import("../../lib/api")>()),
  api: (path: string) => {
    if (path === "/mail/accounts") return Promise.resolve({ accounts: [{ id: "a1", email: "me@example.com" }] });
    if (path.startsWith("/inbox?")) return Promise.resolve({ items: hub.items });
    return Promise.resolve({});
  },
}));
// an agent waiting on you, counted in 需要你
vi.mock("../../lib/store", async (orig) => {
  const m = await orig<typeof import("../../lib/store")>();
  const interactions = [{ id: "ix1", machine_id: "m", session_id: "s1", session_title: "改登录页", kind: "question" }];
  return { ...m, useStore: <T,>(sel: (s: ReturnType<typeof m.getState>) => T) => m.useStore((s) => sel({ ...s, loaded: true, interactions: (hub.waiting ? interactions : []) as never })) };
});

import { InboxList } from "./InboxList";
import { reloadInbox, resetInboxForTests, type InboxItem } from "../../lib/inbox";
import { resetMailForTests } from "../../lib/mail";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const item = (id: string, patch: Partial<InboxItem> = {}): InboxItem => ({
  id, source: "lark", kind: "group", account: "feishu", account_name: "Lark", title: `chat ${id}`, preview: "text", count: 1,
  updated_at: "2026-11-09T04:00:00Z", tier: "action", version: `v-${id}`, lark: { feed_id: id, is_group: true }, ...patch,
});
// 需要你: two items (and the agent); 进行中: one; 知悉即可: two; 已折叠: one; 已处理: one
const everySection = () => [
  item("a1", { summary: "合同条款确认" }), item("a2"), item("w1", { status: "working" }), item("f1", { tier: "fyi" }),
  item("f2", { tier: "fyi", summary: "合同的附件" }), item("n1", { tier: "noise" }), item("d1", { done_at: "2026-11-09T05:00:00Z", done_by: "user" }),
];

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 8; i++) await act(async () => { await Promise.resolve(); });
};
const choices = () => [...host.querySelectorAll<HTMLButtonElement>("[data-bar-choice]")];
const choice = (id: string) => host.querySelector<HTMLButtonElement>(`[data-bar-choice="${id}"]`)!;
const chosen = () => choices().find((b) => b.getAttribute("aria-checked") === "true")?.dataset.barChoice;
const sections = () => [...host.querySelectorAll<HTMLElement>("[data-section]")].map((s) => s.dataset.section);
const pick = (id: string) => act(() => choice(id).click());
const key = (el: HTMLElement, k: string) => act(() => { el.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true })); });
const typeSearch = async (text: string) => {
  const input = host.querySelector<HTMLInputElement>('input[aria-label="搜索收件箱"]')!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, text);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await flush();
};
const show = async () => {
  act(() => root.render(<InboxList />));
  await flush();
};

beforeEach(() => {
  hub.items = everySection();
  hub.waiting = true;
  localStorage.clear();
  resetInboxForTests();
  resetMailForTests();
  vi.stubGlobal("CSS", { escape: (v: string) => v });
  Element.prototype.scrollIntoView = vi.fn();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe("the Inbox's section bar (#9053)", () => {
  it("opens on 需要你, with each count in its tone and spoken with its choice's name", async () => {
    await show();
    const bar = host.querySelector('[role="radiogroup"]')!;
    expect(bar.getAttribute("aria-label")).toBe("分段");
    // over the list's scroller, so it stays in sight
    expect(bar.closest("[data-inbox-list-scroll]")).toBeNull();
    // 已折叠 has no choice of its own
    expect(choices().map((b) => b.dataset.barChoice)).toEqual(["action", "working", "fyi", "done", "all"]);
    expect(choices().map((b) => b.textContent)).toEqual(["需要你3", "进行中1", "知悉即可2", "已处理", "全部"]);
    expect(chosen()).toBe("action");
    expect(choices().map((b) => b.tabIndex)).toEqual([0, -1, -1, -1, -1]);
    expect(choices().map((b) => b.getAttribute("aria-label"))).toEqual(["需要你，3", "进行中，1", "知悉即可，2", null, null]);
    // the blue of 需要你 (the agent waiting counts), the neutral tone for the rest; the number is in the name, not read twice
    const badge = (id: string) => choice(id).querySelector<HTMLElement>("[data-count-badge]");
    expect([badge("action")?.dataset.countBadge, badge("working")?.dataset.countBadge, badge("fyi")?.dataset.countBadge]).toEqual(["needs", "neutral", "neutral"]);
    expect(badge("action")?.getAttribute("aria-hidden")).toBe("true");
    expect(badge("done")).toBeNull();
    // only 需要你 is drawn, its title a heading that doesn't fold
    expect(sections()).toEqual(["action"]);
    expect(host.querySelector('[data-section="action"] h3')?.textContent).toContain("需要你");
    expect(host.querySelector('[data-section="action"] > div > button[aria-expanded]')).toBeNull();
  });

  it("shows a choice's section alone with its title's actions, and 全部 stacks them all as before", async () => {
    await show();
    pick("fyi");
    expect(chosen()).toBe("fyi");
    expect(sections()).toEqual(["fyi"]);
    const fyi = host.querySelector('[data-section="fyi"]')!;
    expect(fyi.querySelector("h3")?.textContent).toContain("知悉即可");
    expect([...fyi.querySelectorAll("button")].some((b) => b.textContent === "全部看过")).toBe(true);
    expect([...fyi.querySelectorAll("[data-item]")].map((r) => r.getAttribute("data-item"))).toEqual(["f1", "f2"]);

    pick("done");
    expect(sections()).toEqual(["done"]);
    expect(host.querySelector('[data-section="done"] h3')?.textContent).toContain("已处理");
    expect(host.querySelector('[data-section="done"] [data-item="d1"]')?.closest("[inert]")).toBeNull();

    pick("all");
    expect(sections()).toEqual(["action", "working", "fyi", "noise", "done"]);
    const folds = (k: string) => host.querySelector(`[data-section="${k}"] > div > button[aria-expanded]`)?.getAttribute("aria-expanded");
    expect(["action", "working", "fyi", "noise", "done"].map(folds)).toEqual(["true", undefined, "true", "false", "false"]);
  });

  it("moves along the bar with the arrow keys, Home and End, choosing as it goes", async () => {
    await show();
    act(() => choice("action").focus());
    await key(choice("action"), "ArrowRight");
    expect(chosen()).toBe("working");
    expect(document.activeElement).toBe(choice("working"));
    expect(choice("working").tabIndex).toBe(0);
    await key(choice("working"), "End");
    expect(chosen()).toBe("all");
    expect(document.activeElement).toBe(choice("all"));
    await key(choice("all"), "Home");
    expect(chosen()).toBe("action");
    await key(choice("action"), "ArrowLeft");
    expect(chosen()).toBe("all");
    expect(sections()).toEqual(["action", "working", "fyi", "noise", "done"]);
  });

  it("searches the whole Inbox: its text shows 全部 with the matches' counts, a choice narrows, clearing goes back", async () => {
    await show();
    pick("fyi");
    await typeSearch("合同");
    expect(chosen()).toBe("all");
    expect(choices().map((b) => b.textContent)).toEqual(["需要你1", "知悉即可1", "全部"]);
    expect(sections()).toEqual(["action", "fyi"]);
    pick("action");
    expect(sections()).toEqual(["action"]);
    await typeSearch("");
    expect(chosen()).toBe("fyi");
    expect(sections()).toEqual(["fyi"]);
  });

  it("says a choice with nothing in it is empty in its own words, not 都处理完了", async () => {
    hub.items = [item("f1", { tier: "fyi" })];
    hub.waiting = false;
    await show();
    expect(chosen()).toBe("action");
    const empty = host.querySelector<HTMLElement>("[data-inbox-section-empty]")!;
    expect(empty.dataset.inboxSectionEmpty).toBe("action");
    expect(empty.textContent).toBe("没有需要你处理的");
    expect(host.textContent).not.toContain("都处理完了");
    expect(host.querySelector(".bg-green-tint")).toBeNull();
    // no count at zero, and the rows elsewhere are counted right above
    expect(choices().map((b) => b.textContent)).toEqual(["需要你", "知悉即可1", "全部"]);
  });

  it("keeps a chosen choice while it empties, and says so", async () => {
    await show();
    pick("working");
    hub.items = everySection().map((it) => (it.id === "w1" ? { ...it, done_at: "2026-11-09T06:00:00Z", done_by: "manager" } : it));
    await act(async () => { await reloadInbox(); });
    await flush();
    expect(chosen()).toBe("working");
    expect(choice("working").textContent).toBe("进行中");
    expect(host.querySelector("[data-inbox-section-empty]")?.textContent).toBe("没有进行中的");
    // left, it goes: no row and not chosen
    pick("action");
    expect(choice("working")).toBeNull();
  });

  it("says 都处理完了 only when nothing is open anywhere, with 已处理 still a choice", async () => {
    hub.items = [item("d1", { done_at: "2026-11-09T05:00:00Z", done_by: "user" })];
    hub.waiting = false;
    await show();
    expect(host.textContent).toContain("都处理完了");
    expect(host.querySelector("[data-inbox-section-empty]")).toBeNull();
    expect(choices().map((b) => b.dataset.barChoice)).toEqual(["action", "done", "all"]);
    // 全部: the same, with 已处理 under it
    pick("all");
    expect(host.textContent).toContain("都处理完了");
    expect(sections()).toEqual(["done"]);
  });

  it("has the grouping in its trailing menu, not where the choice is not grouped", async () => {
    await show();
    const grouping = () => host.querySelector('[data-inbox-bar] button[aria-label="分组方式"]');
    expect(grouping()).not.toBeNull();
    expect(host.querySelector('[role="radiogroup"][aria-label="分组方式"]')).toBeNull(); // no row of its own
    pick("done");
    expect(grouping()).toBeNull();
    pick("working");
    expect(grouping()).toBeNull();
    pick("all");
    expect(grouping()).not.toBeNull();
  });

  it("caps a count at 99+ and still says the exact number", async () => {
    hub.items = Array.from({ length: 120 }, (_, i) => item(`a${i}`));
    await show();
    expect(choice("action").querySelector("[data-count-badge]")?.textContent).toBe("99+");
    expect(choice("action").getAttribute("aria-label")).toBe("需要你，121");
  });
});
