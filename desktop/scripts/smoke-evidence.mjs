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
