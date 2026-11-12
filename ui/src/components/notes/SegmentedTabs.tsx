"use client"

import { useLayoutEffect, useRef, useState } from "react"
import { cn } from "../../lib/utils"

// One connected control with a bubble that slides to the picked tab. The
// count beside each label reads strongest on the active one.
export function SegmentedTabs<T extends string>({
  value,
  options,
  onChange,
  label,
}: {
  value: T
  options: { value: T; label: string; count?: number }[]
  onChange: (v: T) => void
  label: string
}) {
  const ref = useRef<HTMLDivElement>(null)
  const [pill, setPill] = useState<{ left: number; width: number } | null>(null)

  useLayoutEffect(() => {
    const box = ref.current
    if (!box) return
    const place = () => {
      const el = box.querySelector<HTMLElement>(`[data-value="${value}"]`)
      if (el) setPill({ left: el.offsetLeft, width: el.offsetWidth })
    }
    place()
    const ro = new ResizeObserver(place)
    ro.observe(box)
    return () => ro.disconnect()
  }, [value])

  return (
    <div ref={ref} role="tablist" aria-label={label} className="relative inline-flex max-w-full overflow-x-auto overscroll-x-contain rounded-full [scrollbar-width:none] [&::-webkit-scrollbar]:hidden bg-muted p-0.5">
      {pill && (
        <span
          aria-hidden
          className="absolute inset-y-0.5 rounded-full bg-background shadow-sm ring-1 ring-border transition-[left,width] duration-300 ease-[cubic-bezier(.3,1.4,.5,1)] motion-reduce:transition-none in-data-[motion=reduced]:transition-none"
          style={{ left: pill.left, width: pill.width }}
        />
      )}
      {options.map((o) => {
        const on = o.value === value
        return (
          <button
            key={o.value}
            type="button"
            role="tab"
            aria-selected={on}
            data-value={o.value}
            onClick={() => onChange(o.value)}
            className={cn(
              "relative z-10 flex shrink-0 items-center gap-1.5 rounded-full px-3 py-1 text-xs font-medium outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring",
              on ? "text-foreground" : "text-muted-foreground hover:text-foreground",
            )}
          >
            {o.label}
            {!!o.count && (
              <span className={cn("rounded-full px-1.5 text-[10px] leading-4 tabular-nums", on ? "bg-foreground text-background" : "bg-background/70")}>{o.count}</span>
            )}
          </button>
        )
      })}
    </div>
  )
}
