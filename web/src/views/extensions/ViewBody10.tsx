// UI version 10 (#9727, docs/extensions/extensions.md "UI version 10"): a view's `graph` drawn as an interactive
// canvas — nodes as DOM buttons on a transformed layer, the edges in one SVG behind them, group frames, fold
// controls, the selected node's ring; drag and wheel pan, pinch and Ctrl/Cmd + wheel and the buttons zoom (25%–200%),
// Fit, and a Full screen overlay with the same canvas. A chunk of its own, loaded with the first view that has a
// graph (ViewBody.tsx), never the first screen or the idle preload. The layout is lib/graph-layout.ts, the shared
// rules in slots; this file only multiplies by the web's node pitch and draws.
import { useLayoutEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { t } from "../../i18n";
import { layOutGraph } from "../../lib/graph-layout";
import { toneOf, type UIAction, type UIItem, type UIGraphNode } from "../../lib/extension-ui";
import { hue } from "../../lib/extension-ui-v6";
import { useCloseOnBack } from "../../lib/back-close";
import { cx, EASE, IconButton } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { useEscapeClose, useModalFocus } from "../../ui/overlay";
import { NARROW, useMedia } from "../../ui/media-query";
import { Badge, gaugePercent, glyphOf } from "./ViewBody6";
import { ROW_ICON } from "./ViewBody";

// The web's node pitch: a node is the graph node token's size (about 224 × 64 at the default text size,
// docs/design/DESIGN_SYSTEM.md, the graph of "UI version 10" in docs/extensions/extensions.md); a slot is a node
// and its gap on each axis.
const NODE_W = 224;
const NODE_H = 64;
const PITCH_X = 272;
const PITCH_Y = 92;
// The layer's own margin, so a group's frame and its title never touch the edge.
const MARGIN = 32;
// What a fitted view keeps free around the drawing.
const INSET = 40;
// A group's frame reaches this far above its members (its title) and around them.
const FRAME_PAD = 12;
const CAPTION = 28;
const MIN_ZOOM = 0.25;
const MAX_ZOOM = 2;
const STEP = 1.25;

const clamp = (v: number, min: number, max: number) => Math.min(max, Math.max(min, v));

/** A node's tone (a role color) as one of the named hues the web's `--hue-*` colors are. */
const TONE_HUE: Record<string, string> = { positive: "green", caution: "orange", danger: "red" };
const nodeColor = (n: UIGraphNode): string | undefined => (n.color ? hue(n.color) : n.tone ? hue(TONE_HUE[toneOf(n.tone)]) : undefined);

interface CanvasView { x: number; y: number; zoom: number }

/** What a `key` keeps across redraws (docs: the person's pan, zoom and folding; a re-fit only while untouched). */
interface GraphStore { view: CanvasView; moved: boolean; folds: Set<string> | null }

/** Every canvas's persistence, by the graph's `key` ("" for one without). */
const STORES = new Map<string, GraphStore>();
const storeFor = (key: string): GraphStore => {
  let s = STORES.get(key);
  if (!s) STORES.set(key, (s = { view: { x: 0, y: 0, zoom: 1 }, moved: false, folds: null }));
  return s;
};
/** Forgets what every `key` kept (a test's clean slate; the page never calls it). */
export const forgetGraphStores = () => STORES.clear();

const KEY_PAN: Readonly<Record<string, [number, number]>> = {
  ArrowLeft: [48, 0], ArrowRight: [-48, 0], ArrowUp: [0, 48], ArrowDown: [0, -48],
};

/** A node's label, as the doc reads it: the title, then the badge, subtitle, progress, then "under <parent>". */
function nodeLabel(n: UIGraphNode, parentTitle: string | undefined): string {
  const parts = [n.title, n.badge?.text, n.subtitle, n.progress !== undefined ? `${gaugePercent(n.progress)}%` : ""].filter(Boolean);
  if (parentTitle) parts.push(t("extensions-viewbody.graph_under", { title: parentTitle }));
  return parts.join(t("extensions-viewbody.read_sep"));
}

/**
 * A view's `graph` (UI version 10): the canvas. On a page it is 70% of the page's visible height (styles.css
 * `graph-canvas`) and the page scrolls around it; `compact` (a card, an overview or an assistant view) draws the
 * still fitted preview (`graph-preview`, 200 px) with no buttons, gestures or node presses — pressing it opens the
 * Full screen overlay, which draws the same canvas over the window with its position, zoom and folds.
 */
export function GraphCanvas({ item, compact, busy, onPress, canPress }: {
  item: UIItem;
  compact?: boolean;
  busy: string;
  onPress: (a: UIAction, destructive?: boolean) => void;
  canPress?: (a: UIAction) => boolean;
}) {
  const storeKey = item.key ?? "";
  // The person's folds for this `key`, kept across redraws (the store); null until they fold or unfold one, when the
  // view's `folded` applies, redraws included.
  const [personFolds, setPersonFolds] = useState<Set<string> | null>(() => storeFor(storeKey).folds);
  const folds = personFolds ?? new Set(item.folded ?? []);
  const [full, setFull] = useState(false);
  const stage = useRef<HTMLDivElement>(null);
  const layer = useRef<HTMLDivElement>(null);
  const dialog = useRef<HTMLDivElement>(null);
  const view = useRef<CanvasView>({ x: 0, y: 0, zoom: 1 });
  // The person has moved this key's canvas (kept in the store, so a redraw does not mistake it for untouched).
  const moved = useRef(false);
  const controls = useRef<{ zoom: (factor: number) => void; fit: () => void } | null>(null);
  useModalFocus(dialog, full);
  useEscapeClose(full, () => setFull(false));
  useCloseOnBack(full, () => setFull(false), useMedia(NARROW));

  // The layout follows from the nodes, edges and folds alone, so a redraw that changes only states keeps every node
  // in place (spec/fixtures/graph-layout/README.md).
  const laid = useMemo(
    () => layOutGraph({ nodes: item.nodes ?? [], edges: item.edges, layout: item.layout, direction: item.direction }, [...folds]),
    [item.nodes, item.edges, item.layout, item.direction, folds],
  );
  const byId = useMemo(() => Object.fromEntries((item.nodes ?? []).map((n) => [n.id, n])), [item.nodes]);
  const down = item.direction === "down";
  const lw = (laid.size[0] - 1) * PITCH_X + NODE_W + 2 * MARGIN;
  const lh = (laid.size[1] - 1) * PITCH_Y + NODE_H + 2 * MARGIN;

  // A redraw: while the person has not moved the canvas it takes the opening view again; their folds, once made,
  // override the view's `folded` from then on.
  const toggleFold = (id: string) => {
    const next = new Set(folds);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    storeFor(storeKey).folds = next; // the person's choice, kept for this key
    setPersonFolds(next);
  };

  // The parent each node is read "under": the first edge that names it as a child, in the view's order.
  const parentTitle = useMemo(() => {
    const out: Record<string, string | undefined> = {};
    for (const [from, to] of laid.edges) if (!out[to]) out[to] = byId[from]?.title;
    return out;
  }, [laid, byId]);

  /** The node's place in the layer: slots × the web's pitch, plus the layer's margin. */
  const at = (id: string): [number, number] => (laid.at[id] ? [laid.at[id][0] * PITCH_X + MARGIN, laid.at[id][1] * PITCH_Y + MARGIN] : [0, 0]);
  const sel = item.selected && laid.at[item.selected] ? item.selected : undefined;

  useLayoutEffect(() => {
    const area = stage.current;
    const pane = layer.current;
    if (!area || !pane) return;
    const size = () => ({ w: area.clientWidth, h: area.clientHeight });
    // What the store kept for this key: the person's view, and whether they moved it (a redraw keeps both).
    const kept = storeFor(storeKey);
    moved.current = kept.moved;
    if (kept.moved) view.current = kept.view;

    const apply = (v: CanvasView) => {
      view.current = v;
      pane.style.transform = `translate(${v.x}px, ${v.y}px) scale(${v.zoom})`;
      const s = storeFor(storeKey);
      s.view = v;
      s.moved = moved.current;
    };

    // Keep some of the drawing in reach on each axis; a drawing that fits an axis stays centred on it.
    const clampPan = (v: CanvasView): CanvasView => {
      const { w, h } = size();
      const cw = lw * v.zoom;
      const ch = lh * v.zoom;
      return { zoom: v.zoom, x: cw <= w ? (w - cw) / 2 : clamp(v.x, INSET - cw, w - INSET), y: ch <= h ? (h - ch) / 2 : clamp(v.y, INSET - ch, h - INSET) };
    };

    const panBy = (dx: number, dy: number) => {
      const v = view.current;
      moved.current = true;
      apply(clampPan({ x: v.x + dx, y: v.y + dy, zoom: v.zoom }));
    };

    const zoomAt = (factor: number, cx: number, cy: number) => {
      const v = view.current;
      const zoom = clamp(v.zoom * factor, MIN_ZOOM, MAX_ZOOM);
      if (zoom === v.zoom) return;
      const ratio = zoom / v.zoom;
      moved.current = true;
      apply(clampPan({ x: cx - (cx - v.x) * ratio, y: cy - (cy - v.y) * ratio, zoom }));
    };

    // It opens at the fitted scale held to 50%–100%, centred on `selected`, else with the first root at the top start.
    const open = () => {
      const { w, h } = size();
      if (!w || !h) return;
      const fit = Math.min((w - 2 * INSET) / lw, (h - 2 * INSET) / lh);
      const zoom = clamp(fit, 0.5, 1);
      if (sel) {
        const [sx, sy] = at(sel);
        apply(clampPan({ x: w / 2 - (sx + NODE_W / 2) * zoom, y: h / 2 - (sy + NODE_H / 2) * zoom, zoom }));
      } else {
        apply(clampPan({ x: INSET - MARGIN * zoom, y: INSET - MARGIN * zoom, zoom }));
      }
    };

    // Fit shows everything, down to 25% (a compact preview is this, always).
    const fit = () => {
      const { w, h } = size();
      if (!w || !h) return;
      const zoom = clamp(Math.min((w - 2 * INSET) / lw, (h - 2 * INSET) / lh), MIN_ZOOM, 1);
      apply({ x: (w - lw * zoom) / 2, y: (h - lh * zoom) / 2, zoom });
    };

    const settled = () => {
      if (!moved.current) open();
      else apply(clampPan(view.current));
    };

    const local = (e: PointerEvent | WheelEvent): { x: number; y: number } => {
      const r = area.getBoundingClientRect();
      return { x: e.clientX - r.left, y: e.clientY - r.top };
    };
    const stop = (e: Event) => e.stopPropagation();

    const wheel = (e: WheelEvent) => {
      e.preventDefault(); // the canvas is a pan surface: the wheel never scrolls the page past it
      const unit = e.deltaMode === 1 ? 16 : e.deltaMode === 2 ? area.clientHeight : 1;
      if (e.ctrlKey || e.metaKey) {
        const p = local(e);
        zoomAt(Math.exp(clamp(-e.deltaY * unit * 0.004, -1, 1)), p.x, p.y);
      } else {
        panBy(-e.deltaX * unit, -e.deltaY * unit);
      }
    };

    // Drag pans; two pointers pinch around their centre (the point between them stays put). A press that starts on a
    // node (or any control) stays that control's press — the drag takes over only once the pointer moves 4 px, and
    // dragging never presses the node under it.
    const points = new Map<number, { x: number; y: number }>();
    const gesture = (ps: Map<number, { x: number; y: number }>) => {
      const [a, b] = ps.values();
      if (!a) return undefined;
      if (!b) return { cx: a.x, cy: a.y, d: 0 };
      return { cx: (a.x + b.x) / 2, cy: (a.y + b.y) / 2, d: Math.hypot(a.x - b.x, a.y - b.y) };
    };
    const down = (e: PointerEvent) => {
      if (e.button !== 0 || points.size >= 2) return;
      points.set(e.pointerId, local(e));
    };
    const move = (e: PointerEvent) => {
      const p = points.get(e.pointerId);
      if (!p) return;
      const at = local(e);
      const onControl = (e.target as Element | null)?.closest("button, a, input, select, textarea") ?? null;
      const dragging = area.hasPointerCapture(e.pointerId);
      // a control's own press stays its own until the pointer really moves (4 px) and the drag takes it over
      if (!dragging && onControl && Math.hypot(at.x - p.x, at.y - p.y) < 4) return;
      if (!dragging) {
        area.setPointerCapture(e.pointerId);
        if (onControl) onControl.addEventListener("click", stop, { capture: true, once: true }); // the drag begins on a control: swallow its click rather than press it as well
      }
      const before = gesture(points)!;
      points.set(e.pointerId, at);
      const after = gesture(points)!;
      if (before.d > 0 && after.d > 0) {
        // the point between the fingers stays put: zoom about it, then carry it with the fingers
        zoomAt(after.d / before.d, before.cx, before.cy);
      }
      panBy(after.cx - before.cx, after.cy - before.cy);
    };
    const end = (e: PointerEvent) => points.delete(e.pointerId);

    // With the canvas focused (not a node of it): the arrows pan, + / - zoom, 0 fits.
    const key = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey || e.target !== area) return;
      const pan = KEY_PAN[e.key];
      const { w, h } = size();
      if (pan) (e.preventDefault(), panBy(pan[0], pan[1]));
      else if (e.key === "+" || e.key === "=") (e.preventDefault(), zoomAt(STEP, w / 2, h / 2));
      else if (e.key === "-" || e.key === "_") (e.preventDefault(), zoomAt(1 / STEP, w / 2, h / 2));
      else if (e.key === "0") (e.preventDefault(), fit());
    };

    controls.current = {
      zoom: (factor: number) => zoomAt(factor, area.clientWidth / 2, area.clientHeight / 2),
      fit,
    };
    if (compact) fit();
    else settled();
    const resized = () => settled();
    const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(resized);
    observer?.observe(area);
    if (!compact) {
      area.addEventListener("wheel", wheel, { passive: false });
      area.addEventListener("pointerdown", down);
      area.addEventListener("pointermove", move);
      area.addEventListener("pointerup", end);
      area.addEventListener("pointercancel", end);
      area.addEventListener("lostpointercapture", end);
      area.addEventListener("keydown", key);
    }
    return () => {
      observer?.disconnect();
      controls.current = null;
      if (!compact) {
        area.removeEventListener("wheel", wheel);
        area.removeEventListener("pointerdown", down);
        area.removeEventListener("pointermove", move);
        area.removeEventListener("pointerup", end);
        area.removeEventListener("pointercancel", end);
        area.removeEventListener("lostpointercapture", end);
        area.removeEventListener("keydown", key);
      }
    };
    // laid changes when the view is drawn again or a fold is toggled; the person's view survives it in `view`.
  }, [laid, compact, full, storeKey]);

  // A node the keyboard focuses is panned into view (it does not count as the person moving the canvas).
  const reveal = (id: string) => {
    const area = stage.current;
    const pane = layer.current;
    if (!area || !pane) return;
    const w = area.clientWidth;
    const h = area.clientHeight;
    if (!w || !h) return;
    const v = view.current;
    const [nx, ny] = at(id);
    const sx = v.x + nx * v.zoom;
    const sy = v.y + ny * v.zoom;
    const nw = NODE_W * v.zoom;
    const nh = NODE_H * v.zoom;
    const dx = sx < 12 ? 12 - sx : sx + nw > w - 12 ? w - 12 - nw - sx : 0;
    const dy = sy < 12 ? 12 - sy : sy + nh > h - 12 ? h - 12 - nh - sy : 0;
    if (dx || dy) {
      view.current = { x: v.x + dx, y: v.y + dy, zoom: v.zoom };
      pane.style.transform = `translate(${view.current.x}px, ${view.current.y}px) scale(${view.current.zoom})`;
    }
  };

  // One node: a card of the node token's size — the tone or color as a bar on the leading edge and a state dot,
  // the icon at the start, the title one line with the badge at its end, the subtitle under, progress along the foot.
  const Node = ({ id, compact: still }: { id: string; compact?: boolean }) => {
    const n = byId[id] as UIGraphNode;
    const [x, y] = at(id);
    const color = nodeColor(n);
    const glyph = glyphOf(n.icon, ROW_ICON);
    const label = nodeLabel(n, parentTitle[id]);
    const act = still ? undefined : n.action; // the compact preview presses nothing: it opens Full screen
    const pressable = !!act && (canPress ? canPress(act) : true);
    const body = (
      <>
        {color && <span aria-hidden className="absolute inset-y-0 left-0 w-[3px] bg-current" style={{ color }} />}
        <span aria-hidden className={cx("flex items-center gap-2", color && "pl-1.5")}>
          {glyph && <Icon name={glyph} size={14} className="shrink-0 text-ink-3" />}
        </span>
        <span className={cx("flex min-w-0 flex-1 flex-col justify-center gap-0.5 py-1.5", color && "pl-1")}>
          <span className="flex min-w-0 items-center gap-1.5">
            {color && <span aria-hidden className="size-1.5 shrink-0 rounded-full bg-current" style={{ color }} />}
            <span className="min-w-0 flex-1 truncate text-meta font-medium text-ink" title={n.title}>{n.title}</span>
            {n.badge && <Badge badge={n.badge} />}
          </span>
          {n.subtitle && <span className="min-w-0 truncate text-submeta text-ink-3" title={n.subtitle}>{n.subtitle}</span>}
        </span>
        {n.progress !== undefined && (
          <span aria-hidden className="absolute inset-x-0 bottom-0 h-[3px]">
            <span
              className="h-full rounded-r-full transition-[width] duration-500"
              style={{ width: `${gaugePercent(n.progress)}%`, background: color ?? "var(--accent)", transitionTimingFunction: EASE }}
            />
          </span>
        )}
      </>
    );
    return (
      <div role="listitem" data-graph-at={id} className="absolute" style={{ left: x, top: y, width: NODE_W, height: NODE_H }}>
        {act ? (
          <button
            type="button"
            data-graph-node={id}
            aria-label={label}
            title={label}
            disabled={!!busy || !pressable}
            onFocus={() => reveal(id)}
            onClick={() => onPress(act)}
            className={cx(
              "group flex h-full w-full items-center overflow-hidden rounded-card border border-line bg-surface pl-2.5 pr-2 text-left shadow-card",
              "transition-colors duration-300 enabled:hover:bg-hover focus-visible:z-10 disabled:opacity-60",
            )}
            style={{ animation: `fade-in 200ms ${EASE} both` }}
          >
            {body}
          </button>
        ) : (
          <div
            data-graph-node={id}
            aria-label={label}
            title={label}
            className="flex h-full w-full items-center overflow-hidden rounded-card border border-line bg-surface pl-2.5 pr-2 shadow-card transition-colors duration-300"
            style={{ animation: `fade-in 200ms ${EASE} both` }}
          >
            {body}
          </div>
        )}
      </div>
    );
  };

  // The fold control: its own control on the side where the node's children leave it, never a press of the node.
  const Fold = ({ id }: { id: string }) => {
    const [x, y] = at(id);
    const folded = folds.has(id);
    const hidden = laid.folds[id];
    const label = folded ? t("extensions-viewbody.graph_show_hidden", { count: hidden }) : t("extensions-viewbody.graph_fold");
    return (
      <button
        type="button"
        data-graph-fold={id}
        aria-label={label}
        aria-expanded={folded}
        title={label}
        onClick={() => toggleFold(id)}
        className={cx(
          "touch-hit absolute z-[1] flex size-6 items-center justify-center rounded-full border border-line bg-surface text-ink-3 shadow-hairline",
          "transition-transform duration-150 hover:text-ink",
        )}
        style={down ? { left: x + NODE_W / 2 - 12, top: y + NODE_H - 13 } : { left: x + NODE_W - 13, top: y + NODE_H / 2 - 12 }}
      >
        {folded ? (
          <span className="text-submeta leading-none font-medium tabular-nums">+{hidden}</span>
        ) : (
          <Icon name={down ? "chevron" : "chevronRight"} size={11} stroke={2.4} />
        )}
      </button>
    );
  };

  const canvas = (
    <div
      ref={stage}
      role="group"
      aria-label={t("extensions-viewbody.graph")}
      tabIndex={compact ? -1 : 0}
      data-graph-stage=""
      className={cx(
        "relative h-full w-full touch-none overflow-hidden overscroll-none select-none rounded-card focus-visible:outline-2 focus-visible:outline-accent",
        !compact && "cursor-grab active:cursor-grabbing",
      )}
    >
      <div ref={layer} data-graph-layer="" className="absolute top-0 left-0 origin-top-left" style={{ width: lw, height: lh }}>
        {/* the edges, thin curves in the quiet ink, behind everything */}
        <svg aria-hidden className="absolute top-0 left-0 text-ink-3" width={lw} height={lh} viewBox={`0 0 ${lw} ${lh}`}>
          {laid.edges.map(([from, to], i) => {
            const [fx, fy] = at(from);
            const [tx, ty] = at(to);
            const [sx, sy, ex, ey] = down
              ? [fx + NODE_W / 2, fy + NODE_H, tx + NODE_W / 2, ty]
              : [fx + NODE_W, fy + NODE_H / 2, tx, ty + NODE_H / 2];
            const k = Math.max(24, down ? (ey - sy) / 2 : (ex - sx) / 2);
            return (
              <path
                key={i}
                data-graph-edge={`${from} ${to}`}
                d={down ? `M${sx} ${sy} C${sx} ${sy + k}, ${ex} ${ey - k}, ${ex} ${ey}` : `M${sx} ${sy} C${sx + k} ${sy}, ${ex - k} ${ey}, ${ex} ${ey}`}
                fill="none"
                stroke="currentColor"
                strokeWidth={1}
              />
            );
          })}
        </svg>
        {/* a group's frame: a quiet fill behind its members with its title above */}
        {laid.frames.map((f, i) => {
          const xs = f.members.map((m) => at(m)[0]);
          const ys = f.members.map((m) => at(m)[1]);
          const x = Math.min(...xs) - FRAME_PAD;
          const y = Math.min(...ys) - CAPTION;
          const w = Math.max(...xs) + NODE_W - Math.min(...xs) + 2 * FRAME_PAD;
          const h = Math.max(...ys) + NODE_H - Math.min(...ys) + FRAME_PAD + CAPTION;
          return (
            <div key={i} data-graph-frame={f.group} aria-hidden className="absolute rounded-card bg-inset" style={{ left: x, top: y, width: w, height: h }}>
              <span className="absolute top-[6px] left-[12px] text-submeta text-ink-3">{f.group}</span>
            </div>
          );
        })}
        {sel && (() => {
          const [x, y] = at(sel);
          return <span aria-hidden data-graph-selected="" className="pointer-events-none absolute z-[1] rounded-[12px] shadow-[0_0_0_2px_var(--accent)]" style={{ left: x - 2, top: y - 2, width: NODE_W + 4, height: NODE_H + 4 }} />;
        })()}
        {/* the canvas reads as a list of its visible nodes in reading order; each listitem wraps its node */}
        <div role="list" className="absolute top-0 left-0">
          {laid.order.map((id) => (
            <Node key={id} id={id} compact={compact} />
          ))}
        </div>
        {/* a fold control never shows in the compact preview: no presses there */}
        {!compact && laid.order.filter((id) => laid.folds[id] !== undefined).map((id) => (
          <Fold key={id} id={id} />
        ))}
      </div>
    </div>
  );

  const buttons = (
    <div className={cx("absolute right-3 bottom-3 z-[2] flex items-center gap-0.5 rounded-full border border-line bg-surface/90 p-1 shadow-card backdrop-blur")}>
      <IconButton icon="minus" label={t("extensions-viewbody.graph_zoom_out")} size={32} onClick={() => controls.current?.zoom(1 / STEP)} />
      <IconButton icon="plus" label={t("extensions-viewbody.graph_zoom_in")} size={32} onClick={() => controls.current?.zoom(STEP)} />
      <IconButton icon="external" label={t("extensions-viewbody.graph_fit")} size={32} onClick={() => controls.current?.fit()} />
      {!full && <IconButton icon="expand" label={t("extensions-viewbody.graph_full_screen")} size={32} onClick={() => setFull(true)} />}
    </div>
  );

  if (full) {
    return createPortal(
      <div
        ref={dialog}
        role="dialog"
        aria-modal="true"
        aria-label={t("extensions-viewbody.graph")}
        className="fixed inset-0 z-50 flex flex-col p-2 outline-none sm:p-4" style={{ background: "var(--page)" }}
      >
        <div className="flex shrink-0 justify-end">
          <IconButton icon="x" label={t("extensions-viewbody.graph_close")} size={36} onClick={() => setFull(false)} />
        </div>
        <div className="relative min-h-0 flex-1">
          {canvas}
          {buttons}
        </div>
      </div>,
      document.body,
    );
  }
  if (compact) {
    return (
      <div className="px-4 py-3">
        <button
          type="button"
          aria-label={t("extensions-viewbody.graph_full_screen")}
          title={t("extensions-viewbody.graph_full_screen")}
          onClick={() => setFull(true)}
          className="graph-preview block w-full cursor-zoom-in overflow-hidden rounded-card"
        >
          {canvas}
        </button>
      </div>
    );
  }
  return (
    <div className="graph-canvas relative w-full">
      {canvas}
      {buttons}
    </div>
  );
}
