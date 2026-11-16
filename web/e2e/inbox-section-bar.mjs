// The Inbox's section bar in a real browser (#9053, docs/assistant/inbox.md "Section bar"): it opens on Needs you, stays in
// sight over the scrolling list, keeps to one row that scrolls sideways on a phone (its words never wrap, in every
// language), shows a choice's section alone, keeps the grouping in its trailing menu, moves to a linked item's choice,
// and screenshots each at 1280 and 390, light and dark.
// Usage: node web/e2e/inbox-section-bar.mjs <loopback-web-origin> <artifact-directory>
// The origin is an exclusively owned web server whose hub is scripts/mock/mock_hub.py started with
// MOCK_INBOX_MANY=action:30,working:6,fyi:40,noise:20,done:25 MOCK_MANAGERS=0 (only the mock's invented data).
import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import assert from "node:assert/strict";
import { chromium } from "playwright-core";

const [origin, output] = process.argv.slice(2);
if (!origin || !output || !/^http:\/\/(127\.0\.0\.1|localhost|\[::1\])(:\d+)?$/.test(origin)) {
  console.error("usage: node web/e2e/inbox-section-bar.mjs <loopback-web-origin> <artifact-directory>");
  process.exit(2);
}
await mkdir(output, { recursive: true });
let browser;
const deadline = setTimeout(() => {
  console.error("INBOX_SECTION_BAR TIMEOUT");
  void browser?.close();
  process.exit(1);
}, 300_000);

const results = {};
const errors = [];
const LOCALE = { en: "en-US", "zh-Hans": "zh-CN", ja: "ja-JP" };

async function page(theme, lang, { width = 1280, height = 820, touch = false } = {}) {
  const context = await browser.newContext({ viewport: { width, height }, hasTouch: touch, isMobile: touch, locale: LOCALE[lang], colorScheme: theme, deviceScaleFactor: 2 });
  await context.addInitScript(([th, lg]) => {
    localStorage.setItem("persona.token", "mock");
    localStorage.setItem("persona.language", lg);
    localStorage.setItem("persona.theme", th);
    for (const k of ["inbox.source", "inbox.kind", "inbox.folder", "inbox.grouping", "inbox.folds"]) localStorage.removeItem(k);
  }, [theme, lang]);
  const p = await context.newPage();
  p.on("pageerror", (e) => errors.push(e.message));
  return { context, p };
}
const shot = (p, name) => p.screenshot({ path: join(output, `${name}.png`) });
const chosen = (p) => p.locator('[data-bar-choice][aria-checked="true"]').getAttribute("data-bar-choice");
const sections = (p) => p.locator("main [data-section]").evaluateAll((els) => els.map((el) => el.getAttribute("data-section")));
// the bar's choices: one row, none taller than a pill (a wrapped word would be), and the page itself never scrolls sideways
const barShape = (p) => p.evaluate(() => {
  const bar = document.querySelector('[role="radiogroup"][data-inbox-bar], [data-inbox-bar] [role="radiogroup"]');
  const pills = [...bar.querySelectorAll("[data-bar-choice]")].map((el) => el.getBoundingClientRect());
  return { rows: new Set(pills.map((r) => Math.round(r.top))).size, tallest: Math.max(...pills.map((r) => r.height)), sideways: bar.scrollWidth > bar.clientWidth, pageWidth: document.documentElement.scrollWidth, width: innerWidth };
});

try {
  browser = await chromium.launch({ channel: "chrome", headless: true });
  for (const theme of ["light", "dark"]) {
    for (const [width, height, touch] of [[1280, 820, false], [390, 844, true]]) {
      const tag = `${width}-${theme}`;
      const { context, p } = await page(theme, "en", { width, height, touch });
      await p.goto(`${origin}/inbox`);
      await p.locator("main [data-item]").first().waitFor({ timeout: 60_000 });
      await p.waitForTimeout(600);
      // opens on Needs you, alone, with the bar over the list's scroller
      assert.equal(await chosen(p), "action", `${tag}: opens on Needs you`);
      assert.deepEqual(await sections(p), ["action"], `${tag}: Needs you alone`);
      assert.equal(await p.locator("[data-inbox-list-scroll] [data-inbox-bar]").count(), 0, `${tag}: the bar is outside the scroller`);
      const shape = await barShape(p);
      assert.equal(shape.rows, 1, `${tag}: one row of choices ${JSON.stringify(shape)}`);
      assert.ok(shape.tallest <= 24 && shape.pageWidth <= shape.width, `${tag}: no choice wraps, the page doesn't scroll sideways ${JSON.stringify(shape)}`);
      const names = await p.locator("[data-bar-choice]").evaluateAll((els) => els.map((el) => el.getAttribute("aria-label") ?? el.textContent));
      results[`${tag}-names`] = names;
      assert.match(names[0], /^Needs you, \d+$/, `${tag}: the count is in Needs you's spoken name`);
      await shot(p, `bar-needs-you-${tag}`);
      // it stays in sight while the list scrolls
      const top = await p.locator("[data-inbox-bar]").evaluate((el) => el.getBoundingClientRect().top);
      await p.locator("[data-inbox-list-scroll]").evaluate((el) => { el.scrollTop = 1500; });
      await p.waitForTimeout(200);
      assert.equal(await p.locator("[data-inbox-bar]").evaluate((el) => el.getBoundingClientRect().top), top, `${tag}: the bar stays in sight`);
      await shot(p, `bar-needs-you-scrolled-${tag}`);
      for (const id of ["working", "fyi", "done"]) {
        await p.locator(`[data-bar-choice="${id}"]`).click();
        await p.waitForTimeout(400);
        assert.deepEqual(await sections(p), [id], `${tag}: ${id} alone`);
        assert.equal(await p.locator(`main [data-section="${id}"] > div > button[aria-expanded]:not([aria-haspopup])`).count(), 0, `${tag}: ${id}'s title doesn't fold`);
        assert.equal(await p.locator("[data-inbox-list-scroll]").evaluate((el) => el.scrollTop), 0, `${tag}: ${id} from its top`);
        await shot(p, `bar-${id}-${tag}`);
      }
      await p.locator('[data-bar-choice="all"]').click();
      await p.waitForTimeout(400);
      assert.deepEqual(await sections(p), ["action", "working", "fyi", "noise", "done"], `${tag}: All stacks every section`);
      await shot(p, `bar-all-${tag}`);
      // the grouping is the bar's trailing menu
      await p.locator('[data-inbox-bar] button[aria-label="Group by"]').click();
      await p.getByRole("menuitemradio", { name: "By time" }).waitFor();
      await p.waitForTimeout(300);
      await shot(p, `bar-grouping-menu-${tag}`);
      await p.getByRole("menuitemradio", { name: "By time" }).click();
      assert.equal(await p.evaluate(() => localStorage.getItem("inbox.grouping")), "time");
      // a search shows All, clearing it goes back to the choice before
      await p.locator('[data-bar-choice="fyi"]').click();
      await p.getByRole("textbox", { name: "Search Inbox" }).fill("a");
      assert.equal(await chosen(p), "all", `${tag}: a search shows All`);
      await p.getByRole("textbox", { name: "Search Inbox" }).fill("");
      assert.equal(await chosen(p), "fyi", `${tag}: clearing it goes back`);
      // the keyboard: one stop, the arrows move and choose, the focus ring in full
      await p.locator('[data-bar-choice="fyi"]').focus();
      await p.keyboard.press("ArrowLeft");
      assert.equal(await chosen(p), "working", `${tag}: ArrowLeft chooses the one before`);
      assert.equal(await p.evaluate(() => document.activeElement?.getAttribute("data-bar-choice")), "working");
      await p.keyboard.press("Home");
      assert.equal(await chosen(p), "action", `${tag}: Home chooses the first`);
      await shot(p, `bar-keyboard-${tag}`);
      await context.close();
    }
  }
  // the words of every language keep to one row on a phone
  for (const lang of ["zh-Hans", "ja"]) {
    for (const theme of ["light", "dark"]) {
      const tag = `390-${theme}-${lang}`;
      const { context, p } = await page(theme, lang, { width: 390, height: 844, touch: true });
      await p.goto(`${origin}/inbox`);
      await p.locator("main [data-item]").first().waitFor({ timeout: 60_000 });
      await p.waitForTimeout(600);
      const shape = await barShape(p);
      assert.ok(shape.rows === 1 && shape.tallest <= 24 && shape.pageWidth <= shape.width, `${tag}: one row ${JSON.stringify(shape)}`);
      results[`${tag}-names`] = await p.locator("[data-bar-choice]").evaluateAll((els) => els.map((el) => el.getAttribute("aria-label") ?? el.textContent));
      await shot(p, `bar-needs-you-${tag}`);
      await context.close();
    }
  }
  // a link to a done item moves the list to Done, with the item's row marked; Back returns to Needs you
  {
    const { context, p } = await page("light", "en");
    await p.goto(`${origin}/inbox`);
    await p.locator("main [data-item]").first().waitFor({ timeout: 60_000 });
    const done = await p.evaluate(async () => {
      const r = await fetch("/api/v1/inbox", { headers: { Authorization: "Bearer mock" } });
      return (await r.json()).items.find((it) => it.done_at)?.id;
    });
    assert.ok(done, "the mock has a done item");
    await p.evaluate((id) => { history.pushState(null, "", `/inbox/${encodeURIComponent(id)}`); dispatchEvent(new PopStateEvent("popstate")); }, done);
    await p.waitForTimeout(800);
    assert.equal(await chosen(p), "done", "a done item's link lands on Done");
    assert.equal(await p.locator(`main [data-section="done"] [data-item="${done}"] a[aria-current="page"]`).count(), 1, "its row is marked");
    await shot(p, "bar-landed-done-1280-light");
    await p.goBack();
    await p.waitForTimeout(600);
    assert.equal(await chosen(p), "action", "Back returns to Needs you");
    await context.close();
  }
  assert.deepEqual(errors, []);
  await writeFile(join(output, "results.json"), JSON.stringify(results, null, 2));
  console.log(JSON.stringify(results, null, 2));
  console.log("INBOX_SECTION_BAR PASS");
} catch (error) {
  await writeFile(join(output, "results.json"), JSON.stringify({ results, errors, failure: String(error?.stack ?? error) }, null, 2)).catch(() => {});
  console.error(error);
  console.error("INBOX_SECTION_BAR FAIL");
  process.exitCode = 1;
} finally {
  clearTimeout(deadline);
  await browser?.close();
}
