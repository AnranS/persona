export function gatewayEvidenceFailure({ outsidePrefix = [], otherLocalRequests = [], gatewayRequests = [], otherGatewayEvidence } = {}) {
  const failures = [];
  if (otherLocalRequests.length) failures.push(`unattributed local requests: ${otherLocalRequests.slice(0, 5).join(", ")}`);
  const journalFailure = entry => !entry || entry.complete !== true || entry.upstreamError || entry.status === 400 || entry.status === "connecting" || entry.status === "invalid-response" || entry.status === "forwarded";
  for (const entry of gatewayRequests) {
    if (journalFailure(entry)) failures.push(`gateway request failed: ${entry.method} ${entry.path} (${entry.status ?? "incomplete"})`);
    else if (entry.status === 404 && !entry.path.includes("/api/")) failures.push(`unexpected local static request: ${entry.method} ${entry.path}`);
  }
  if (otherGatewayEvidence) {
    if (otherGatewayEvidence.outside?.length) failures.push("the second gateway received requests outside its hub prefix");
    if (otherGatewayEvidence.otherLocalRequests?.length) failures.push("the second gateway received unattributed local requests");
    for (const entry of otherGatewayEvidence.requests ?? []) {
      if (journalFailure(entry)) failures.push(`second gateway request failed: ${entry.method} ${entry.path} (${entry.status ?? "incomplete"})`);
      else if (entry.status === 404 && !entry.path.includes("/api/")) failures.push(`unexpected local static request on second gateway: ${entry.method} ${entry.path}`);
    }
  }
  return failures.length ? failures.join("; ") : undefined;
}
