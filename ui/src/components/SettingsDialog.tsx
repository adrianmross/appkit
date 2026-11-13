"use client"

import { useMemo, useState, type KeyboardEvent, type ReactNode } from "react"
import { History, Keyboard, KeyRound, Palette, Plug, Search, User } from "lucide-react"
import { getPref, setPref, type Pref, type TextSize } from "../lib/settings"
import { useShortcuts } from "../lib/keybindings"
import { useTheme } from "next-themes"
import { cn } from "../lib/utils"
import { KeyboardPanel } from "./settings/KeyboardPanel"
import { AuditPanel } from "./settings/AuditPanel"
import { AccountPanel } from "./settings/AccountPanel"
import { TokensPanel } from "./settings/TokensPanel"
import { ConnectedAppsPanel } from "./settings/ConnectedAppsPanel"
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "./ui/dialog"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "./ui/field"
import { Input } from "./ui/input"
import { ToggleGroup, ToggleGroupItem } from "./ui/toggle-group"

type Theme = "light" | "dark" | "system"

// Miniature app window: sidebar bar, header bar, two rows. Colors are fixed
// per theme so the preview shows that theme regardless of the current one.
const PALETTES = {
  light: { bg: "#ffffff", bar: "#e4e4e7", fg: "#18181b", accent: "#3b82f6" },
  dark: { bg: "#18181b", bar: "#3f3f46", fg: "#fafafa", accent: "#60a5fa" },
}

function Mini({ p, x, w }: { p: (typeof PALETTES)["light"]; x: number; w: number }) {
  return (
    <svg x={x} width={w} height={60} viewBox="0 0 60 60" preserveAspectRatio="none">
      <rect width="60" height="60" fill={p.bg} />
      <rect x="0" y="0" width="14" height="60" fill={p.bar} />
      <rect x="18" y="6" width="26" height="5" rx="2" fill={p.fg} opacity=".8" />
      <rect x="18" y="18" width="38" height="6" rx="2" fill={p.bar} />
      <rect x="18" y="28" width="38" height="6" rx="2" fill={p.bar} />
      <rect x="18" y="38" width="22" height="6" rx="2" fill={p.accent} />
    </svg>
  )
}

function ThemeCard({ value, current, onPick }: { value: Theme; current: Theme; onPick: (t: Theme) => void }) {
  return (
    <button
      type="button"
      onClick={() => onPick(value)}
      aria-pressed={current === value}
      className={cn(
        "flex w-28 shrink-0 flex-col gap-1.5 rounded-lg border p-1.5 text-left text-xs capitalize outline-none focus-visible:ring-2 focus-visible:ring-ring",
        current === value ? "border-primary ring-2 ring-primary/30" : "hover:bg-muted",
      )}
    >
      <svg viewBox="0 0 60 60" className="aspect-[3/2] w-full overflow-hidden rounded-md border" preserveAspectRatio="xMidYMid slice">
        {value === "system" ? (
          <>
            <Mini p={PALETTES.light} x={0} w={30} />
            <svg x={30} width={30} height={60} viewBox="30 0 30 60">
              <svg width={60} height={60} viewBox="0 0 60 60">
                <Mini p={PALETTES.dark} x={0} w={60} />
              </svg>
            </svg>
          </>
        ) : (
          <Mini p={PALETTES[value]} x={0} w={60} />
        )}
      </svg>
      <span className="px-0.5 font-medium">{value}</span>
    </button>
  )
}

// One labelled setting with its explanation underneath.
export function SettingsRow({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <Field>
      <FieldLabel>{label}</FieldLabel>
      <div className="flex flex-col">{children}</div>
      {hint && <FieldDescription>{hint}</FieldDescription>}
    </Field>
  )
}

// A preference picked from a few named values; it saves as it is picked.
export function SettingsChoice<T extends string>({ pref, options }: { pref: Pref; options: [T, string][] }) {
  const [value, setValue] = useState(getPref<T>(pref))
  return (
    <ToggleGroup
      type="single"
      variant="outline"
      value={value}
      onValueChange={(v) => {
        if (!v) return
        setValue(v as T)
        setPref(pref, v)
      }}
    >
      {options.map(([v, label]) => (
        <ToggleGroupItem key={v} value={v}>
          {label}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  )
}

// The appearance section. An app's own appearance settings go in as
// children, after the platform's.
export function AppearanceSettings({ children }: { children?: ReactNode }) {
  const { theme, setTheme } = useTheme()
  return (
    <FieldGroup>
      <SettingsRow label="Theme" hint="System follows your operating system and switches automatically.">
        <div className="flex flex-wrap gap-3">
          {(["light", "dark", "system"] as const).map((t) => (
            <ThemeCard key={t} value={t} current={(theme as Theme) || "system"} onPick={setTheme} />
          ))}
        </div>
      </SettingsRow>
      <SettingsRow label="Text size" hint="Scales the whole interface.">
        <SettingsChoice<TextSize> pref="textSize" options={[["small", "Small"], ["default", "Default"], ["large", "Large"]]} />
      </SettingsRow>
      <SettingsRow label="Motion" hint="Reduced turns off transitions and animations.">
        <SettingsChoice pref="motion" options={[["full", "Full"], ["reduced", "Reduced"]]} />
      </SettingsRow>
      <SettingsRow label="Text reveal" hint="Text decodes into place as it opens: in the notes view, or everywhere text opens. Off whenever motion is reduced.">
        <SettingsChoice pref="textFx" options={[["notes", "Notes"], ["all", "Everywhere"], ["off", "Off"]]} />
      </SettingsRow>
      <SettingsRow label="Vim keys in notes" hint="The notes editor takes Vim motions, with zc, zo and za to fold. Applies the next time notes open.">
        <SettingsChoice pref="vimKeys" options={[["off", "Off"], ["on", "On"]]} />
      </SettingsRow>
      {children}
    </FieldGroup>
  )
}

// `items` feed the settings search and the command palette: each is a
// setting's label plus synonyms. `sub` indents the section under the one above.
export type SettingsSection = {
  id: string
  label: string
  icon: ReactNode
  render: (close: () => void) => ReactNode
  items: [string, string][]
  sub?: boolean
}

// The sections every app has. An app builds its list from these and its
// own: `[platform.appearance, mine, platform.tokens, ...]`.
export const platformSections = {
  appearance: {
    id: "appearance",
    label: "Appearance",
    icon: <Palette />,
    render: () => <AppearanceSettings />,
    items: [
      ["Theme", "light dark system mode color"],
      ["Text size", "font zoom larger smaller"],
      ["Motion", "animations reduce accessibility"],
    ],
  },
  tokens: {
    id: "tokens",
    label: "API tokens",
    icon: <KeyRound />,
    render: () => <TokensPanel />,
    items: [
      ["Generate token", "api key cli agent extension"],
      ["Revoke token", "delete"],
    ],
  },
  apps: {
    id: "apps",
    label: "Connected apps",
    icon: <Plug />,
    render: () => <ConnectedAppsPanel />,
    items: [
      ["Connected apps", "chatgpt claude connector oauth mcp authorized"],
      ["Disconnect app", "revoke sign out"],
    ],
  },
  account: {
    id: "account",
    label: "Account",
    icon: <User />,
    render: () => <AccountPanel />,
    items: [
      ["Change password", "security current new"],
      ["Email", "address sign-in"],
      ["Sessions", "sign out other sessions revoke devices browsers security"],
      ["Display name", "profile email avatar photo picture"],
      ["Sign out", "logout session"],
    ],
  },
  keys: {
    id: "keys",
    label: "Keyboard",
    icon: <Keyboard />,
    render: () => <KeyboardPanel />,
    items: [["Keyboard shortcuts", "keybindings keys hotkeys vim enable disable"]],
  },
  audit: {
    id: "audit",
    label: "Audit log",
    icon: <History />,
    render: () => <AuditPanel />,
    items: [["Audit log", "activity history changes who did what"]],
  },
} satisfies Record<string, SettingsSection>

// For the command palette: one entry per section, searchable by its settings.
export const sectionLinks = (sections: SettingsSection[]) =>
  sections.map((s) => ({
    id: s.id,
    label: s.label,
    icon: s.icon,
    keywords: s.items.map(([label, words]) => label + " " + words).join(" "),
  }))

export function SettingsDialog({
  sections,
  open,
  onOpenChange,
  initial = sections[0]?.id,
}: {
  sections: SettingsSection[]
  open: boolean
  onOpenChange: (open: boolean) => void
  initial?: string
}) {
  const [active, setActive] = useState(initial)
  const [query, setQuery] = useState("")

  const hits = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return []
    return sections.flatMap((s) =>
      s.items
        .filter(([label, words]) => (s.label + " " + label + " " + words).toLowerCase().includes(q))
        .map(([label]) => ({ section: s, label })),
    )
  }, [query, sections])

  const [sel, setSel] = useState(0)
  const pick = (id: string) => {
    setActive(id)
    setQuery("")
  }
  function onSearchKey(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === "Escape") {
      // Clear first, then leave the field. The dialog's own Esc is held back below.
      if (query) {
        setQuery("")
        setSel(0)
      } else e.currentTarget.blur()
      return
    }
    if (!hits.length) return
    const down = e.key === "ArrowDown" || (e.ctrlKey && e.key === "n")
    const up = e.key === "ArrowUp" || (e.ctrlKey && e.key === "p")
    if (down || up) {
      e.preventDefault()
      setSel((i) => (i + (down ? 1 : hits.length - 1)) % hits.length)
    } else if (e.key === "Enter") {
      e.preventDefault()
      pick(hits[Math.min(sel, hits.length - 1)].section.id)
    }
  }

  const current = sections.find((s) => s.id === active) || sections[0]
  const step = (d: number) => {
    setQuery("")
    setActive(sections[(sections.indexOf(current) + d + sections.length) % sections.length].id)
  }
  useShortcuts({ prevSection: () => step(-1), nextSection: () => step(1) })

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) setQuery("")
        onOpenChange(next)
      }}
    >
      <DialogContent
        data-settings-dialog
        onEscapeKeyDown={(e) => {
          if ((e.target as HTMLElement).closest("[data-search]")) e.preventDefault()
        }}
        className="h-[90svh] w-[95vw] gap-0 overflow-hidden p-0 sm:max-w-6xl" showCloseButton>
        <DialogTitle className="sr-only">Settings</DialogTitle>
        <DialogDescription className="sr-only">{sections.map((s) => s.label).join(", ")}.</DialogDescription>
        <div className="grid h-full min-h-0 grid-cols-1 sm:grid-cols-[14rem_1fr]">
          <nav className="flex flex-col gap-2 border-b bg-muted/40 p-3 sm:border-r sm:border-b-0">
            <div className="relative">
              <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input
                data-search="settings"
                aria-label="Search settings"
                placeholder="Search settings"
                className="h-8 pl-8"
                value={query}
                onChange={(e) => {
                  setQuery(e.target.value)
                  setSel(0)
                }}
                onKeyDown={onSearchKey}
              />
            </div>
            <div className="flex gap-1 overflow-x-auto sm:flex-col">
              {sections.map((s) => (
                <button
                  key={s.id}
                  type="button"
                  onClick={() => {
                    setActive(s.id)
                    setQuery("")
                  }}
                  aria-current={!query && s.id === current.id}
                  className={cn(
                    "flex shrink-0 items-center gap-2 rounded-md px-2.5 py-1.5 text-sm [&_svg]:size-4",
                    s.sub && "sm:ml-5",
                    !query && s.id === current.id ? "bg-background font-medium shadow-sm ring-1 ring-foreground/10" : "text-muted-foreground hover:bg-muted",
                  )}
                >
                  {s.icon}
                  {s.label}
                </button>
              ))}
            </div>
          </nav>
          <section className="min-h-0 overflow-y-auto p-5 pr-12">
            {query ? (
              <div className="flex flex-col gap-1">
                <h2 className="mb-2 text-base font-medium">Results for “{query}” <span className="text-xs font-normal text-muted-foreground">↑↓ or Ctrl-N/P to move, Enter to open</span></h2>
                {hits.length === 0 && <p className="text-sm text-muted-foreground">No matching settings.</p>}
                {hits.map(({ section, label }, i) => (
                  <button
                    key={section.id + label}
                    type="button"
                    aria-selected={i === sel}
                    ref={(el) => {
                      if (i === sel) el?.scrollIntoView({ block: "nearest" })
                    }}
                    className={cn(
                      "flex items-center justify-between rounded-md px-2 py-2 text-left text-sm hover:bg-muted",
                      i === sel && "bg-muted ring-1 ring-foreground/10",
                    )}
                    onMouseEnter={() => setSel(i)}
                    onClick={() => pick(section.id)}
                  >
                    {label}
                    <span className="text-xs text-muted-foreground">{section.label}</span>
                  </button>
                ))}
              </div>
            ) : (
              <>
                <h2 className="mb-4 text-base font-medium">{current.label}</h2>
                {current.render(() => onOpenChange(false))}
              </>
            )}
          </section>
        </div>
      </DialogContent>
    </Dialog>
  )
}
