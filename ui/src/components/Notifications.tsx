"use client"

import { useState, type ReactNode } from "react"
import Link from "next/link"
import { Bell } from "lucide-react"
import { useLive, type LiveEvent } from "../lib/live"
import { cn } from "../lib/utils"
import { Button } from "./ui/button"
import { Popover, PopoverContent, PopoverTrigger } from "./ui/popover"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "./ui/tabs"

const rtf = new Intl.RelativeTimeFormat("en", { numeric: "auto", style: "short" })

export function ago(iso?: string) {
  if (!iso) return ""
  const s = (new Date(iso).getTime() - Date.now()) / 1000
  if (s > -45) return "just now"
  for (const [unit, n] of [["minute", 60], ["hour", 3600], ["day", 86400]] as const) {
    if (-s < n * (unit === "day" ? 7 : unit === "hour" ? 24 : 60)) return rtf.format(Math.round(s / n), unit)
  }
  return new Date(iso).toLocaleDateString()
}

// How the app words its events. `describe` is one line a person reads
// (`win` marks it as good news); `href` is where clicking it goes; `meta`
// is the small line under it, and defaults to who did it and when.
export type EventText = {
  describe: (ev: LiveEvent) => { text: string; win?: boolean }
  href?: (ev: LiveEvent) => string | undefined
  meta?: (ev: LiveEvent) => string
}

export const eventMeta = (ev: LiveEvent, text: EventText) => text.meta?.(ev) ?? [ev.actor?.name, ago(ev.occurred_at)].filter(Boolean).join(" · ")

// The empty state of a notifications tab. `art` goes above the line.
export function CaughtUp({ art, children }: { art?: ReactNode; children: ReactNode }) {
  return (
    <div className="flex flex-col items-center gap-2 px-6 py-8 text-center">
      {art}
      <p className="font-heading text-base font-semibold">{children}</p>
    </div>
  )
}

function Everything({ text, empty }: { text: EventText; empty: ReactNode }) {
  const { events, mine } = useLive()
  const others = events.filter((e) => !mine(e))
  if (!others.length) return <>{empty}</>
  return (
    <ul className="max-h-96 divide-y overflow-y-auto">
      {others.map((ev) => {
        const { text: line, win } = text.describe(ev)
        return (
          <li key={ev.id} className="px-4 py-3">
            <Link href={text.href?.(ev) || "#"} className="flex items-start gap-3 rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring">
              <span className={cn("mt-1.5 size-2 shrink-0 rounded-full", win ? "bg-brand-gold ring-1 ring-gold-ink" : "bg-muted-foreground/40")} aria-hidden />
              <span className="flex min-w-0 flex-1 flex-col gap-1">
                <span className="text-sm">{line}</span>
                <span className={cn("text-xs", win ? "text-gold-ink" : "text-muted-foreground")}>{eventMeta(ev, text)}</span>
              </span>
            </Link>
          </li>
        )
      })}
    </ul>
  )
}

// The bell and its popover. With `first`, the popover has two tabs: the
// app's own (what is waiting on the person, say) and Everything.
export function NotificationBell({
  text,
  first,
  empty = <CaughtUp>No news yet.</CaughtUp>,
}: {
  text: EventText
  first?: { label: string; render: (open: boolean) => ReactNode }
  empty?: ReactNode
}) {
  const { unread, markRead, connected } = useLive()
  const [open, setOpen] = useState(false)
  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        setOpen(next)
        if (!next) markRead()
      }}
    >
      <PopoverTrigger asChild>
        <Button variant="ghost" size="icon" className="relative" aria-label={unread ? `Notifications, ${unread} new` : "Notifications"}>
          <Bell />
          {unread > 0 && (
            <span className="absolute -top-0.5 -right-0.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-foreground px-1 text-[10px] font-semibold text-background">
              {unread > 9 ? "9+" : unread}
            </span>
          )}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-96 p-0">
        <div className="flex items-center gap-2 border-b px-4 py-3">
          <span className="flex-1 font-heading text-base font-semibold">Notifications</span>
          <span className="flex items-center gap-1.5 text-xs text-muted-foreground" title={connected ? "Updates arrive as they happen" : "Reconnecting"}>
            <span className={cn("size-1.5 rounded-full", connected ? "bg-foreground" : "bg-muted-foreground/40")} aria-hidden />
            {connected ? "Live" : "Offline"}
          </span>
          <Button variant="link" size="sm" className="h-auto px-0" onClick={markRead}>
            Mark all read
          </Button>
        </div>
        {first ? (
          <Tabs defaultValue="first" className="gap-0">
            <TabsList className="mx-4 mt-3">
              <TabsTrigger value="first">{first.label}</TabsTrigger>
              <TabsTrigger value="all">Everything</TabsTrigger>
            </TabsList>
            <TabsContent value="first">{first.render(open)}</TabsContent>
            <TabsContent value="all">
              <Everything text={text} empty={empty} />
            </TabsContent>
          </Tabs>
        ) : (
          <Everything text={text} empty={empty} />
        )}
      </PopoverContent>
    </Popover>
  )
}
