"use client"

import { useSyncExternalStore } from "react"
import { defaultPref, getPref, subscribePrefs, type Pref } from "./settings"

// A preference that re-renders its reader when it changes in Settings or in
// another tab, so a change shows at once instead of after a reload.
export function usePref<T extends string>(key: Pref): T {
  return useSyncExternalStore(
    subscribePrefs,
    () => getPref<T>(key),
    () => defaultPref<T>(key),
  )
}
