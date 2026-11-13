"use client"

import { useSyncExternalStore } from "react"
import { Moon, Sun } from "lucide-react"
import { useTheme } from "next-themes"
import { Button } from "./ui/button"
import { cn } from "../lib/utils"

// Public pages: light or dark, nothing in between. It starts on whatever the system
// is using (the stored theme stays "system" until the first click), and writes the same
// next-themes setting as Settings > Appearance, so a choice made signed out is the one
// the app uses after sign-in.
const noop = () => () => {}

export function ThemeToggle({ className }: { className?: string }) {
  const { resolvedTheme, setTheme } = useTheme()
  // The resolved theme is only known in the browser; show light until hydrated.
  const mounted = useSyncExternalStore(noop, () => true, () => false)
  const dark = mounted && resolvedTheme === "dark"
  const Icon = dark ? Moon : Sun
  return (
    <Button
      type="button"
      variant="ghost"
      size="icon"
      className={cn("shrink-0", className)}
      aria-label={`Theme: ${dark ? "dark" : "light"}. Switch to ${dark ? "light" : "dark"}`}
      title={`Theme: ${dark ? "dark" : "light"}`}
      onClick={() => setTheme(dark ? "light" : "dark")}
    >
      <Icon />
    </Button>
  )
}
