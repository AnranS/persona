export function gatewayEvidenceFailure({ outsidePrefix = [], otherLocalRequests = [], gatewayRequests = [], otherGatewayEvidence } = {}) {
  const failures = [];
  // Off-prefix documents already fail the prefixed scenario; this helper adds terminal/upstream evidence without
  // reporting the same escape twice. A metadata-free request is not attributable to another local process.
  const journalFailure = entry => !entry || entry.complete !== true || entry.upstreamError || entry.status === 400 || entry.status === "connecting" || entry.status === "invalid-response" || entry.status === "forwarded";
  const report = (entry, second = false) => {
    const prefix = second ? "second gateway" : "gateway";
    const outside = !second && outsidePrefix.some(outside => outside.startsWith(`${entry.method} ${entry.path} `));
    if (outside) {
      if (entry.complete === true && entry.status === 404 && !entry.upstreamError) return;
      failures.push(`${prefix} request outside the prefix was not completely refused: ${entry.method} ${entry.path} (${entry.status ?? "incomplete"})`);
      return;
    }
    if (entry.clientAborted && !entry.upstreamError) return;
    if (journalFailure(entry)) failures.push(`${prefix} request failed: ${entry.method} ${entry.path} (${entry.status ?? "incomplete"})`);
    else if (entry.status === 404 && !entry.path.includes("/api/")) failures.push(`unexpected local static request${second ? " on second gateway" : ""}: ${entry.method} ${entry.path}`);
  };
  for (const entry of gatewayRequests) report(entry);
  if (otherGatewayEvidence) {
    if (otherGatewayEvidence.outside?.length) failures.push("the second gateway received requests outside its hub prefix");
    for (const entry of otherGatewayEvidence.requests ?? []) report(entry, true);
  }
  return failures.length ? failures.join("; ") : undefined;
}
