// The facade's fail-toward-warning answer before the lazy chunk registers the classifier (#8166). This file never
// registers it, so the synchronous getter must answer "unknown" for every request — a confidently local one included
// — and no gated call site can take the quiet single-Allow path.
import { describe, expect, it } from "vitest";
import { outboundAction } from "./outbound-facade";

describe("outboundAction facade before its classifier is registered", () => {
  it("answers the warning bucket for rule matches, local commands, file tools and missing input", () => {
    expect(outboundAction("Bash", { command: "git push origin main" })).toEqual({ kind: "unknown" });
    expect(outboundAction("Bash", { command: "npm test" })).toEqual({ kind: "unknown" });
    expect(outboundAction("Read", { file_path: "/tmp/a.txt" })).toEqual({ kind: "unknown" });
    expect(outboundAction(undefined, undefined)).toEqual({ kind: "unknown" });
  });

  it("never returns null while unregistered, so no gated call site can take the quiet single-Allow path", () => {
    for (const tool of ["Bash", "Read", "Edit", "Write", "Grep", "some_custom_tool", undefined]) {
      expect(outboundAction(tool, {})).not.toBeNull();
    }
  });
});
