import { cookies } from "next/headers"
import { NextResponse, type NextRequest } from "next/server"
import { brand, tokenCookie } from "./brand"
import { isPublicPath } from "./public-paths"
import { ApiError, serverApi } from "./server-api"

// The hosted build's route handlers and its proxy (formerly "middleware").
// An app re-exports them from its own route files:
//
//   app/api/proxy/[...path]/route.ts   export { GET, POST, PATCH, DELETE } from proxyRoute
//   app/api/auth/sign-in/route.ts      export { signInRoute as POST }
//   app/api/auth/sign-out/route.ts     export { signOutRoute as POST }
//   app/api/auth/demo/route.ts         export { demoRoute as POST }
//   proxy.ts                           export default authProxy([...public paths])

// Every client component talks to the Go API through here. Reads the
// httpOnly cookie server-side, attaches it as a bearer header, forwards the
// request verbatim, and streams the response back — the browser never sees
// the token.
async function forward(request: NextRequest, { params }: { params: Promise<{ path: string[] }> }) {
  const path = (await params).path
  const token = (await cookies()).get(tokenCookie())?.value
  const headers: Record<string, string> = {}
  if (token) headers.Authorization = "Bearer " + token
  const contentType = request.headers.get("content-type")
  if (contentType) headers["Content-Type"] = contentType
  // The Go API's CSRF check on /v1/oauth/* trusts the browser's own
  // Sec-Fetch-Site for this origin; pass it through, never invent it.
  const fetchSite = request.headers.get("sec-fetch-site")
  if (fetchSite) headers["Sec-Fetch-Site"] = fetchSite

  const url = brand().serverApiUrl + "/" + path.join("/") + request.nextUrl.search
  const hasBody = request.method !== "GET" && request.method !== "HEAD"

  const response = await fetch(url, {
    method: request.method,
    headers,
    body: hasBody ? await request.arrayBuffer() : undefined,
    cache: "no-store",
  })

  const body = await response.arrayBuffer()
  return new NextResponse(body, {
    status: response.status,
    headers: {
      "Content-Type": response.headers.get("content-type") || "application/json",
    },
  })
}

export const proxyRoute = { GET: forward, POST: forward, PATCH: forward, DELETE: forward }

async function keep(token: string) {
  const jar = await cookies()
  jar.set(tokenCookie(), token, {
    httpOnly: true,
    secure: process.env.NODE_ENV === "production",
    sameSite: "lax",
    path: "/",
    maxAge: 60 * 60 * 24 * 30,
  })
}

// Accepts either a token the Go API issued or a Better Auth JWT — the API
// dispatches on shape, so this just verifies whatever it's given against
// GET /v1/me, then stores it in an httpOnly cookie. The browser never holds
// the raw token.
export async function signInRoute(request: Request) {
  const { token } = (await request.json()) as { token?: string }
  if (!token) {
    return NextResponse.json({ error: { message: "Missing token" } }, { status: 400 })
  }
  try {
    const me = await serverApi("/v1/me", { token })
    await keep(token)
    return NextResponse.json(me)
  } catch (err) {
    const status = err instanceof ApiError ? err.status : 401
    const message = err instanceof ApiError ? err.message : "That token was rejected."
    return NextResponse.json({ error: { message } }, { status })
  }
}

export async function signOutRoute() {
  const jar = await cookies()
  jar.delete(tokenCookie())
  return NextResponse.json({ ok: true })
}

// "Try it without an account" / "replay tutorial": mints a fresh,
// anonymous, throwaway tenant from the Go API (token: "" so no stale
// cookie token gets sent on an otherwise-unauthenticated request) and
// stores the result exactly like a normal sign-in. 404s when the
// deployment doesn't have demo mode on.
export async function demoRoute() {
  try {
    const { token } = await serverApi<{ token: string }>("/v1/demo", { method: "POST", token: "" })
    const me = await serverApi("/v1/me", { token })
    await keep(token)
    return NextResponse.json(me)
  } catch (err) {
    const status = err instanceof ApiError ? err.status : 500
    const message = err instanceof ApiError ? err.message : "Could not start the demo."
    return NextResponse.json({ error: { message } }, { status })
  }
}

// Only runs on the hosted (server) build — static export has no proxy at
// runtime, self-hosted auth is gated client-side instead (see AuthProvider).
// It only checks that the cookie exists; a stale token is caught by
// serverApiAuthed.
export function authProxy(publicPaths: string[] = []) {
  return function proxy(request: NextRequest) {
    const { pathname } = request.nextUrl
    if (isPublicPath(pathname, publicPaths) || request.cookies.has(tokenCookie())) return NextResponse.next()
    const url = request.nextUrl.clone()
    url.pathname = "/sign-in"
    // Come back here after signing in, e.g. to an invite link.
    url.search = "?" + new URLSearchParams({ next: pathname + request.nextUrl.search })
    return NextResponse.redirect(url)
  }
}
