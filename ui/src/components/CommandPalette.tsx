"use client"

import { useEffect, useState, type ReactNode } from "react"
import { catchError } from "next/error"
import { LogOut } from "lucide-react"
import { sectionLinks, type SettingsSection } from "./SettingsDialog"
import { useAuth } from "../lib/auth"
import { useShortcuts } from "../lib/keybindings"
import { Command, CommandDialog, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "./ui/command"

// `value` is what typing matches; it defaults to the label when that is plain text.
export type PaletteItem = { id?: string; label: ReactNode; icon?: ReactNode; value?: string; onSelect: () => void }
export type PaletteGroup = { heading: string; items: PaletteItem[] }

// Contains a render error to the palette: the dialog shows this instead of
// the whole page falling through to app/error.tsx. Closing the dialog
// unmounts the boundary, so the next open starts clean.
const PaletteBoundary = catchError(function PaletteError() {
  return <p className="p-6 text-center text-sm text-muted-foreground">Fast actions hit an error. Close and reopen to try again.</p>
})

// The app's groups come first, then one entry per settings section and Sign
// out. `onOpen` runs each time the palette opens, for groups that list
// records: fetch there and pass the result back in through `groups`.
export function CommandPalette({
  groups,
  sections,
  onOpenSettings,
  onOpen,
  placeholder = "Run a command…",
}: {
  groups: PaletteGroup[]
  sections: SettingsSection[]
  onOpenSettings: (section: string) => void
  onOpen?: () => void
  placeholder?: string
}) {
  const { signOut } = useAuth()
  const [open, setOpen] = useState(false)

  useShortcuts({ palette: () => setOpen((prev) => !prev) })

  useEffect(() => {
    if (open) onOpen?.()
    // eslint-disable-next-line react-hooks/exhaustive-deps -- only when it opens
  }, [open])

  function go(fn: () => void) {
    fn()
    setOpen(false)
  }

  return (
    <CommandDialog open={open} onOpenChange={setOpen} title="Fast actions" description={placeholder}>
      <PaletteBoundary>
        {/* cmdk's Input, List and Items read the store <Command> provides;
            without it they throw "Cannot read properties of undefined (reading 'subscribe')". */}
        <Command>
          <CommandInput placeholder={placeholder} />
          <CommandList>
            <CommandEmpty>No matches.</CommandEmpty>
            {groups.map((group) => (
              <CommandGroup key={group.heading} heading={group.heading}>
                {group.items.map((item, i) => (
                  <CommandItem key={item.id ?? i} value={item.value} onSelect={() => go(item.onSelect)}>
                    {item.icon} {item.label}
                  </CommandItem>
                ))}
              </CommandGroup>
            ))}
            <CommandGroup heading="Settings">
              {sectionLinks(sections).map((link) => (
                <CommandItem key={link.id} value={`settings ${link.label} ${link.keywords}`} onSelect={() => go(() => onOpenSettings(link.id))}>
                  {link.icon} Settings: {link.label}
                </CommandItem>
              ))}
            </CommandGroup>
            <CommandGroup heading="Account">
              <CommandItem onSelect={() => go(() => void signOut())}>
                <LogOut /> Sign out
              </CommandItem>
            </CommandGroup>
          </CommandList>
        </Command>
      </PaletteBoundary>
    </CommandDialog>
  )
}
