import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import type { LinkType, TaskLink } from "../mcpApi";

// DependencyOverlay draws typed connector lines between task cards, on an SVG
// layer absolutely positioned over the board's columns container. It reads each
// card's live rect from the DOM (cards carry data-task-id) and recomputes the
// paths on scroll, resize, and whenever the card set or links change — so the
// lines track the cards without the overlay owning layout.
//
// Link-type treatment:
//   blocks / depends_on  -> solid, directional (arrowhead at the target)
//   blocked_by           -> solid, directional, drawn toward the blocker
//   related              -> dashed, no arrowhead (symmetric relationship)
// Colors come from theme CSS variables (see styles.css), never fixed hex, so
// the overlay reads correctly in light and dark themes.

type Pt = { x: number; y: number };
type Segment = {
  key: string;
  type: LinkType;
  from: Pt;
  to: Pt;
  path: string;
  directional: boolean;
};

// The container the overlay measures against. We pass the board-columns element
// so coordinates are relative to it (position: relative on that element).
export function DependencyOverlay({
  containerRef,
  links,
  visible,
}: {
  containerRef: React.RefObject<HTMLElement | null>;
  links: TaskLink[];
  visible: boolean;
}) {
  const [size, setSize] = useState<{ w: number; h: number }>({ w: 0, h: 0 });
  const [segments, setSegments] = useState<Segment[]>([]);
  const rafRef = useRef<number | null>(null);

  const recompute = useCallback(() => {
    const container = containerRef.current;
    if (!container) return;
    const base = container.getBoundingClientRect();
    setSize({ w: base.width, h: base.height });

    // Find each card's center-ish anchor relative to the container.
    const anchorOf = (id: number): DOMRect | null => {
      const el = container.querySelector<HTMLElement>(
        `[data-task-id="${id}"]`,
      );
      return el ? el.getBoundingClientRect() : null;
    };

    const segs: Segment[] = [];
    for (const link of links) {
      const a = anchorOf(link.from);
      const b = anchorOf(link.to);
      if (!a || !b) continue; // a card not currently rendered

      // Anchor on the nearest horizontal edges so the line runs card-to-card
      // across columns. If source is left of target, exit source's right edge
      // and enter target's left edge; otherwise mirror.
      const srcRight = a.left + a.width <= b.left;
      const from: Pt = {
        x: (srcRight ? a.right : a.left) - base.left,
        y: a.top + a.height / 2 - base.top,
      };
      const to: Pt = {
        x: (srcRight ? b.left : b.right) - base.left,
        y: b.top + b.height / 2 - base.top,
      };

      // A horizontal cubic Bézier: control points pushed out along x for a
      // smooth S-curve that doesn't cut through intervening cards too harshly.
      const dx = Math.max(40, Math.abs(to.x - from.x) * 0.5);
      const c1: Pt = { x: from.x + (srcRight ? dx : -dx), y: from.y };
      const c2: Pt = { x: to.x + (srcRight ? -dx : dx), y: to.y };
      const path = `M ${from.x} ${from.y} C ${c1.x} ${c1.y}, ${c2.x} ${c2.y}, ${to.x} ${to.y}`;

      segs.push({
        key: `${link.from}-${link.type}-${link.to}`,
        type: link.type,
        from,
        to,
        path,
        directional: link.type !== "related",
      });
    }
    setSegments(segs);
  }, [containerRef, links]);

  // Recompute on mount, when links change, and on container resize/scroll.
  useLayoutEffect(() => {
    recompute();
  }, [recompute]);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    const schedule = () => {
      if (rafRef.current != null) return;
      rafRef.current = requestAnimationFrame(() => {
        rafRef.current = null;
        recompute();
      });
    };

    const ro = new ResizeObserver(schedule);
    ro.observe(container);
    // Observe each card too, so a card entering/leaving or changing height
    // retriggers a redraw.
    container
      .querySelectorAll("[data-task-id]")
      .forEach((el) => ro.observe(el));

    window.addEventListener("resize", schedule);
    window.addEventListener("scroll", schedule, true);
    // Columns can scroll internally.
    const scrollers = container.querySelectorAll(".board-column-cards");
    scrollers.forEach((el) => el.addEventListener("scroll", schedule));

    return () => {
      ro.disconnect();
      window.removeEventListener("resize", schedule);
      window.removeEventListener("scroll", schedule, true);
      scrollers.forEach((el) => el.removeEventListener("scroll", schedule));
      if (rafRef.current != null) cancelAnimationFrame(rafRef.current);
    };
  }, [containerRef, recompute, links]);

  if (!visible || segments.length === 0 || size.w === 0) return null;

  return (
    <svg
      className="dep-overlay"
      width={size.w}
      height={size.h}
      viewBox={`0 0 ${size.w} ${size.h}`}
      aria-hidden="true"
    >
      <defs>
        <marker
          id="dep-arrow-dep"
          viewBox="0 0 10 10"
          refX="9"
          refY="5"
          markerWidth="7"
          markerHeight="7"
          orient="auto-start-reverse"
        >
          <path d="M 0 0 L 10 5 L 0 10 z" className="dep-arrowhead dep-dep" />
        </marker>
        <marker
          id="dep-arrow-block"
          viewBox="0 0 10 10"
          refX="9"
          refY="5"
          markerWidth="7"
          markerHeight="7"
          orient="auto-start-reverse"
        >
          <path d="M 0 0 L 10 5 L 0 10 z" className="dep-arrowhead dep-block" />
        </marker>
      </defs>
      {segments.map((s) => (
        <path
          key={s.key}
          d={s.path}
          className={`dep-line dep-${s.type}`}
          markerEnd={
            s.directional
              ? s.type === "related"
                ? undefined
                : s.type === "blocks" || s.type === "blocked_by"
                  ? "url(#dep-arrow-block)"
                  : "url(#dep-arrow-dep)"
              : undefined
          }
        />
      ))}
    </svg>
  );
}
