"use client"

// Keeps every open dashboard page current. One long poll on GET /v1/events
// (the same feed the CLI's `events --follow` reads) bumps a revision that
// views add to their fetch effects; returning to the tab bumps it too, which
// covers changes that record no event. A bump waits while you are typing, so
// a refetch never lands on a half-made edit.

import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from "react"
import { api } from "./api-client"
import { useAuth } from "./auth"
import { brand } from "./brand"
import type { Event, Page } from "./types"

// An event after the app's `enrich` has added what its views show (the
// record's title, say). The added fields ride on Event's open shape.
export type LiveEvent = Event

type Live = {
  revision: number
  connected: boolean
  events: LiveEvent[]
  unread: number
  markRead: () => void
  // An event this browser's token caused: the person already saw it happen.
  mine: (ev: Event) => boolean
}

const LiveContext = createContext<Live>({ revision: 0, connected: false, events: [], unread: 0, markRead: () => {}, mine: () => false })

export const useLive = () => useContext(LiveContext)
export const useRevision = () => useContext(LiveContext).revision

const seenKey = () => `${brand().slug}:notifications-seen`
const WAIT_MS = 25_000

function editing() {
  const el = document.activeElement
  return Boolean(el && el.matches("input:not([type=checkbox]):not([type=radio]), textarea, select, [contenteditable=true]"))
}

function readSeen() {
  try {
    return localStorage.getItem(seenKey()) || ""
  } catch {
    return ""
  }
}

export function LiveProvider({
  children,
  onEvent,
  enrich,
}: {
  children: ReactNode
  // Called once for each event that arrives after the page loaded.
  onEvent?: (ev: LiveEvent) => void
  // Adds what the app shows about each event's subject, in one round trip
  // for the batch. It must keep the order and never throw away an event.
  enrich?: (events: Event[]) => Promise<LiveEvent[]>
}) {
  const { me } = useAuth()
  const [revision, setRevision] = useState(0)
  const [connected, setConnected] = useState(false)
  const [events, setEvents] = useState<LiveEvent[]>([])
  // ponytail: read state lives in this browser's localStorage, not per account; move to the server if it must sync.
  const [seen, setSeen] = useState(() => (typeof window === "undefined" ? "" : readSeen()))
  const onEventRef = useRef(onEvent)
  onEventRef.current = onEvent
  const enrichRef = useRef(enrich)
  enrichRef.current = enrich
  const myToken = me?.token?.id

  // One refetch at a time; while a field has focus it waits for the blur.
  const pending = useRef(false)
  function bump() {
    if (!editing()) {
      setRevision((r) => r + 1)
      return
    }
    if (pending.current) return
    pending.current = true
    document.addEventListener(
      "focusout",
      () => {
        pending.current = false
        setTimeout(bump, 0)
      },
      { once: true },
    )
  }

  // A failed enrich shows the bare events rather than dropping them.
  const enriched = (list: Event[]): Promise<LiveEvent[]> => (enrichRef.current ? enrichRef.current(list).catch(() => list) : Promise.resolve(list))

  useEffect(() => {
    let stopped = false
    let after = ""

    async function loop() {
      try {
        const first = await api<Page<Event>>("/v1/events?limit=30&sort=-occurred_at")
        const recent = await enriched(first.data || [])
        if (stopped) return
        setEvents(recent)
        after = recent[0]?.id || ""
        // First visit with notifications: what already happened isn't news.
        if (!readSeen() && after) {
          setSeen(after)
          try {
            localStorage.setItem(seenKey(), after)
          } catch {
            // Private mode: the badge starts from zero on each visit.
          }
        }
        setConnected(true)
      } catch {
        setConnected(false)
      }
      while (!stopped) {
        if (document.hidden) {
          await new Promise<void>((resolve) => document.addEventListener("visibilitychange", () => resolve(), { once: true }))
          continue
        }
        try {
          const qs = new URLSearchParams({ wait_ms: String(WAIT_MS), limit: "50" })
          if (after) qs.set("after", after)
          const page = await api<Page<Event>>("/v1/events?" + qs, { timeoutMs: WAIT_MS + 10_000 })
          if (stopped) return
          setConnected(true)
          const fresh = (page.data || []).filter((e) => e.id && e.id !== after)
          if (!fresh.length) continue
          after = fresh[fresh.length - 1].id || after
          const ready = await enriched(fresh)
          setEvents((prev) => [...ready.reverse(), ...prev].slice(0, 100))
          ready.forEach((e) => onEventRef.current?.(e))
          bump()
        } catch {
          if (stopped) return
          setConnected(false)
          // A server restart or a lost network: back off, then pick up where we left off.
          await new Promise((r) => setTimeout(r, 5_000))
        }
      }
    }
    void loop()

    // Back on the tab: catch what no event records.
    let last = Date.now()
    const back = () => {
      if (document.hidden || Date.now() - last < 5_000) return
      last = Date.now()
      bump()
    }
    document.addEventListener("visibilitychange", back)
    window.addEventListener("focus", back)
    return () => {
      stopped = true
      document.removeEventListener("visibilitychange", back)
      window.removeEventListener("focus", back)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- one loop per signed-in page
  }, [])

  const mine = (ev: Event) => Boolean(myToken && ev.actor?.token_id === myToken)
  const seenAt = events.find((e) => e.id === seen)?.occurred_at || ""
  const unread = events.filter((e) => !mine(e) && (!seen || (e.occurred_at || "") > seenAt) && e.id !== seen).length

  function markRead() {
    const top = events[0]?.id || ""
    setSeen(top)
    try {
      localStorage.setItem(seenKey(), top)
    } catch {
      // Private mode: read state lasts for this page only.
    }
  }

  return <LiveContext.Provider value={{ revision, connected, events, unread, markRead, mine }}>{children}</LiveContext.Provider>
}

// One resource, fetched independently of the others on the page: a slow or
// failed call leaves its neighbours alone, and each has its own retry. It
// refetches when the live feed bumps the revision.
export function useSection<T>(path: string, enabled = true) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState(false)
  const [tick, setTick] = useState(0)
  const revision = useRevision()

  useEffect(() => {
    if (!enabled) return
    let cancelled = false
    // Resets for this fetch (a prior failure shouldn't linger through a retry).
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setError(false)
    api<T>(path)
      .then((result) => {
        if (!cancelled) setData(result)
      })
      .catch(() => {
        if (!cancelled) setError(true)
      })
    return () => {
      cancelled = true
    }
  }, [path, enabled, tick, revision])

  return { data, error, retry: () => setTick((t) => t + 1) }
}
