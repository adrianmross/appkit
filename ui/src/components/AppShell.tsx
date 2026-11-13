"use client"

import { Suspense, createContext, useContext, useState, useSyncExternalStore, type ReactNode } from "react"
import Link from "next/link"
import { usePathname, useRouter } from "next/navigation"
import { LogOut, Search, Settings, X } from "lucide-react"
import { toast } from "sonner"
import { useAuth } from "../lib/auth"
import { brand } from "../lib/brand"
import { keyLabels, useShortcuts } from "../lib/keybindings"
import { LiveProvider, type LiveEvent } from "../lib/live"
import type { Event } from "../lib/types"
import { cn } from "../lib/utils"
import { CommandPalette, type PaletteGroup } from "./CommandPalette"
import { DemoBanner } from "./DemoBanner"
import { NotificationBell, eventMeta, type EventText } from "./Notifications"
import { SettingsDialog, type SettingsSection } from "./SettingsDialog"
import { Tour } from "./Tour"
import type { TourConfig } from "./TourOverlay"
import { UserAvatar, displayName } from "./UserAvatar"
import { Button } from "./ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "./ui/dropdown-menu"
import { Kbd } from "./ui/kbd"
import { Toaster } from "./ui/sonner"

function useMedia(query: string) {
  return useSyncExternalStore(
    (cb) => {
      const mq = window.matchMedia(query)
      mq.addEventListener("change", cb)
      return () => mq.removeEventListener("change", cb)
    },
    () => window.matchMedia(query).matches,
    () => false,
  )
}

// What the shell lets the app's own pieces do.
export type Shell = {
  /** Opens Settings on a section, the first one when none is named. */
  openSettings: (section?: string) => void
  /** Focuses the first search field on the page with one of these data-search names; false when there is none. */
  focusSearch: (...names: string[]) => void | false
}

const ShellContext = createContext<Shell | null>(null)

export function useShell() {
  const shell = useContext(ShellContext)
  if (!shell) throw new Error("useShell must be used within AppShell")
  return shell
}

export type ShellProps = {
  children: ReactNode
  /** Where the mark in the corner goes. */
  home: (pathname: string) => { href: string; label: string }
  /** Beside the mark: the way back, breadcrumbs. */
  nav?: ReactNode
  /**
   * The search field in the middle of the header. `compact` asks for a short
   * placeholder; `onBlurEmpty` is set on a phone, where the field is an icon
   * until tapped and closes again when left empty.
   */
  search?: (opts: { compact: boolean; onBlurEmpty?: () => void }) => ReactNode
  /** The account menu's own entries, between the name and Settings. */
  menu?: { label: string; icon?: ReactNode; onSelect: () => void }[]
  sections: SettingsSection[]
  palette: PaletteGroup[]
  onPaletteOpen?: () => void
  palettePlaceholder?: string
  /** How events read in the bell and in toasts. */
  events: EventText
  /** The bell's first tab, before Everything. */
  notificationsFirst?: { label: string; render: (open: boolean) => ReactNode }
  notificationsEmpty?: ReactNode
  /** Adds what the app shows about each event's subject. See LiveProvider. */
  enrich?: (events: Event[]) => Promise<LiveEvent[]>
  /** Handlers for the bindings the app registered, by binding id. */
  shortcuts?: (shell: Shell) => Partial<Record<string, () => void | false>>
  tour?: TourConfig
}

// The signed-in frame: header (mark, nav, search, palette, bell, account
// menu), the page, the brand's footer, and the dialogs that hang off them.
export function AppShell({
  children,
  home,
  nav,
  search,
  menu = [],
  sections,
  palette,
  onPaletteOpen,
  palettePlaceholder,
  events,
  notificationsFirst,
  notificationsEmpty,
  enrich,
  shortcuts,
  tour,
}: ShellProps) {
  const { me, signOut } = useAuth()
  const router = useRouter()
  const pathname = usePathname()
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [settingsSection, setSettingsSection] = useState<string | undefined>()
  const [settingsKey, setSettingsKey] = useState(0)
  // Remount on each open so the dialog starts on the requested section.
  const openSettings = (section?: string) => {
    setSettingsSection(section)
    setSettingsKey((k) => k + 1)
    setSettingsOpen(true)
  }
  const narrow = useMedia("(max-width: 639px)")
  const medium = useMedia("(max-width: 719px)")
  const [searching, setSearching] = useState(false)
  const focusSearch = (...names: string[]) => {
    // On a phone the global search is an icon until tapped.
    if (names[0] === "global" && narrow && search && !document.querySelector('[data-search="global"]')) {
      setSearching(true)
      return
    }
    const el = names.map((n) => document.querySelector<HTMLInputElement>(`[data-search="${n}"]`)).find(Boolean)
    el?.focus()
    el?.select()
    return el ? undefined : (false as const)
  }
  const shell: Shell = { openSettings, focusSearch }
  useShortcuts({
    settings: () => openSettings(),
    keys: () => openSettings("keys"),
    // Search whatever is in front: the open settings dialog, else the app.
    search: () => (document.querySelector("[data-settings-dialog]") ? focusSearch("settings") : focusSearch("global")),
    find: () => focusSearch("audit"),
    ...shortcuts?.(shell),
  })
  const identity = displayName(me)
  const to = home(pathname)

  // A change made somewhere else (an agent, the extension, another tab) gets a toast; your own don't.
  const toastEvent = (ev: LiveEvent) => {
    if (me?.token?.id && ev.actor?.token_id === me.token.id) return
    const href = events.href?.(ev)
    toast(events.describe(ev).text, {
      description: eventMeta(ev, events),
      action: href ? { label: "Open", onClick: () => router.push(href) } : undefined,
    })
  }

  return (
    <ShellContext.Provider value={shell}>
      <LiveProvider onEvent={toastEvent} enrich={enrich}>
        <div className="flex min-h-svh flex-col">
          <DemoBanner />
          {/* Wide, a grid with equal sides, so the search keeps its size and place whatever sits left of it. */}
          <header className={cn("items-center gap-3 border-b px-4 py-2.5", narrow ? "flex" : "grid grid-cols-[1fr_minmax(0,max(22rem,33vw))_1fr]")}>
            {narrow && searching && search ? (
              <>
                <Suspense fallback={<div className="w-full" />}>{search({ compact: true, onBlurEmpty: () => setSearching(false) })}</Suspense>
                <Button variant="ghost" size="icon" aria-label="Close search" onClick={() => setSearching(false)}>
                  <X />
                </Button>
              </>
            ) : (
              <>
                <div className="flex min-w-0 items-center gap-3">
                  <Link href={to.href} aria-label={to.label} className="shrink-0">
                    {/* eslint-disable-next-line @next/next/no-img-element -- drawn SVG mark, not a photo next/image needs to optimize */}
                    <img src={brand().mark.light} alt="" className="h-6 w-auto dark:hidden" />
                    {/* eslint-disable-next-line @next/next/no-img-element */}
                    <img src={brand().mark.dark} alt="" className="hidden h-6 w-auto dark:block" />
                  </Link>
                  {nav}
                </div>
                {!search ? (
                  <div />
                ) : narrow ? (
                  <Button variant="ghost" size="icon" className="ml-auto" aria-label="Search" onClick={() => setSearching(true)}>
                    <Search />
                  </Button>
                ) : (
                  // The middle column: a third of the screen, never under 22rem.
                  <div className="min-w-0">
                    <Suspense fallback={<div className="h-10 w-full" />}>{search({ compact: medium })}</Suspense>
                  </div>
                )}
                <div className={cn("flex shrink-0 items-center justify-end gap-2 justify-self-end", narrow && !search && "ml-auto")}>
                  <CommandPalette groups={palette} sections={sections} onOpenSettings={openSettings} onOpen={onPaletteOpen} placeholder={palettePlaceholder} />
                  <Kbd className="hidden sm:inline-flex" title="Command palette">
                    {keyLabels("mod+p").join(" ")}
                  </Kbd>
                  <NotificationBell text={events} first={notificationsFirst} empty={notificationsEmpty} />
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <Button variant="ghost" size="icon" className="rounded-full" aria-label="Account" data-tour="account">
                        <UserAvatar me={me} size="sm" />
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end" className="w-56">
                      <DropdownMenuLabel className="flex items-center gap-2">
                        <UserAvatar me={me} />
                        <span className="min-w-0">
                          <span className="block truncate text-sm">{identity}</span>
                          {me?.user?.email && me.user.email !== identity && (
                            <span className="block truncate text-xs font-normal text-muted-foreground">{me.user.email}</span>
                          )}
                        </span>
                      </DropdownMenuLabel>
                      <DropdownMenuSeparator />
                      {menu.map((item) => (
                        <DropdownMenuItem key={item.label} onClick={item.onSelect}>
                          {item.icon} {item.label}
                        </DropdownMenuItem>
                      ))}
                      <DropdownMenuItem onClick={() => openSettings()}>
                        <Settings /> Settings
                      </DropdownMenuItem>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem onClick={() => void signOut()} variant="destructive">
                        <LogOut /> Sign out
                      </DropdownMenuItem>
                    </DropdownMenuContent>
                  </DropdownMenu>
                </div>
              </>
            )}
          </header>
          <main className="flex-1 p-4">{children}</main>
          {brand().footer}
          {tour && <Tour {...tour} />}
          <SettingsDialog key={settingsKey} sections={sections} initial={settingsSection} open={settingsOpen} onOpenChange={setSettingsOpen} />
          <Toaster position="bottom-right" />
        </div>
      </LiveProvider>
    </ShellContext.Provider>
  )
}
