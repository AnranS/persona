import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { externalBrowserURL, hubRoute, inHub, parseDeepLink, parseHubAddress } from "../src/urls.js";

interface DeepLinkCase {
  readonly name: string;
  readonly url: string;
  readonly reason?: string;
  readonly route?: string;
}

// dist-test/test → the repository root
const fixtures = JSON.parse(readFileSync(join(import.meta.dirname, "..", "..", "..", "spec", "fixtures", "deep-links", "cases.json"), "utf8")) as { parse: DeepLinkCase[] };

test("hub root and reverse-proxy subpath are preserved", () => {
  assert.equal(parseHubAddress("https://hub.example/persona").url.href, "https://hub.example/persona/");
  assert.equal(parseHubAddress("hub.example/persona/").prefix, "/persona/");
  assert.equal(hubRoute(parseHubAddress("https://hub.example/persona"), "/s/computer/session"), "https://hub.example/persona/s/computer/session");
  assert.equal(inHub(parseHubAddress("https://hub.example/persona"), "https://hub.example/persona/api/v1/ws"), true);
  assert.equal(inHub(parseHubAddress("https://hub.example/persona"), "https://hub.example/other"), false);
  assert.equal(inHub(parseHubAddress("https://hub.example/persona"), "https://hub.example.evil.test/persona/"), false);
});

test("hub addresses reject credentials, fragments and arbitrary schemes", () => {
  for (const input of ["file:///tmp/a", "javascript:alert(1)", "https://u:p@hub.example/", "https://hub.example/#a", "https://hub.example/a b", ""]) {
    assert.throws(() => parseHubAddress(input), input);
  }
});

test("external links are restricted to HTTP(S) without credentials", () => {
  assert.equal(externalBrowserURL("https://example.com/a"), "https://example.com/a");
  assert.equal(externalBrowserURL("file:///tmp/a"), undefined);
  assert.equal(externalBrowserURL("javascript:alert(1)"), undefined);
  assert.equal(externalBrowserURL("https://u:p@example.com/"), undefined);
});

test("deep links never auto-pair or navigate to an arbitrary origin", () => {
  assert.deepEqual(parseDeepLink("persona://open?route=%2Fs%2Fcomputer%2Fsession"), { kind: "open", url: "persona://open?route=%2Fs%2Fcomputer%2Fsession", route: "/s/computer/session" });
  assert.equal(parseDeepLink("persona://link?url=https%3A%2F%2Fhub.example")?.kind, "pair-link");
  assert.equal(parseDeepLink("persona://pair?token=temporary")?.kind, "pair-app");
  assert.equal(parseDeepLink("persona://open?route=//evil.test"), undefined);
  assert.equal(parseDeepLink("persona://open?route=https://evil.test"), undefined);
  assert.equal(parseDeepLink("file:///tmp/a"), undefined);
});

test("shared desktop app-link fixtures open with the exact filtered route", async t => {
  const accepted = fixtures.parse.filter((entry): entry is DeepLinkCase & { route: string } => entry.route !== undefined);
  assert.ok(accepted.length > 0, "the shared fixture has accepted desktop app links");
  for (const entry of accepted) {
    await t.test(entry.name, () => {
      assert.deepEqual(parseDeepLink(entry.url), { kind: "open", url: entry.url, route: entry.route });
    });
  }
});

test("shared unsafe desktop app-link fixtures are refused", async t => {
  const unsafe = fixtures.parse.filter(entry => entry.url.startsWith("persona://") && entry.reason === "unsafe");
  assert.ok(unsafe.length > 0, "the shared fixture has unsafe desktop app links");
  for (const entry of unsafe) {
    await t.test(entry.name, () => {
      assert.equal(parseDeepLink(entry.url), undefined);
    });
  }
});

test("malformed canonical and route= app links are refused", () => {
  const invalid = [
    "persona://user:pw@open/persona",
    "persona://user:pw@open?route=%2Fpersona",
    "persona://open:8080?route=%2Fpersona",
    "persona://open/persona#settings",
    "persona://open?route=%2Fpersona#settings",
    "persona://open?route=persona",
    "persona://open/\\evil.example/persona",
    "persona://open?route=%2Fpersona%5Csettings",
    "persona://open/persona%00hidden",
    "persona://open?route=%2Fpersona%00hidden",
    "persona://open/persona%7Fhidden",
    "persona://open?route=%2Fpersona%7Fhidden",
  ];
  for (const input of invalid) assert.equal(parseDeepLink(input), undefined, input);
});

test("pairing links keep their complete existing results", () => {
  const link = "persona://link?url=https%3A%2F%2Fhub.example";
  const pair = "persona://pair?token=temporary";
  assert.deepEqual(parseDeepLink(link), { kind: "pair-link", url: link });
  assert.deepEqual(parseDeepLink(pair), { kind: "pair-app", url: pair });
});
