import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { errorText } from "../../lib/api";
import { stateOf } from "../../lib/format";
import { isSessionReview, SESSION_REVIEW_FEATURE } from "../../lib/types";
import { hubHas, useStore } from "../../lib/store";
import { loadSessionReview, startSessionReview, type SessionReviewStatus } from "../../lib/session-review-agent";
import type { Interaction, Session } from "../../lib/types";
import type { MenuItem } from "../../ui/menu-model";
import { toast } from "../../ui/atoms";
import { currentLanguage } from "../../i18n";
import en from "../../i18n/locales/en/chat-session-review.json";
import zhHans from "../../i18n/locales/zh-Hans/chat-session-review.json";
import ja from "../../i18n/locales/ja/chat-session-review.json";

const messages = { en, "zh-Hans": zhHans, ja } as const;
function reviewT(key: keyof typeof en, vars?: { error: string }): string {
  const selected = currentLanguage() === "zh-Hans" ? messages["zh-Hans"] : currentLanguage() === "ja" ? messages.ja : en;
  const template = selected[key] ?? en[key];
  return vars ? template.replace("{error}", vars.error) : template;
}

/** Starts a review from the synchronous fallback item, with all feedback strings in this lazy chunk. */
export async function reviewThisWork(session: Session): Promise<void> {
  const mid = session.machine_id;
  const sid = session.session_id;
  toast(reviewT("starting"), "info");
  try {
    await startSessionReview(mid, sid);
    toast(reviewT("requested"), "ok");
  } catch (e) {
    toast(reviewT("failed", { error: errorText(e) }), "error");
  }
}

/** Loads the explicit session-review menu item only when ⋯ opens, keeping its API and words off the first screen. */
export function SessionReviewMenuItem({ session, busy: starting, ix: interactions, onItem }: {
  session?: Session;
  busy: boolean;
  ix: Interaction[];
  onItem: (items: MenuItem[]) => void;
}) {
  const enabled = useStore((s) => hubHas(s.features, SESSION_REVIEW_FEATURE)) && Boolean(session);
  const [status, setStatus] = useState<SessionReviewStatus>();
  const [checking, setChecking] = useState(false);
  const [error, setError] = useState<string>();
  const [startingReview, setStartingReview] = useState(false);
  const inFlight = useRef(false);
  const mid = session?.machine_id ?? "";
  const sid = session?.session_id ?? "";
  useEffect(() => {
    if (!enabled) return;
    let live = true;
    setStatus(undefined);
    setError(undefined);
    setChecking(true);
    void loadSessionReview(mid, sid).then((next) => {
      if (live) setStatus(next);
    }).catch((e: unknown) => {
      if (live) setError(errorText(e));
    }).finally(() => {
      if (live) setChecking(false);
    });
    return () => { live = false; };
  }, [enabled, mid, sid]);
  const pendingReviews = interactions.filter(isSessionReview).length;
  const otherPending = interactions.length - pendingReviews;
  const unlistedOther = Math.max(0, (session?.pending_interactions ?? 0) - interactions.length);
  const sourceState = session ? stateOf(session) : "unknown";
  const sourceBusy = Boolean(starting || session?.starting || session?.status === "starting" || sourceState === "working" || otherPending > 0 || unlistedOther > 0 || (sourceState === "needs_you" && pendingReviews === 0));
  const detail = startingReview ? reviewT("starting")
    : sourceBusy ? reviewT("busy")
    : checking ? reviewT("checking")
    : error ? reviewT("failed", { error })
    : !status ? reviewT("checking")
    : status.running ? reviewT("running")
    : !status.has_changes ? reviewT("no_change") : undefined;
  const disabled = Boolean(detail);
  const start = useCallback(async () => {
    if (inFlight.current || disabled) return;
    inFlight.current = true;
    setStartingReview(true);
    toast(reviewT("starting"), "info");
    try {
      await startSessionReview(mid, sid);
      setStatus((current) => ({ running: true, has_changes: current?.has_changes ?? true, started_at: current?.started_at }));
      toast(reviewT("requested"), "ok");
    } catch (e) {
      toast(reviewT("failed", { error: errorText(e) }), "error");
    } finally {
      inFlight.current = false;
      setStartingReview(false);
    }
  }, [disabled, mid, sid]);
  const item = useMemo<MenuItem>(() => ({ key: "session-review", label: reviewT("this_work"), icon: "pullRequest", disabled, detail, onSelect: start }), [detail, disabled, start]);
  useLayoutEffect(() => {
    onItem(enabled ? [item] : []);
    return () => onItem([]);
  }, [enabled, item, onItem]);
  return null;
}
