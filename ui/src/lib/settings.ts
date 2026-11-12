import { storageKey } from "./brand"

// Per-browser preferences in localStorage, one key each. The platform owns
// the ones below; an app adds its own with registerPrefs() at module scope.
export type TextSize = "small" | "default" | "large"

const SIZES: Record<TextSize, string> = { small: "14px", default: "16px", large: "18px" }

const DEFAULTS: Record<string, string> = {
  textSize: "default",
  motion: "full",
  textFx: "notes",
  vimKeys: "off",
  searchSyntax: "on",
  // The guided tour: "auto" runs it for a new account, "on" starts it (a demo sandbox, Replay), "off" once it is done or skipped.
  // ponytail: per browser; a server-side flag if it should follow the account across devices.
  tour: "auto",
}
export type Pref = string

// Adds the app's preferences and their defaults. A key the platform already
// has keeps the platform's meaning but takes the app's default.
export function registerPrefs(defaults: Record<string, string>) {
  Object.assign(DEFAULTS, defaults)
}

function read(key: Pref) {
  // Server rendering has no localStorage: fall back to the default.
  if (typeof localStorage === "undefined") return DEFAULTS[key] ?? ""
  return localStorage.getItem(storageKey(key)) || (DEFAULTS[key] ?? "")
}

export function getPref<T extends string>(key: Pref) {
  return read(key) as T
}

// Everything that reads a preference while rendering subscribes here, so a
// change in Settings shows at once instead of after a reload. Another tab's
// change arrives as a storage event.
const listeners = new Set<() => void>()

export function subscribePrefs(cb: () => void) {
  listeners.add(cb)
  const onStorage = (e: StorageEvent) => {
    if (!e.key?.startsWith(storageKey(""))) return
    applyAppearance()
    cb()
  }
  window.addEventListener("storage", onStorage)
  return () => {
    listeners.delete(cb)
    window.removeEventListener("storage", onStorage)
  }
}

// The default a preference has before anything is stored, for server renders.
export function defaultPref<T extends string>(key: Pref) {
  return (DEFAULTS[key] ?? "") as T
}

export function setPref(key: Pref, value: string) {
  if (value === DEFAULTS[key]) localStorage.removeItem(storageKey(key))
  else localStorage.setItem(storageKey(key), value)
  applyAppearance()
  listeners.forEach((l) => l())
}

// Reflect the appearance preferences onto <html>; theme.css reads these.
export function applyAppearance() {
  const root = document.documentElement
  root.dataset.motion = read("motion")
  root.style.fontSize = SIZES[read("textSize") as TextSize] || SIZES.default
}
