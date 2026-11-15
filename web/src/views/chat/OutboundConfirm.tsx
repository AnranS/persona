// The plain-language warning and the second confirmation step an outward-facing permission card carries (#8166).
// This whole module is a lazy chunk: it loads off the cold first screen (Interactions.tsx imports it dynamically,
// after the rest of the catalog is in), so its words live in the non-startup outbound-confirm namespace. The tiny gate
// the first screen keeps is the dependency-free lib/outbound-facade, whose pending answer is to warn.
import { t } from "../../i18n";
import { Icon } from "../../ui/icons";
import { Button, Kbd } from "../../ui/atoms";
import { applyOutboundSetting } from "../../lib/outbound-setting";
import type { OutboundRisk } from "../../lib/outbound-facade";

// On first evaluation this chunk installs the classifier matching the stored setting: the real one while the gate is on,
// a no-op ("local-only" for everything) when it is off. Until then the first-screen facade's fail-toward-warning
// "unknown" bucket is in effect.
applyOutboundSetting();

/** The risk line for a category, including the fail-toward-warning bucket. */
export function riskText(risk: OutboundRisk): string {
  switch (risk.kind) {
    case "push":
      return t("outbound-confirm.outbound_warning_push");
    case "deploy":
      return t("outbound-confirm.outbound_warning_deploy");
    case "send":
      return t("outbound-confirm.outbound_warning_send");
    case "unknown":
      return t("outbound-confirm.outbound_warning_unknown");
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

/** The one-line explanation under the warning while the second confirmation step is open. */
export function OutboundConfirmStep() {
  return <p data-outbound-confirm-step className="mt-1.5 text-[12px] leading-snug text-orange">{t("outbound-confirm.outbound_confirm_hint")}</p>;
}

export interface OutboundConfirmFooterProps {
  busy: boolean;
  /** Whether the card holds keyboard focus, and so the Enter hint shows. */
  focused: boolean;
  onDeny: () => void;
  onBack: () => void;
  onConfirm: () => void;
}

/** The footer while the second step is open: Deny stays one tap, Back leaves the step, Confirm and allow sends. */
export function OutboundConfirmFooter({ busy, focused, onDeny, onBack, onConfirm }: OutboundConfirmFooterProps) {
  return (
    <>
      <Button tone="quiet" disabled={busy} onClick={onDeny}>
        {t("chat-interactions.deny")}
      </Button>
      <span className="flex gap-1.5">
        <Button tone="quiet" disabled={busy} onClick={onBack}>
          {t("outbound-confirm.outbound_cancel")}
        </Button>
        <Button tone="danger" busy={busy} onClick={onConfirm}>
          {t("outbound-confirm.outbound_confirm_allow")}
          {focused && <Kbd>Enter</Kbd>}
        </Button>
      </span>
    </>
  );
}
