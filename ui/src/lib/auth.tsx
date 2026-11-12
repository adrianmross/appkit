"use client"

import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react"
import { api, ApiError, cookieSession, EXPORT_MODE, getToken, setToken } from "./api-client"
import { brand } from "./brand"
import { setPref } from "./settings"
import type { Me } from "./types"

type AuthState = {
  me: Me | null
  // Only meaningful in export mode: true until the mount-time /v1/me check
  // (below) resolves one way or the other. Hosted mode has nothing to wait
  // for — `me` is already server-seeded — so this is always false there.
  loading: boolean
  error: string | null
  signIn: (token: string) => Promise<void>
  // "Try it without an account" (signed out) and "replay tutorial"
  // (Settings, any time, signed in or not): mints a fresh throwaway
  // sandbox tenant and signs into it, same as a normal sign-in. 404s if
  // the deployment doesn't have demo mode on (see isDemoAvailable below).
  startDemo: () => Promise<void>
  // `to` is where the hard navigation after sign-out lands.
  signOut: (to?: string) => Promise<void>
  // Export mode behind a trusted proxy: every token-less
  // request is the owner, so after an explicit sign-out the mount-time
  // check is skipped (or /sign-in would sign straight back in) and the
  // sign-in page offers this to go back in as the owner.
  proxySignedOut: boolean
  continueAsOwner: () => Promise<void>
  // Re-reads /v1/me after the profile (name, avatar) changes.
  refresh: () => Promise<void>
  can: (scope: string) => boolean
  // True once signed into a demo sandbox (POST /v1/demo names its token
  // "demo"). Settings uses this to label
  // its action "Replay tutorial" instead of "Try the tutorial".
  isDemo: boolean
}

const AuthContext = createContext<AuthState | null>(null)

const proxySignedOutKey = () => `${brand().slug}_proxy_signed_out`

function readProxySignedOut() {
  return typeof window !== "undefined" && localStorage.getItem(proxySignedOutKey()) === "1"
}

// Export mode sign-in: same-origin, the server checks the token and keeps
// it in its httpOnly cookie; split-origin, the browser keeps it.
async function storeToken(token: string, timeoutMs?: number): Promise<Me> {
  if (cookieSession()) return api<Me>("/v1/auth/session", { method: "POST", body: { token }, timeoutMs })
  setToken(token)
  return api<Me>("/v1/me", { timeoutMs })
}

// The mount-time session check gates every signed-in page (AppGate renders
// nothing until it settles), so it can't wait on a stalled request forever:
// a cold serverless start or a dropped connection would leave a blank page.
// Past this, the check gives up and the gate sends the visitor to sign in.
const SESSION_CHECK_TIMEOUT_MS = 10_000

// On the hosted build, `me` is fetched server-side (middleware already
// redirects anonymous requests to /sign-in, and the root layout passes the
// server-fetched identity down) — this provider just holds it in context.
// In export mode there's no server fetch to seed it from: it starts null,
// SignIn populates it directly after a successful token check, and — since
// a full page load/reload re-mounts this provider with initialMe back to
// null even though the browser still holds a valid token — the effect
// below re-populates it from that stored token on mount. Without this,
// `can()` silently goes back to false after any reload (the exact bug
// that made row actions vanish post-reload: see AppGate, which relies on
// this same state instead of doing its own disconnected check).
//
// The check always fires in export mode, even with no stored token — it
// just 401s harmlessly when there's genuinely nothing stored. A token left
// in localStorage by an older build is moved into the session cookie here. Demo mode
// doesn't change this: a demo session is a normal token from
// POST /v1/demo (see startDemo below), stored exactly like a real one.
export function AuthProvider({ initialMe, children }: { initialMe: Me | null; children: ReactNode }) {
  const [me, setMe] = useState<Me | null>(initialMe)
  const [loading, setLoading] = useState(EXPORT_MODE && initialMe == null)
  const [error, setError] = useState<string | null>(null)
  const [proxySignedOut, setProxySignedOut] = useState(false)

  const setProxyMarker = useCallback((on: boolean) => {
    if (on) localStorage.setItem(proxySignedOutKey(), "1")
    else localStorage.removeItem(proxySignedOutKey())
    setProxySignedOut(on)
  }, [])

  useEffect(() => {
    const skip = EXPORT_MODE && me == null && !getToken() && readProxySignedOut()
    if (!EXPORT_MODE || me != null || skip) {
      // Nothing to check (hosted, already resolved, or signed out of the
      // proxy owner) — settles the one-time mount-time state, not a render loop.
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setProxySignedOut(skip)
      setLoading(false)
      return
    }
    let cancelled = false
    const legacy = cookieSession() ? getToken() : ""
    const timeoutMs = SESSION_CHECK_TIMEOUT_MS
    const check = legacy ? storeToken(legacy, timeoutMs).finally(() => setToken("")) : api<Me>("/v1/me", { timeoutMs })
    check
      .then((result) => {
        if (!cancelled) setMe(result)
      })
      .catch((err) => {
        if (cancelled) return
        // Only a server that answered and refused the token signs it out; a
        // timeout or network error leaves it for the next attempt.
        if (err instanceof ApiError) setToken("")
        else setError("Couldn't reach the server. Check your connection and try again.")
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const signIn = useCallback(async (token: string) => {
    setError(null)
    try {
      if (EXPORT_MODE) {
        setProxyMarker(false)
        setMe(await storeToken(token))
      } else {
        const response = await fetch("/api/auth/sign-in", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ token }),
        })
        const data = await response.json()
        if (!response.ok) throw new Error(data?.error?.message || "That token was rejected.")
        setMe(data as Me)
      }
    } catch (err) {
      setMe(null)
      setError(err instanceof ApiError || err instanceof Error ? err.message : "Sign-in failed")
      throw err
    }
  }, [setProxyMarker])

  const startDemo = useCallback(async () => {
    setError(null)
    try {
      if (EXPORT_MODE) {
        const { token } = await api<{ token: string }>("/v1/demo", { method: "POST" })
        setProxyMarker(false)
        setMe(await storeToken(token))
      } else {
        const response = await fetch("/api/auth/demo", { method: "POST" })
        const data = await response.json()
        if (!response.ok) throw new Error(data?.error?.message || "Could not start the demo.")
        setMe(data as Me)
      }
      // A new sandbox gets the guided tour, also on Replay.
      setPref("tour", "on")
    } catch (err) {
      setError(err instanceof ApiError || err instanceof Error ? err.message : "Could not start the demo.")
      throw err
    }
  }, [setProxyMarker])

  const continueAsOwner = useCallback(async () => {
    setProxyMarker(false)
    setMe(await api<Me>("/v1/me"))
  }, [setProxyMarker])

  const signOut = useCallback(async (to = "/sign-in") => {
    if (EXPORT_MODE) {
      // Awaited for the same reason as the hosted branch below.
      if (cookieSession()) await api("/v1/auth/session", { method: "DELETE" })
      setToken("")
      if (me?.proxy) setProxyMarker(true)
    } else {
      // Must await: navigating away can cancel an in-flight fire-and-forget
      // request before the server clears the cookie, leaving the user
      // still signed in after landing on /sign-in.
      await fetch("/api/auth/sign-out", { method: "POST" })
    }
    setMe(null)
    // A hard navigation, not router.push: forces every cached RSC payload
    // to be re-fetched so no stale authenticated state can linger client-side.
    // eslint-disable-next-line @next/next/no-location-assign-relative-destination
    window.location.assign(to)
  }, [me, setProxyMarker])

  const can = useCallback(
    (scope: string) => {
      const scopes = me?.token.scopes || []
      return scopes.includes(scope) || (scope === "read" && scopes.includes("write"))
    },
    [me],
  )

  const isDemo = me?.token.name === "demo"
  const refresh = useCallback(async () => setMe(await api<Me>("/v1/me")), [])

  return (
    <AuthContext.Provider value={{ me, loading, error, signIn, startDemo, signOut, proxySignedOut, continueAsOwner, refresh, can, isDemo }}>
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error("useAuth must be used within AuthProvider")
  return ctx
}
