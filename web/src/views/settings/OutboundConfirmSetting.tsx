// Settings › General › Agent preferences: the switch for the extra confirmation on outward-facing permission
// actions (pushes, deploys and messages sent as the user). On by default, kept only in this browser and reversible.
import { t } from "../../i18n";
import { setOutboundConfirmEnabled, useOutboundConfirm } from "../../lib/outbound-confirm";
import { Row, Switch } from "./SettingsParts";

export function OutboundConfirmRow() {
  const on = useOutboundConfirm();
  return (
    <Row label={t("settings.outbound_confirm")} hint={t("settings.outbound_confirm_hint")} wrapHint anchor="outbound-confirm">
      <Switch on={on} onChange={setOutboundConfirmEnabled} label={t("settings.outbound_confirm")} />
    </Row>
  );
}
