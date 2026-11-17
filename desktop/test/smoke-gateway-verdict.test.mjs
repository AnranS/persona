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

test("ordinary prefix escapes require a complete refusal, never an accepted page", () => {
  const outsidePrefix = ["GET /outside (request)"];
  assert.equal(gatewayEvidenceFailure({ outsidePrefix, gatewayRequests: [{ method: "GET", path: "/outside", status: 404, complete: true }] }), undefined);
  assert.match(gatewayEvidenceFailure({ outsidePrefix, gatewayRequests: [{ method: "GET", path: "/outside", status: 200, complete: true }] }), /not completely refused/);
  assert.match(gatewayEvidenceFailure({ outsidePrefix, gatewayRequests: [{ method: "GET", path: "/outside", status: 404, complete: false }] }), /not completely refused/);
});

test("only the documented inbox time-zone fallback tolerates an upstream 400", () => {
  const base = { method: "GET", path: "/api/v1/inbox/done/days", status: 400, complete: true, responseCategory: "unsupported-time-zone" };
  assert.equal(gatewayEvidenceFailure({ gatewayRequests: [base] }), undefined);
  assert.equal(gatewayEvidenceFailure({ gatewayRequests: [{ ...base, path: "/persona/api/v1/inbox/done/days" }] }), undefined);
  assert.equal(gatewayEvidenceFailure({ otherGatewayEvidence: { requests: [{ ...base, path: "/persona/api/v1/inbox/done/days" }] } }), undefined);
  for (const request of [
    { ...base, responseCategory: "unclassified" },
    { ...base, responseCategory: undefined },
    { ...base, status: 200 },
    { ...base, complete: false },
    { ...base, upstreamError: "read failed" },
    { ...base, clientAborted: true },
    { ...base, path: "/api/v1/inbox/done/days/extra" },
    { ...base, path: "/api/v1/other" },
    { ...base, method: "POST" },
  ]) assert.match(gatewayEvidenceFailure({ gatewayRequests: [request] }), /gateway request failed/, JSON.stringify(request));
});

test("client cancellation is distinct from upstream failure", () => {
  const cancelled = { method: "GET", path: "/api/v1/models", status: "client-aborted", clientAborted: true, complete: false };
  assert.equal(gatewayEvidenceFailure({ gatewayRequests: [cancelled] }), undefined);
  assert.equal(gatewayEvidenceFailure({ otherGatewayEvidence: { requests: [cancelled] } }), undefined);
  assert.match(gatewayEvidenceFailure({ gatewayRequests: [{ ...cancelled, upstreamError: "socket hang up" }] }), /gateway request failed/);
});

test("metadata-free traffic stays diagnostic, not proof of another local process", () => {
  assert.equal(gatewayEvidenceFailure({ otherLocalRequests: ["GET / (request)"], gatewayRequests: [{ method: "GET", path: "/", status: 200, complete: true }] }), undefined);
  assert.equal(gatewayEvidenceFailure({ otherGatewayEvidence: { otherLocalRequests: ["GET / (request)"], requests: [{ method: "GET", path: "/", status: 200, complete: true }] } }), undefined);
});

test("gateway verdict failures retain their diagnostic text", () => {
  const error = gatewayEvidenceFailure({ gatewayRequests: [{ method: "GET", path: "/api/v1/truncated", status: 200, complete: false }] });
  assert.match(error, /truncated/);
});
