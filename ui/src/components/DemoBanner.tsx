"use client"

import { useEffect, useState } from "react"
import { api } from "../lib/api-client"
import { useAuth } from "../lib/auth"
import { Button } from "./ui/button"

// Shown in a demo sandbox only: it is throwaway, so say so and offer the way out.
export function DemoBanner() {
  const { isDemo, signOut } = useAuth()
  const [signupsOpen, setSignupsOpen] = useState(false)
  useEffect(() => {
    if (!isDemo) return
    api<{ signups_open?: boolean }>("/v1/auth/providers")
      .then((p) => setSignupsOpen(p.signups_open === true))
      .catch(() => {})
  }, [isDemo])
  if (!isDemo) return null
  return (
    <div role="status" className="flex flex-wrap items-center justify-center gap-x-4 gap-y-1 border-b bg-muted px-4 py-2 text-sm">
      <span>
        <strong className="font-medium">Demo sandbox.</strong> <span className="text-muted-foreground">Everything here is throwaway.</span>
      </span>
      <span className="flex items-center gap-2">
        {signupsOpen && (
          <Button size="sm" onClick={() => void signOut("/sign-in?mode=sign-up")}>
            Create an account
          </Button>
        )}
        <Button size="sm" variant="outline" onClick={() => void signOut()}>
          Leave demo
        </Button>
      </span>
    </div>
  )
}
