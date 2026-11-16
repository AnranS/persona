export function browserFaviconEvidence(scenario, outside, gatewayURL, external = []) {
  // A metadata-free favicon is allowed only for an off-prefix URL that Electron actually handed to the system browser
  // during this scenario. The driver separately proves the hub window never left its prefix. A root 404 is not proof.
  const origin = gatewayURL.match(/^http:\/\/127\.0\.0\.1:\d+/)?.[0];
  const handedOff = external.some(url => origin && (url === `${origin}/` || (url.startsWith(origin) && !url.startsWith(gatewayURL))));
  return (scenario === "subpath" || scenario === "hub-prefix-parent") && handedOff
    && outside.length > 0 && outside.every(entry => entry === "GET / (request)");
}

export async function settleGatewayHTTP(gateway, timeout = 15_000) {
  const deadline = Date.now() + timeout;
  for (;;) {
    const pending = gateway.requests().filter(entry => entry.method !== "UPGRADE" && !entry.closed);
    if (pending.length === 0) return;
    if (Date.now() >= deadline) throw new Error(`Gateway HTTP requests did not settle within ${timeout} ms: ${pending.slice(0, 5).map(entry => `${entry.method} ${entry.path}`).join(", ")}`);
    await new Promise(done => setTimeout(done, 25));
  }
}

export function recordScenarioEvidence(current, failure) {
  const error = failure instanceof Error ? failure.message : String(failure);
  const inner = current ?? { ok: false, error: "The Electron result was not observed", steps: [] };
  return { ...inner, ok: false, error, outerFailure: error, innerResult: inner };
}

export function smokeScenarioSummary(scenario, name) {
  const screenshots = Array.isArray(scenario.screenshots) ? scenario.screenshots : [];
  const malformedScreenshots = [];
  const indexed = screenshots.map((screenshot, index) => {
    const file = typeof screenshot === "string" ? screenshot : screenshot && typeof screenshot === "object" && typeof screenshot.name === "string" ? screenshot.name : undefined;
    if (!file) { malformedScreenshots.push({ index, reason: "missing screenshot name" }); return undefined; }
    return file.startsWith(`${name}/`) ? file : `${name}/${file}`;
  });
  return {
    ...scenario,
    steps: Array.isArray(scenario.steps) ? scenario.steps : [],
    screenshots: indexed.filter(file => file !== undefined),
    ...(malformedScreenshots.length ? { malformedScreenshots } : {}),
    evidence: { ...scenario },
  };
}
