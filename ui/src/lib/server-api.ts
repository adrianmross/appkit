import { cookies } from "next/headers"
import { redirect } from "next/navigation"

import { brand, tokenCookie } from "./brand"
import type { Me } from "./types"

export class ApiError extends Error {
  status: number
  hint?: string
  constructor(message: string, status: number, hint?: string) {
    super(message)
    this.status = status
    this.hint = hint
  }
}

type ApiOptions = {
  method?: string
  body?: unknown
  rawBody?: { text: string; contentType: string }
  /** Explicit bearer token, for calls made before the cookie is set (sign-in). */
  token?: string
}

// Server-only: the only thing that talks to the Go API. Reads the bearer
// token from the httpOnly cookie (never exposed to client JS) unless one is
// passed explicitly (the sign-in route, before the cookie exists yet).
export async function serverApi<T>(path: string, options: ApiOptions = {}): Promise<T> {
  const token = options.token ?? (await cookies()).get(tokenCookie())?.value
  const headers: Record<string, string> = {}
  if (token) headers.Authorization = "Bearer " + token

  const init: RequestInit = { method: options.method || "GET", headers, cache: "no-store" }
  if (options.rawBody) {
    headers["Content-Type"] = options.rawBody.contentType
    init.body = options.rawBody.text
  } else if (options.body !== undefined) {
    headers["Content-Type"] = "application/json"
    init.body = JSON.stringify(options.body)
  }

  const response = await fetch(brand().serverApiUrl + path, init)
  const text = await response.text()
  let data: { error?: { message?: string; hint?: string } } | null = null
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = { error: { message: text } }
    }
  }
  if (!response.ok) {
    const body = data?.error || {}
    throw new ApiError(body.message || response.statusText || "Request failed", response.status, body.hint)
  }
  return data as T
}

export const EXPORT_MODE = process.env.NEXT_PUBLIC_EXPORT_MODE === "1"

// Server-side identity lookup for the root layout (hosted build only —
// middleware already redirected anonymous requests before this runs).
export async function getMe() {
  if (EXPORT_MODE) return null
  const token = (await cookies()).get(tokenCookie())?.value
  if (!token) return null
  try {
    return await serverApi<Me>("/v1/me")
  } catch {
    return null
  }
}

// Protected pages' server-side data fetches use this instead of serverApi
// directly: proxy.ts only checks that the cookie exists, not that its token
// is still valid, so a revoked/expired/stale token reaches the page and
// serverApi() throws — unhandled, that's a 500, not a sign-in prompt.
export async function serverApiAuthed<T>(path: string, options: ApiOptions = {}): Promise<T> {
  try {
    return await serverApi<T>(path, options)
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) redirect("/sign-in")
    throw err
  }
}

// Server-side, hosted build only: whether the inline sign-in on the CLI
// consent pages offers the paste-a-token form. Export mode has no server
// here; SignInForm checks /v1/auth/providers client-side instead.
export async function allowTokenLogin(): Promise<boolean> {
  if (EXPORT_MODE) return true
  try {
    return (await serverApi<{ allow_token_login: boolean }>("/v1/auth/providers")).allow_token_login
  } catch {
    return true
  }
}
