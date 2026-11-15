// The plain-language warning an outward-facing permission card carries above its buttons (#8166): one line saying what
// the action does beyond this machine, by the classifier's category. The second confirmation step itself lives in
// Interactions.tsx's PermissionCard; this is only the words and the mark, drawn with the design system's tokens.
import { t } from "../../i18n";
import { Icon } from "../../ui/icons";
import type { OutboundRisk } from "../../lib/outbound-actions";

/** The risk line for a category, including the fail-toward-warning bucket. */
export function riskText(risk: OutboundRisk): string {
  switch (risk.kind) {
    case "push":
      return t("chat-interactions.outbound_warning_push");
    case "deploy":
      return t("chat-interactions.outbound_warning_deploy");
    case "send":
      return t("chat-interactions.outbound_warning_send");
    case "unknown":
      return t("chat-interactions.outbound_warning_unknown");
  }
}

export function OutboundWarning({ risk }: { risk: OutboundRisk }) {
  return (
    <div data-outbound-warning role="note" className="mt-2 flex items-start gap-2 rounded-control bg-orange-tint px-2.5 py-2 text-[12px] leading-snug text-orange shadow-hairline">
      <Icon name="warning" size={13} stroke={2.2} className="mt-px shrink-0" />
      <span className="min-w-0">{riskText(risk)}</span>
    </div>
  );
}
