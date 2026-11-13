"use client"

import { useState } from "react"
import { api } from "./api-client"
import { useAuth } from "./auth"

// `release` marks an entry as the announcement for that version, replacing the
// generated one whose id is "v" + release. `href` defaults to the changelog.
export type Announcement = { id: string; at: string; title: string; body?: string; href?: string; release?: string }

// Newest first. The sort is stable, so entries sharing a time (a release and
// its announcement, or two releases on one day) keep this order: written
// ones, then generated ones newest release first.
export function latestAnnouncement(written: Announcement[], generated: Announcement[] = []) {
  const replaced = new Set(written.map((e) => e.release))
  return [...written, ...generated.filter((r) => !replaced.has(r.id.slice(1)))].sort((a, b) => b.at.localeCompare(a.at))[0]
}

// The newest announcement while the signed-in account hasn't dismissed it.
// Dismissing saves its time on the account (PATCH /v1/me), which also covers
// everything older, on every device.
export function useAnnouncement(latest: Announcement | undefined) {
  const { me, refresh } = useAuth()
  const [gone, setGone] = useState(false)
  const due = latest && !gone && me?.user && (me.user.announcements_seen ?? "") < latest.at
  return {
    item: due ? { ...latest, href: latest.href ?? "/docs/changelog" } : null,
    dismiss: () => {
      setGone(true)
      if (!latest) return
      api("/v1/me", { method: "PATCH", body: { announcements_seen: latest.at } }).then(refresh, () => setGone(false))
    },
  }
}
