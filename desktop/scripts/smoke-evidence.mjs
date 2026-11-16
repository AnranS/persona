export function browserFaviconEvidence(scenario, outside, gatewayURL) {
  // Only the exact root of the scenario's first gateway can be the favicon for its proven outside-browser hand-off.
  // It stays in outsidePrefix and the query-free request journal; this prevents a false second navigation failure.
  return scenario === "hub-prefix-parent" && gatewayURL.startsWith("http://127.0.0.1:")
    && gatewayURL.endsWith("/persona/") && outside.length > 0 && outside.every(entry => entry === "GET / (request)");
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
  return {
    ...scenario,
    steps: Array.isArray(scenario.steps) ? scenario.steps : [],
    screenshots: screenshots.map(screenshot => {
      const file = typeof screenshot === "string" ? screenshot : screenshot.name;
      return file.startsWith(`${name}/`) ? file : `${name}/${file}`;
    }),
    evidence: { ...scenario },
  };
}
