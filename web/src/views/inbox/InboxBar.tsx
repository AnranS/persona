// The Inbox's section bar (docs/assistant/inbox.md "Section bar", #9053): one row of choices over the list, drawn as the
// Agents list's quick filters are (Sidebar's FilterBar), with Needs you's count in its blue, In progress's and FYI's in
// the neutral tone, and the grouping in a menu at the row's end. One row that scrolls sideways when it doesn't fit; the
// arrow keys, Home and End move along it and choose, as in a radio group.
import { useRef } from "react";
import { t } from "../../i18n";
import { TIERS } from "../../lib/inbox";
import { nextChoice, type BarChoice, type BarEntry } from "../../lib/inbox-bar";
import type { Grouping } from "../../lib/inbox-groups";
import { CountBadge, cx, IconButton, Menu, useMenu } from "../../ui/atoms";

/** A choice's name: the section's title, Done, or All. */
export function barChoiceName(id: BarChoice): string {
  if (id === "done") return t("inbox-inboxlist.done");
  if (id === "all") return t("inbox-inboxlist.all");
  return TIERS.find((tier) => tier.key === id)?.title ?? id;
}

/** `grouping`: the list's grouping, with its menu at the end; null where the choice is not grouped (Done, In progress). */
export function InboxBar({ entries, chosen, onPick, grouping, onGrouping }: { entries: BarEntry[]; chosen: BarChoice; onPick: (choice: BarChoice) => void; grouping: Grouping | null; onGrouping: (g: Grouping) => void }) {
  const pills = useRef<(HTMLButtonElement | null)[]>([]);
  const menu = useMenu();
  return (
    <div className="flex items-center gap-1" data-inbox-bar>
      {/* room around the pills for their touch targets (44 px around 24) and the keyboard's focus ring, which a sideways
          scroller would clip, given back by the negative margin so the row sits where it would */}
      <div role="radiogroup" aria-label={t("inbox-inboxlist.sections")} className="-mx-1 -my-2.5 flex min-w-0 flex-1 items-center gap-0.5 overflow-x-auto px-1 py-2.5 scrollbar-none">
        {entries.map((e, i) => {
          const on = e.id === chosen;
          const name = barChoiceName(e.id);
          const n = e.count ?? 0;
          return (
            <button
              key={e.id}
              ref={(el) => {
                pills.current[i] = el;
              }}
              type="button"
              role="radio"
              aria-checked={on}
              // one stop for the keyboard: the chosen one; the arrows move along the row
              tabIndex={on ? 0 : -1}
              // the count is part of the name ("Needs you, 12"); the badge itself is not read twice
              aria-label={n > 0 ? t("inbox-inboxlist.bar_count", { name, count: n }) : undefined}
              data-bar-choice={e.id}
              onClick={() => onPick(e.id)}
              onKeyDown={(ev) => {
                const to = nextChoice(i, ev.key, entries.length);
                if (to === undefined) return;
                ev.preventDefault();
                pills.current[to]?.focus();
                if (to !== i) onPick(entries[to]!.id);
              }}
              className={cx(
                "touch-hit relative z-10 flex h-6 shrink-0 items-center gap-1 whitespace-nowrap rounded-full px-[7px] text-submeta transition-[background-color,color,box-shadow] duration-150",
                on ? "bg-surface font-medium text-ink shadow-btn" : "text-ink-2 hover:bg-hover hover:text-ink",
              )}
            >
              {name}
              {e.tone && <CountBadge count={n} tone={e.tone} label={false} />}
            </button>
          );
        })}
      </div>
      {grouping && (
        <>
          <span aria-hidden className="h-4 w-px shrink-0 bg-line-strong" />
          <IconButton icon="filter" label={t("inbox-inboxlist.grouping")} onClick={menu.open} active={Boolean(menu.anchor)} size={24} iconSize={13} />
          {menu.anchor && (
            <Menu anchor={menu.anchor} onClose={menu.close} align="right" width={180} header={t("inbox-inboxlist.grouping")}
              items={(["source", "time"] as const).map((g) => ({
                key: g,
                label: g === "source" ? t("inbox-inboxlist.by_source") : t("inbox-inboxlist.by_time"),
                radio: true,
                checked: grouping === g,
                onSelect: () => onGrouping(g),
              }))} />
          )}
        </>
      )}
    </div>
  );
}
