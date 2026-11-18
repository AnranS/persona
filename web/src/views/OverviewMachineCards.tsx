import { memo, Suspense, useMemo } from "react";
import { t } from "../i18n";
import { isPending } from "../lib/add-computer";
import { lazyPage } from "../lib/lazy-page";
import { useMachineMetrics } from "../lib/machine-metrics";
import { hubLabel } from "../lib/mesh";
import { href, onLinkClick } from "../lib/router";
import type { Machine, MachineMetrics } from "../lib/types";
import { cx } from "../ui/atoms";
import { Icon } from "../ui/icons";

const MachineMetricsSummary = lazyPage(() => import("./machines/MachineMetricsSummary").then((m) => m.MachineMetricsSummary));

/** Overview's Computers: one shared metrics read, with compact rows until a computer has a current sample. */
export function OverviewMachineCards({ machines, agentCounts, offlineText }: {
  machines: Machine[];
  agentCounts?: ReadonlyMap<string, number>;
  offlineText: (machine: Machine) => string;
}) {
  // Section-level state keeps metric refreshes out of Home's Agent and task sections. No card starts work of its own.
  const { metrics } = useMachineMetrics();
  const byMachine = useMemo(() => new Map((metrics ?? []).map((m) => [m.machine_id, m])), [metrics]);
  return (
    <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 @min-[60rem]:grid-cols-3 @min-[84rem]:grid-cols-4 @min-[110rem]:grid-cols-5">
      {machines.map((m) => (
        <OverviewMachineCard
          key={m.id}
          m={m}
          count={agentCounts ? (agentCounts.get(m.id) ?? 0) : undefined}
          hub={hubLabel(m, machines)}
          metrics={byMachine.get(m.id)}
          offlineText={offlineText}
        />
      ))}
    </div>
  );
}

const OverviewMachineCard = memo(function OverviewMachineCard({ m, count, hub, metrics, offlineText }: {
  m: Machine;
  count?: number;
  hub: string;
  metrics?: MachineMetrics;
  offlineText: (machine: Machine) => string;
}) {
  const detail = { name: "machine" as const, mid: m.id };
  const line = m.online ? [count !== undefined && t("home.machine_agents", { count }), m.system?.name].filter(Boolean).join(" · ") || t("home.online") : offlineText(m);
  const monitored = hasCurrentSample(m, metrics);
  return (
    <a href={href(detail)} onClick={(e) => onLinkClick(e, detail)} className={cx("block rounded-card bg-surface shadow-card transition-colors hover:bg-hover", monitored ? "h-full" : "self-start")}>
      <span className={cx("flex items-center gap-3 px-3", monitored ? "pt-2.5" : "py-2.5")}>
        <span className={cx("flex size-8 shrink-0 items-center justify-center rounded-[8px]", m.online ? "bg-field text-ink-2" : "bg-inset text-ink-3")}>
          <Icon name="machine" size={15} />
        </span>
        <span className="min-w-0 flex-1">
          <span className="flex min-w-0 items-center gap-1.5">
            <span className="truncate text-[13px] font-medium text-ink">{m.name}</span>
            {hub && <span className="shrink-0 truncate text-[11.5px] text-ink-3">{hub}</span>}
          </span>
          <span className="block truncate text-[11.5px] text-ink-3" title={!m.online && m.error && line !== m.error ? m.error : undefined} data-machine-line>{line}</span>
        </span>
        <span className={cx("size-2 shrink-0 rounded-full", m.online ? "bg-green" : "bg-line-strong")} title={m.online ? t("home.online") : t("home.offline")} />
      </span>
      {monitored && <Suspense fallback={null}><MachineMetricsSummary machine={m} metrics={metrics} compact /></Suspense>}
    </a>
  );
});

function hasCurrentSample(machine: Machine, metrics?: MachineMetrics): boolean {
  if (!machine.online || isPending(machine) || metrics?.online === false || !metrics?.latest) return false;
  const latest = metrics.latest;
  return [latest.cpu, latest.load, latest.mem_used, latest.mem_total, latest.net_rx, latest.net_tx, latest.disk_used, latest.disk_total]
    .some((value) => typeof value === "number" && Number.isFinite(value));
}
