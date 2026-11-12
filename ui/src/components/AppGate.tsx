"use client"

import { useEffect } from "react"
import { useRouter } from "next/navigation"
import { EXPORT_MODE } from "../lib/api-client"
import { useAuth } from "../lib/auth"

// Hosted build: proxy.ts already redirected unauthenticated requests to
// /sign-in server-side before this ever renders — this is a no-op there.
//
// Export mode has no server to do that gating (no middleware at runtime in
// a static export), so this is the client-side equivalent: wait for
// AuthProvider's mount-time /v1/me check (see lib/auth.tsx) to settle,
// then bounce to /sign-in if it didn't produce an identity. The public
// Landing page lives at "/" outside this gate (app/page.tsx).
export function AppGate({ children }: { children: React.ReactNode }) {
  const router = useRouter()
  const { me, loading } = useAuth()
  const anonymous = EXPORT_MODE && !loading && me == null

  useEffect(() => {
    if (anonymous) router.replace("/sign-in")
  }, [anonymous, router])

  if (EXPORT_MODE && loading) return null
  if (anonymous) return null
  return children
}
