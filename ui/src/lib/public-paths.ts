// Paths anyone may open on every app. A trailing "/" matches everything under it.
const PUBLIC = ["/sign-in", "/device", "/authorize", "/api/auth/", "/_next/", "/brand/", "/favicon.ico"]

export function isPublicPath(pathname: string, extra: string[] = []) {
  return [...PUBLIC, ...extra].some((p) => (p.endsWith("/") && p !== "/" ? pathname.startsWith(p) : pathname === p))
}
