"use client"

import { useState } from "react"
import dynamic from "next/dynamic"
import { useAuth } from "../lib/auth"
import { usePref } from "../lib/use-pref"
import type { TourConfig } from "./TourOverlay"

// The overlay loads only when a tour is due, so everyone else never downloads it.
const TourOverlay = dynamic(() => import("./TourOverlay").then((m) => m.TourOverlay), { ssr: false })

// A first-time real account is one made in the last quarter hour, tour never run.
const NEW_ACCOUNT_MS = 15 * 60 * 1000

export function Tour(config: TourConfig) {
  const { me, isDemo } = useAuth()
  const pref = usePref("tour")
  const [opened] = useState(() => Date.now())
  const created = Date.parse(me?.user?.created_at || "")
  const wanted = !!me && (pref === "on" || pref === "go" || (pref === "auto" && !isDemo && opened - created < NEW_ACCOUNT_MS))
  // A new sandbox is a new user, so Replay remounts the overlay at step one.
  return wanted ? <TourOverlay key={me?.user?.id} {...config} /> : null
}
