// Settings › General › Agent preferences: the switch for the extra confirmation on outward-facing permission
// actions (pushes, deploys and messages sent as the user). On by default, kept only in this browser and reversible.
// The row owns its switch state; the flag itself is read straight from storage wherever a card needs it.
import { useState } from "react";
import { t } from "../../i18n";
import { outboundConfirmEnabled, setOutboundConfirmEnabled } from "../../lib/outbound-setting";
import { Row, Switch } from "./SettingsParts";

export function OutboundConfirmRow() {
  const [on, setOn] = useState(outboundConfirmEnabled);
  const change = (next: boolean) => {
    setOutboundConfirmEnabled(next);
    setOn(next);
  };
  return (
    <Row label={t("outbound-confirm.setting_outbound_confirm")} hint={t("outbound-confirm.setting_outbound_confirm_hint")} wrapHint anchor="outbound-confirm">
      <Switch on={on} onChange={change} label={t("outbound-confirm.setting_outbound_confirm")} />
    </Row>
  );
}
