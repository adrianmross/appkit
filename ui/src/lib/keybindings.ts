import { useEffect, useRef, useSyncExternalStore } from "react"
import { storageKey } from "./brand"

// combo: "mod+p" (⌘ on Mac, Ctrl elsewhere), a bare key ("/"), or two steps
// separated by a space ("g h"). "|" lists alternatives ("j|ArrowDown"), and
// "ctrl+n" is Control only, where "mod+" is ⌘ on a Mac. when: "app" = no dialog open, "dialog" = a
// dialog (settings) is open, "any" = always.
export type Binding = { id: string; label: string; group: string; combo: string; when: "app" | "dialog" | "any" }

// The platform's own shortcuts. An app adds the rest with registerBindings()
// at module scope; KeyboardPanel lists them by group in this order.
export const BINDINGS: Binding[] = [
  { id: "palette", label: "Open command palette", group: "General", combo: "mod+p", when: "any" },
  { id: "search", label: "Search what you are looking at", group: "General", combo: "/", when: "any" },
  { id: "settings", label: "Open settings", group: "General", combo: "mod+,", when: "any" },
  { id: "keys", label: "Show keyboard shortcuts", group: "General", combo: "?", when: "any" },
  { id: "find", label: "Find in the audit log", group: "Settings", combo: "mod+f", when: "dialog" },
  { id: "prevSection", label: "Previous settings section", group: "Settings", combo: "[", when: "dialog" },
  { id: "nextSection", label: "Next settings section", group: "Settings", combo: "]", when: "dialog" },
]

// A binding whose id is already registered replaces it, so a hot reload
// that runs this twice changes nothing.
export function registerBindings(bindings: Binding[]) {
  for (const b of bindings) {
    const at = BINDINGS.findIndex((x) => x.id === b.id)
    if (at >= 0) BINDINGS[at] = b
    else BINDINGS.push(b)
  }
}

type Prefs = { master: boolean; off: string[] }
const SERVER_PREFS: Prefs = { master: true, off: [] }

function load(): Prefs {
  try {
    const raw = JSON.parse(localStorage.getItem(storageKey("keys")) || "{}")
    return { master: raw.master !== false, off: Array.isArray(raw.off) ? raw.off : [] }
  } catch {
    return { master: true, off: [] }
  }
}

// Read on first use, not at import: the brand may be configured after this module loads.
let loaded: Prefs | null = null
const current = () => (loaded ??= load())
const listeners = new Set<() => void>()

function save(next: Prefs) {
  loaded = next
  localStorage.setItem(storageKey("keys"), JSON.stringify(next))
  listeners.forEach((l) => l())
}

export function useKeyPrefs() {
  const now = useSyncExternalStore(
    (cb) => {
      listeners.add(cb)
      return () => void listeners.delete(cb)
    },
    current,
    () => SERVER_PREFS,
  )
  return {
    master: now.master,
    isOn: (id: string) => !now.off.includes(id),
    setMaster: (on: boolean) => save({ ...current(), master: on }),
    setOn: (id: string, on: boolean) =>
      save({ ...current(), off: on ? current().off.filter((x) => x !== id) : [...current().off, id] }),
  }
}

export const isMac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform)

// "mod+p" -> ["⌘", "P"]; "g h" -> [["G"], ["H"]] is rendered as steps by the caller.
const NAMES: Record<string, string> = { Escape: "Esc", ArrowDown: "↓", ArrowUp: "↑", ctrl: "Ctrl", Space: "Space" }
export function keyLabels(step: string) {
  if (step === "+") return ["+"]
  return step.split("+").map((k) => (k === "mod" ? (isMac ? "⌘" : "Ctrl") : NAMES[k] || (k.length === 1 ? k.toUpperCase() : k)))
}

function matches(e: KeyboardEvent, step: string) {
  if (step.startsWith("ctrl+")) return e.ctrlKey && !e.metaKey && e.key.toLowerCase() === step.slice(5)
  // ⌘ only on a Mac, so Control+P there stays "previous row".
  if (step.startsWith("mod+")) return (isMac ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey) && e.key.toLowerCase() === step.slice(4)
  return !e.metaKey && !e.ctrlKey && !e.altKey && e.key === (step === "Space" ? " " : step)
}

// A handler may return false to say it did nothing, leaving the key to the browser.
export function useShortcuts(handlers: Partial<Record<string, () => void | false>>) {
  const ref = useRef(handlers)
  useEffect(() => {
    ref.current = handlers
  })

  useEffect(() => {
    let pending = ""
    let timer = 0

    function onKey(e: KeyboardEvent) {
      if (e.isComposing || e.defaultPrevented || !current().master) return
      const target = e.target as HTMLElement
      const typing = !!target.isContentEditable || !!target.closest?.("input, textarea, select")
      const dialog = !!document.querySelector('[role="dialog"]')

      // One entry per alternative key, so "j|ArrowDown" is two shortcuts.
      const active = BINDINGS.filter((b) => {
        if (!ref.current[b.id] || current().off.includes(b.id)) return false
        if (b.when === "app" && dialog) return false
        if (b.when === "dialog" && !dialog) return false
        // Esc belongs to an open menu or select first.
        if (b.combo === "Escape" && document.querySelector('[role="menu"], [role="listbox"]')) return false
        return true
      }).flatMap((b) =>
        b.combo
          .split("|")
          .filter((alt) => !typing || alt.startsWith("mod+"))
          .map((alt) => ({ b, alt })),
      )

      function fire(b: Binding) {
        pending = ""
        window.clearTimeout(timer)
        if (ref.current[b.id]?.() !== false) e.preventDefault()
      }

      if (pending) {
        const first = pending
        pending = ""
        const hit = active.find(({ alt }) => alt.split(" ")[0] === first && alt.split(" ")[1] && matches(e, alt.split(" ")[1]))
        if (hit) return fire(hit.b)
      }
      for (const { b, alt } of active) {
        const [a, second] = alt.split(" ")
        if (!matches(e, a)) continue
        if (second) {
          pending = a
          window.clearTimeout(timer)
          timer = window.setTimeout(() => (pending = ""), 1000)
          return
        }
        return fire(b)
      }
    }

    window.addEventListener("keydown", onKey)
    return () => {
      window.removeEventListener("keydown", onKey)
      window.clearTimeout(timer)
    }
  }, [])
}
