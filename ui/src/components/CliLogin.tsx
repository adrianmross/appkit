"use client"

// Consent screens for the CLI's `auth login`: /authorize (browser + PKCE)
// and /device (device code). Both sign the human in inline, so the query
// string the CLI built survives sign-in. Everything shown comes from the
// server (GET /v1/oauth/authorize, GET /v1/oauth/device); this page never
// parses scopes itself, and the server re-checks on approve.

import { useEffect, useState, type FormEvent, type ReactNode } from "react"
import { api, ApiError } from "../lib/api-client"
import { brand } from "../lib/brand"
import { useAuth } from "../lib/auth"
import { SignInForm } from "./SignInForm"
import { ThemeToggle } from "./ThemeToggle"
import { Button } from "./ui/button"
import { Input } from "./ui/input"
import { Label } from "./ui/label"
import { Badge } from "./ui/badge"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./ui/card"

const SCOPE_HELP: Record<string, string> = {
  read: "See your applications, companies, and settings.",
  write: "Create and change applications. This cannot approve anything.",
}

function errorText(err: unknown) {
  if (err instanceof ApiError) return err.hint ? `${err.message}. ${err.hint}` : err.message
  return err instanceof Error ? err.message : "Request failed"
}

// "3 minutes ago", from the server's created_at.
function ago(iso: string) {
  const seconds = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 1000))
  if (!Number.isFinite(seconds)) return "at an unknown time"
  if (seconds < 60) return `${seconds} second${seconds === 1 ? "" : "s"} ago`
  const minutes = Math.round(seconds / 60)
  return `${minutes} minute${minutes === 1 ? "" : "s"} ago`
}

function Shell({ children }: { children: ReactNode }) {
  return (
    <div className="relative flex min-h-svh items-center justify-center bg-brand-forest p-4">
      <ThemeToggle className="absolute top-4 right-4 text-brand-cream/80 hover:bg-white/10 hover:text-brand-cream dark:hover:bg-white/10" />
      <div className="flex w-full max-w-md flex-col gap-4">
        <div className="flex flex-col items-center gap-3 pb-2 text-center">
          <img src={brand().wordmark.dark} alt={brand().name} className="h-8 w-auto" />
        </div>
        {children}
      </div>
    </div>
  )
}

function ScopeList({ scopes }: { scopes: string[] }) {
  return (
    <ul className="flex flex-col gap-2">
      {scopes.map((scope) => (
        <li key={scope} className="flex items-start gap-2 text-sm">
          <Badge variant="secondary">{scope}</Badge>
          <span className="text-muted-foreground">{SCOPE_HELP[scope] || scope}</span>
        </li>
      ))}
    </ul>
  )
}

function Notice({ title, children }: { title: string; children: ReactNode }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{title}</CardTitle>
        <CardDescription>{children}</CardDescription>
      </CardHeader>
    </Card>
  )
}

function ErrorLine({ error }: { error: string | null }) {
  if (!error) return null
  return (
    <p role="alert" className="text-sm text-destructive">
      {error}
    </p>
  )
}

function Decide({ busy, onDecide }: { busy: boolean; onDecide: (approve: boolean) => void }) {
  return (
    <div className="flex gap-2">
      <Button variant="outline" className="flex-1" disabled={busy} onClick={() => onDecide(false)}>
        Deny
      </Button>
      <Button className="flex-1" disabled={busy} onClick={() => onDecide(true)}>
        Approve
      </Button>
    </div>
  )
}

// Signs in inline when needed and refuses sessions that can't approve
// (agent tokens never carry approve), so a page renders only for a human.
function useApprover(allowTokenLogin: boolean): { gate: ReactNode | null; ready: boolean } {
  const { me, loading, can, isDemo, signOut } = useAuth()
  if (loading) return { gate: <Shell>{null}</Shell>, ready: false }
  if (!me) return { gate: <SignInForm allowTokenLogin={allowTokenLogin} demoAvailable={false} />, ready: false }
  // The deployment-protection owner can see the dashboard but never approve.
  if (me.proxy) {
    return {
      ready: false,
      gate: (
        <Shell>
          <Notice title="This session can't approve a CLI login">
            You are signed in through the deployment&apos;s access protection, which can&apos;t approve CLI logins. Sign
            out, then sign in with an account or a token to approve.
          </Notice>
          <Button variant="outline" onClick={() => void signOut()}>
            Sign out
          </Button>
        </Shell>
      ),
    }
  }
  if (!can("approve") || isDemo) {
    return {
      ready: false,
      gate: (
        <Shell>
          <Notice title="This session can't approve a CLI login">
            You are signed in with a token that lacks the approve scope. Sign out and sign in as yourself.
          </Notice>
          <Button variant="outline" onClick={() => void signOut()}>
            Sign out
          </Button>
        </Shell>
      ),
    }
  }
  return { gate: null, ready: true }
}

type AuthorizeRequest = {
  client_id: string
  client_name: string
  scopes: string[]
  redirect_uri: string
  resource?: string
  unverified: boolean
}

// Only these reach the API, which refuses any other query parameter.
const AUTHORIZE_PARAMS = ["response_type", "resource", "client_id", "redirect_uri", "code_challenge", "code_challenge_method", "state", "scope"]

export function AuthorizeConsent({ params, allowTokenLogin }: { params: URLSearchParams; allowTokenLogin: boolean }) {
  const { me } = useAuth()
  const { gate, ready } = useApprover(allowTokenLogin)
  const [request, setRequest] = useState<AuthorizeRequest | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [done, setDone] = useState<"approved" | "denied" | null>(null)
  const query = new URLSearchParams(AUTHORIZE_PARAMS.flatMap((k) => (params.has(k) ? [[k, params.get(k) as string]] : []))).toString()

  useEffect(() => {
    if (!ready) return
    let cancelled = false
    api<AuthorizeRequest>("/v1/oauth/authorize?" + query)
      .then((result) => {
        if (!cancelled) setRequest(result)
      })
      .catch((err) => {
        if (!cancelled) setError(errorText(err))
      })
    return () => {
      cancelled = true
    }
  }, [ready, query])

  if (gate) return gate

  async function decide(approve: boolean) {
    if (!request) return
    setBusy(true)
    setError(null)
    try {
      const { redirect_to } = await api<{ redirect_to: string }>("/v1/oauth/authorize", {
        method: "POST",
        body: {
          client_id: params.get("client_id") || "",
          redirect_uri: params.get("redirect_uri") || "",
          code_challenge: params.get("code_challenge") || "",
          code_challenge_method: params.get("code_challenge_method") || "",
          state: params.get("state") || "",
          scope: params.get("scope") || "",
          resource: params.get("resource") || undefined,
          approve,
        },
      })
      setDone(approve ? "approved" : "denied")
      window.location.assign(redirect_to)
    } catch (err) {
      setError(errorText(err))
      setBusy(false)
    }
  }

  if (done) {
    return (
      <Shell>
        <Notice title={done === "approved" ? (request?.unverified ? "App connected" : "CLI signed in") : "Login denied"}>
          {request?.unverified ? "Return to the app." : "Return to your terminal. You can close this tab."}
        </Notice>
      </Shell>
    )
  }

  if (!request) {
    return (
      <Shell>
        {error ? (
          <Notice title="This login request can't be approved">{error}</Notice>
        ) : (
          <Notice title="Checking the request">One moment.</Notice>
        )}
      </Shell>
    )
  }

  let target = request.redirect_uri
  try {
    target = new URL(request.redirect_uri).host
  } catch {
    // shown as-is
  }

  return (
    <Shell>
      <Card>
        <CardHeader>
          <CardTitle className="text-base">
            {request.unverified ? `Connect ${request.client_name}?` : `Sign in the ${brand().shortName} CLI?`}
          </CardTitle>
          <CardDescription>
            {request.unverified ? (
              <>
                <strong>{request.client_name}</strong> ({target}) is asking to use {brand().shortName} as{" "}
                {me?.user?.email || "your account"}. Anyone can register an app, and {brand().shortName} hasn&apos;t verified this
                one. Only approve if you just asked it to connect.
              </>
            ) : (
              <>
                <code>{request.client_id}</code> on this computer ({target}) is asking for a token for{" "}
                {me?.user?.email || "your account"}. Only approve if you just ran <code>{brand().cli} auth login</code>.
              </>
            )}
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4 pt-0">
          <ScopeList scopes={request.scopes} />
          <ErrorLine error={error} />
          <Decide busy={busy} onDecide={(approve) => void decide(approve)} />
        </CardContent>
      </Card>
    </Shell>
  )
}

type DeviceRequest = {
  user_code: string
  client_id: string
  scopes: string[]
  status: string
  created_at: string
  expires_at: string
  requested_from?: string
}

// The code is always typed by the human: a link can't pre-fill it, so a
// phished "approve this" URL carries nothing.
export function DeviceConsent({ allowTokenLogin }: { allowTokenLogin: boolean }) {
  const { gate } = useApprover(allowTokenLogin)
  const [code, setCode] = useState("")
  const [request, setRequest] = useState<DeviceRequest | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [done, setDone] = useState<string | null>(null)
  if (gate) return gate

  async function lookup(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    setError(null)
    try {
      setRequest(await api<DeviceRequest>("/v1/oauth/device?user_code=" + encodeURIComponent(code.trim())))
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  async function decide(approve: boolean) {
    if (!request) return
    setBusy(true)
    setError(null)
    try {
      const { status } = await api<{ status: string }>("/v1/oauth/device/approve", {
        method: "POST",
        body: { user_code: request.user_code, approve },
      })
      setDone(status)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  if (done) {
    return (
      <Shell>
        <Notice title={done === "approved" ? "Device approved" : "Login denied"}>
          Return to your terminal. The CLI picks this up on its next poll.
        </Notice>
      </Shell>
    )
  }

  return (
    <Shell>
      {!request ? (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Connect a device</CardTitle>
            <CardDescription>
              Type the code that <code>{brand().cli} auth login --device</code> printed in your own terminal.
            </CardDescription>
          </CardHeader>
          <CardContent className="pt-0">
            <form onSubmit={lookup} className="flex flex-col gap-4">
              <div className="flex flex-col gap-2">
                <Label htmlFor="user-code">Code</Label>
                <Input
                  id="user-code"
                  autoComplete="off"
                  autoCapitalize="characters"
                  placeholder="BCDF-GHJK"
                  className="font-mono uppercase tracking-widest"
                  required
                  value={code}
                  onChange={(event) => setCode(event.target.value)}
                />
              </div>
              <ErrorLine error={error} />
              <Button type="submit" disabled={busy || code.trim() === ""}>
                Continue
              </Button>
            </form>
          </CardContent>
        </Card>
      ) : (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Approve device {request.user_code}?</CardTitle>
            <CardDescription>
              <code>{request.client_id}</code> asked for this login {ago(request.created_at)}. Only approve if you
              started it just now and the code matches your terminal.
              {request.requested_from ? (
                <>
                  {" "}
                  Requested from <code>{request.requested_from}</code>.
                </>
              ) : null}{" "}
              The CLI gets a token with:
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-4 pt-0">
            <ScopeList scopes={request.scopes} />
            <ErrorLine error={error} />
            <Decide busy={busy} onDecide={(approve) => void decide(approve)} />
          </CardContent>
        </Card>
      )}
    </Shell>
  )
}
