import assert from "node:assert/strict";
import test from "node:test";
import { gatewayEvidenceFailure } from "../scripts/smoke-gateway-verdict.mjs";

const complete = { method: "GET", path: "/api/v1/test", status: 200, complete: true };

test("a healthy accepted upgrade reaches a terminal state", () => {
  assert.equal(gatewayEvidenceFailure({ gatewayRequests: [{ method: "UPGRADE", path: "/api/v1/ws", status: 101, closed: true, complete: true }] }), undefined);
});

test("unexpected static 404s fail even with browser metadata", () => {
  assert.match(gatewayEvidenceFailure({ gatewayRequests: [{ method: "GET", path: "/persona/private-local-path.txt", status: 404, complete: true }] }), /unexpected local static request/);
});

test("complete ordinary gateway requests do not fail", () => {
  assert.equal(gatewayEvidenceFailure({ gatewayRequests: [complete] }), undefined);
});

test("incomplete, malformed and failed gateway requests fail closed", () => {
  for (const request of [
    { method: "GET", path: "/api/v1/aborted", status: 200, complete: false },
    { method: "GET", path: "/api/v1/malformed", status: 400, complete: true },
    { method: "UPGRADE", path: "/api/v1/ws", status: "connecting", complete: true },
    { method: "UPGRADE", path: "/api/v1/ws", status: "forwarded", complete: true },
    { method: "UPGRADE", path: "/api/v1/ws", status: 400, complete: true },
    { ...complete, upstreamError: true },
  ]) assert.match(gatewayEvidenceFailure({ gatewayRequests: [request] }), /gateway request failed/);
});

test("unexpected local and second-gateway failures affect the verdict", () => {
  assert.match(gatewayEvidenceFailure({ otherLocalRequests: ["GET /persona/private-local-path (request)"] }), /unattributed local requests/);
  assert.match(gatewayEvidenceFailure({ otherGatewayEvidence: { otherLocalRequests: ["GET /unexpected (request)"], requests: [] } }), /second gateway/);
});

test("gateway verdict failures retain their diagnostic text", () => {
  const error = gatewayEvidenceFailure({ gatewayRequests: [{ method: "GET", path: "/api/v1/truncated", status: 200, complete: false }] });
  assert.match(error, /truncated/);
});
