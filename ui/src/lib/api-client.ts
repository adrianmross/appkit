// Client-side API access. Two modes, picked at build time:
//
// - Hosted (default): calls go through /api/proxy/*, which reads the
//   httpOnly session cookie server-side. The browser never holds a token.
// - Self-hosted (NEXT_PUBLIC_EXPORT_MODE=1, static export): there is no
//   Next.js server at runtime — the Go binary just serves static files — so
//   there's nothing to proxy through. The browser calls the Go API directly
//   with a bearer token it holds itself, kept in localStorage.
"use client"

import { brand, storageKey } from "./brand"

const EXPORT_MODE = process.env.NEXT_PUBLIC_EXPORT_MODE === "1"
// Self-hosted serves the dashboard and the API from the same binary and
// port — same origin, so a relative path is correct by default. Only set
// the brand's apiUrl if the dashboard is genuinely deployed separately from
// the API it talks to.
const apiBase = () => brand().apiUrl
const tokenKey = () => storageKey("token")
// Same-origin export mode keeps the token in the Go server's httpOnly
// session cookie (POST /v1/auth/session), out of reach of page scripts.
// Only a dashboard on another origin than its API still holds a bearer
// token in localStorage, since the cookie can't follow it there.
export const cookieSession = () => EXPORT_MODE && !apiBase()

export class ApiError extends Error {
  status: number
  hint?: string
  constructor(message: string, status: number, hint?: string) {
    super(message)
    this.status = status
    this.hint = hint
  }
}

// Only meaningful in export mode — the hosted build never reads this.
export function getToken() {
  if (typeof window === "undefined") return ""
  return localStorage.getItem(tokenKey()) || ""
}

export function setToken(token: string) {
  if (typeof window === "undefined") return
  if (token) localStorage.setItem(tokenKey(), token)
  else localStorage.removeItem(tokenKey())
}

type ApiOptions = {
  method?: string
  body?: unknown
  rawBody?: { text: string; contentType: string }
  // Aborts the request after this many milliseconds (a DOMException named
  // "TimeoutError"). Unset means no limit, which long uploads rely on.
  timeoutMs?: number
  // Set on the one retry after a passkey step-up, so a second refusal surfaces.
  steppedUp?: boolean
}

export async function api<T>(path: string, options: ApiOptions = {}): Promise<T> {
  const headers: Record<string, string> = {}
  const init: RequestInit = { method: options.method || "GET", headers }
  if (options.timeoutMs) init.signal = AbortSignal.timeout(options.timeoutMs)
  if (options.rawBody) {
    headers["Content-Type"] = options.rawBody.contentType
    init.body = options.rawBody.text
  } else if (options.body !== undefined) {
    headers["Content-Type"] = "application/json"
    init.body = JSON.stringify(options.body)
  }

  let url: string
  if (EXPORT_MODE) {
    url = apiBase() + path
    const token = cookieSession() ? "" : getToken()
    if (token) headers.Authorization = "Bearer " + token
  } else {
    url = "/api/proxy" + path
  }

  const response = await fetch(url, init)
  const text = await response.text()
  let data: { error?: { message?: string; hint?: string; code?: string } } | null = null
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = { error: { message: text } }
    }
  }
  if (!response.ok) {
    const body = data?.error || {}
    // Approving needs a recent passkey sign-in: ask for one, then retry once.
    // Sign-in swaps the session cookie, which the retry then carries.
    if (body.code === "step_up_required" && cookieSession() && !options.steppedUp) {
      const { signInWithPasskey } = await import("./passkeys")
      await signInWithPasskey()
      return api<T>(path, { ...options, steppedUp: true })
    }
    throw new ApiError(body.message || response.statusText || "Request failed", response.status, body.hint)
  }
  return data as T
}

// The server URL an agent or the CLI should use. Client-only: the dashboard
// and API share an origin unless the brand's apiUrl says otherwise.
export function serverUrl(): string {
  return apiBase() || window.location.origin
}

// For calls that can't go through api<T>() (binary responses, e.g. a
// favicon image) — same proxy-vs-direct branching, caller does its own fetch.
export function apiUrl(path: string): string {
  return EXPORT_MODE ? apiBase() + path : "/api/proxy" + path
}

export function apiHeaders(): Record<string, string> {
  if (!EXPORT_MODE || cookieSession()) return {}
  const token = getToken()
  return token ? { Authorization: "Bearer " + token } : {}
}

export { EXPORT_MODE }
