// The first-screen registry for the outward-facing permission classifier (#8166). Deliberately dependency-free so the
// lazy classifier/UI chunk (views/chat/OutboundConfirm) can register here without statically importing the large App
// chunk, which keeps it out of that chunk's dynamic-import preload closure. The real classifier lives in
// ./outbound-actions.classifier (that lazy chunk); until it registers, every synchronous read takes the
// fail-toward-warning "unknown" bucket, so a pending chunk can never take the quiet single-Allow path.
import type { OutboundRisk } from "./outbound-actions.classifier";

export type { OutboundRisk } from "./outbound-actions.classifier";

type Classifier = (tool: string | undefined, input: Record<string, unknown> | undefined) => OutboundRisk | null;

let classifier: Classifier | null = null;

/** Registers the real classifier, run by the lazy chunk the first time it evaluates. */
export function setOutboundClassifier(real: Classifier): void {
  classifier = real;
}

/**
 * The outward-facing category an approval would let through, or null for a confidently local-only request. While the
 * lazy chunk has not registered the classifier, this is the fail-toward-warning "unknown" bucket for every request, a
 * local one included: the second confirmation stays on until the real answer is in.
 */
export function outboundAction(tool: string | undefined, input: Record<string, unknown> | undefined): OutboundRisk | null {
  return classifier ? classifier(tool, input) : { kind: "unknown" };
}
