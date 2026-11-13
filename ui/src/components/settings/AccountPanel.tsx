"use client"

import { useCallback, useEffect, useState, type FormEvent } from "react"
import { Info, KeyRound, LogOut, Monitor, Smartphone } from "lucide-react"
import { useAuth } from "../../lib/auth"
import { authClient } from "../../lib/betterauth"
import { api } from "../../lib/api-client"
import { brand } from "../../lib/brand"
import type { Me } from "../../lib/types"
import { UserAvatar, avatarDataURL, displayName } from "../UserAvatar"
import { Alert, AlertDescription } from "../ui/alert"
import { Badge } from "../ui/badge"
import { ButtonGroup } from "../ui/button-group"
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "../ui/card"
import { Checkbox } from "../ui/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "../ui/dialog"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "../ui/field"
import { Item, ItemActions, ItemContent, ItemDescription, ItemGroup, ItemMedia, ItemTitle } from "../ui/item"
import { Button } from "../ui/button"
import { Input } from "../ui/input"
import { Spinner } from "../ui/spinner"
import { PasskeysSection } from "./PasskeysSection"

// Better Auth's default minPasswordLength; Neon Auth doesn't change it.
const MIN_PASSWORD = 8

function Msg({ ok, text }: { ok: boolean; text: string }) {
  return text ? <p className={"text-sm " + (ok ? "text-emerald-600 dark:text-emerald-400" : "text-destructive")}>{text}</p> : null
}

// How this browser signed in decides what can be managed here. Only a
// Better Auth sign-in has a password and sign-in sessions; the server marks
// it with the actor name "betterauth" (Service.ResolveTenantUser).
type Mode = "account" | "proxy" | "token"

function modeOf(me: Me | null): Mode {
  if (me?.proxy) return "proxy"
  if (authClient && me?.token.name === "betterauth") return "account"
  return "token"
}

function ProfileCard({ mode }: { mode: Mode }) {
  const { me, refresh } = useAuth()
  const [name, setName] = useState(me?.user?.name || "")
  const [email, setEmail] = useState(me?.user?.email || "")
  const [msg, setMsg] = useState({ ok: true, text: "" })

  async function patch(body: { name?: string; avatar?: string; email?: string }) {
    try {
      await api("/v1/me", { method: "PATCH", body })
      await refresh()
      setMsg({ ok: true, text: "Saved." })
    } catch (err) {
      setMsg({ ok: false, text: err instanceof Error ? err.message : "Could not save" })
    }
  }

  async function pick(file?: File) {
    if (file) await patch({ avatar: await avatarDataURL(file) })
  }

  const emailNote =
    mode === "proxy"
      ? "The platform in front of this server signs you in, so this is only how you're shown."
      : mode === "account"
        ? `Your sign-in email. ${brand().shortName} keeps your data under this address, so it can't be changed here.`
        : `The email on this ${brand().shortName} account. It can't be changed from the dashboard.`

  return (
    <Card>
      <CardHeader>
        <CardTitle>Profile</CardTitle>
        <CardDescription>How you appear in the header, on events, and in the audit log.</CardDescription>
      </CardHeader>
      <CardContent>
        <form
          onSubmit={(event: FormEvent) => {
            event.preventDefault()
            void patch(mode === "proxy" && email !== me?.user?.email ? { name, email } : { name })
          }}
          className="flex flex-col gap-4"
        >
          <Item variant="muted">
            <ItemMedia>
              <UserAvatar me={me} size="lg" />
            </ItemMedia>
            <ItemContent>
              <ItemTitle>{displayName(me)}</ItemTitle>
              <ItemDescription>Profile photo</ItemDescription>
            </ItemContent>
            <ItemActions>
              <ButtonGroup>
                <Button type="button" variant="outline" size="sm" asChild>
                  <label>
                    {me?.user?.avatar ? "Change photo" : "Upload photo"}
                    <input type="file" accept="image/*" className="sr-only" onChange={(e) => void pick(e.target.files?.[0])} />
                  </label>
                </Button>
                {me?.user?.avatar && (
                  <Button type="button" variant="outline" size="sm" onClick={() => void patch({ avatar: "" })}>
                    Remove
                  </Button>
                )}
              </ButtonGroup>
            </ItemActions>
          </Item>
          <FieldGroup className="grid gap-4 sm:grid-cols-2">
            <Field>
              <FieldLabel htmlFor="acct-name">Display name</FieldLabel>
              <Input id="acct-name" value={name} onChange={(e) => setName(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="acct-email">Email</FieldLabel>
              <Input
                id="acct-email"
                type="email"
                autoComplete="email"
                value={mode === "proxy" ? email : me?.user?.email || ""}
                readOnly={mode !== "proxy"}
                onChange={(e) => setEmail(e.target.value)}
              />
              <FieldDescription>{emailNote}</FieldDescription>
            </Field>
          </FieldGroup>
          <div className="flex items-center gap-3">
            <Button type="submit" disabled={!name.trim() || (mode === "proxy" && !email.trim())}>
              Save
            </Button>
            <Msg {...msg} />
          </div>
        </form>
      </CardContent>
    </Card>
  )
}

function ChangePasswordDialog({ open, onOpenChange, onChanged }: { open: boolean; onOpenChange: (open: boolean) => void; onChanged: (revoked: boolean) => void }) {
  const [current, setCurrent] = useState("")
  const [next, setNext] = useState("")
  const [confirm, setConfirm] = useState("")
  const [revokeOthers, setRevokeOthers] = useState(true)
  const [touched, setTouched] = useState(false)
  const [busy, setBusy] = useState(false)
  const [serverError, setServerError] = useState("")

  const tooShort = next.length > 0 && next.length < MIN_PASSWORD
  const mismatch = confirm.length > 0 && next !== confirm
  const same = next.length > 0 && next === current
  const valid = current.length > 0 && next.length >= MIN_PASSWORD && next === confirm && !same

  function close(nextOpen: boolean) {
    if (!nextOpen) {
      setCurrent("")
      setNext("")
      setConfirm("")
      setRevokeOthers(true)
      setTouched(false)
      setServerError("")
    }
    onOpenChange(nextOpen)
  }

  async function save(event: FormEvent) {
    event.preventDefault()
    setTouched(true)
    if (!valid || !authClient) return
    setBusy(true)
    setServerError("")
    // Better Auth checks currentPassword server-side; a wrong one changes nothing.
    const { error } = await authClient.changePassword({ currentPassword: current, newPassword: next, revokeOtherSessions: revokeOthers })
    setBusy(false)
    if (error) return setServerError(error.message || "Could not change the password")
    onChanged(revokeOthers)
    close(false)
  }

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={save} className="flex flex-col gap-6" noValidate>
          <DialogHeader>
            <DialogTitle>Change password</DialogTitle>
            <DialogDescription>Enter your current password to set a new one.</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field data-invalid={!!serverError || (touched && !current) || undefined}>
              <FieldLabel htmlFor="pw-current">Current password</FieldLabel>
              <Input
                id="pw-current"
                type="password"
                autoComplete="current-password"
                autoFocus
                aria-invalid={!!serverError || (touched && !current) || undefined}
                value={current}
                onChange={(e) => {
                  setCurrent(e.target.value)
                  setServerError("")
                }}
              />
              {touched && !current && <FieldError>Enter your current password.</FieldError>}
              {serverError && <FieldError>{serverError}</FieldError>}
            </Field>
            <Field data-invalid={tooShort || same || (touched && !next) || undefined}>
              <FieldLabel htmlFor="pw-new">New password</FieldLabel>
              <Input
                id="pw-new"
                type="password"
                autoComplete="new-password"
                aria-invalid={tooShort || same || (touched && !next) || undefined}
                value={next}
                onChange={(e) => setNext(e.target.value)}
              />
              {tooShort || (touched && !next) ? (
                <FieldError>Use at least {MIN_PASSWORD} characters.</FieldError>
              ) : same ? (
                <FieldError>Choose a password different from the current one.</FieldError>
              ) : (
                <FieldDescription>At least {MIN_PASSWORD} characters.</FieldDescription>
              )}
            </Field>
            <Field data-invalid={mismatch || (touched && !confirm) || undefined}>
              <FieldLabel htmlFor="pw-confirm">Confirm new password</FieldLabel>
              <Input
                id="pw-confirm"
                type="password"
                autoComplete="new-password"
                aria-invalid={mismatch || (touched && !confirm) || undefined}
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
              />
              {(mismatch || (touched && !confirm)) && <FieldError>The new passwords don&apos;t match.</FieldError>}
            </Field>
            <Field orientation="horizontal">
              <Checkbox id="pw-revoke" checked={revokeOthers} onCheckedChange={(v) => setRevokeOthers(v === true)} />
              <FieldLabel htmlFor="pw-revoke" className="font-normal">
                Sign out other sessions
              </FieldLabel>
            </Field>
          </FieldGroup>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => close(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy}>
              {busy && <Spinner />}
              Change password
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

type AuthSession = { id: string; token: string; userAgent?: string | null; ipAddress?: string | null; createdAt: Date | string }

function describeAgent(ua?: string | null) {
  if (!ua) return "Unknown device"
  const browser = /Edg\//.test(ua) ? "Edge" : /Firefox\//.test(ua) ? "Firefox" : /Chrome\//.test(ua) ? "Chrome" : /Safari\//.test(ua) ? "Safari" : "Browser"
  const os = /iPhone|iPad/.test(ua) ? "iOS" : /Android/.test(ua) ? "Android" : /Mac OS X/.test(ua) ? "macOS" : /Windows/.test(ua) ? "Windows" : /Linux/.test(ua) ? "Linux" : ""
  return os ? `${browser} on ${os}` : browser
}

// Password and sign-in sessions for a Better Auth (Neon Auth) account.
// Neon Auth's session cookie is third-party to this site, so it can be gone
// (expired, or blocked by the browser) while the app still holds a valid
// JWT; then nothing here would work, and the card says how to get it back.
function SecurityCard() {
  const [state, setState] = useState<"loading" | "ready" | "missing">("loading")
  const [currentToken, setCurrentToken] = useState("")
  const [sessions, setSessions] = useState<AuthSession[]>([])
  const [open, setOpen] = useState(false)
  const [msg, setMsg] = useState({ ok: true, text: "" })

  const load = useCallback(async () => {
    if (!authClient) return
    const { data } = await authClient.getSession()
    if (!data?.session) return setState("missing")
    setCurrentToken(data.session.token)
    const list = await authClient.listSessions()
    setSessions((list.data as AuthSession[] | null) || [])
    setState("ready")
  }, [])

  useEffect(() => {
    // Fetches from Neon Auth; state is only set after the requests resolve.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    load().catch(() => setState("missing"))
  }, [load])

  async function revoke(token: string) {
    const { error } = await authClient!.revokeSession({ token })
    setMsg(error ? { ok: false, text: error.message || "Could not sign out that session" } : { ok: true, text: "Session signed out." })
    await load()
  }

  async function revokeOthers() {
    const { error } = await authClient!.revokeOtherSessions()
    setMsg(error ? { ok: false, text: error.message || "Could not sign out other sessions" } : { ok: true, text: "Other sessions were signed out." })
    await load()
  }

  const ordered = [...sessions].sort((a, b) => Number(b.token === currentToken) - Number(a.token === currentToken))
  const hasOthers = sessions.some((s) => s.token !== currentToken)

  return (
    <Card>
      <CardHeader>
        <CardTitle>Security</CardTitle>
        <CardDescription>Your password and the browsers signed in to your account.</CardDescription>
        {state === "ready" && (
          <CardAction>
            <Button variant="outline" size="sm" onClick={() => setOpen(true)}>
              <KeyRound />
              Change password
            </Button>
          </CardAction>
        )}
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {state === "loading" && (
          <p className="flex items-center gap-2 text-sm text-muted-foreground">
            <Spinner /> Loading sessions
          </p>
        )}
        {state === "missing" && (
          <Alert>
            <Info />
            <AlertDescription>
              Your session with the sign-in service has ended. It expires on its own, and some browsers block its cookie.
              Sign out and sign back in with your email to change your password or manage sessions.
            </AlertDescription>
          </Alert>
        )}
        {state === "ready" && (
          <>
            <ItemGroup className="gap-2">
              {ordered.map((s) => {
                const isCurrent = s.token === currentToken
                return (
                  <Item key={s.id} variant="outline" size="sm">
                    <ItemMedia variant="icon">{/iPhone|iPad|Android/.test(s.userAgent || "") ? <Smartphone /> : <Monitor />}</ItemMedia>
                    <ItemContent>
                      <ItemTitle>
                        {describeAgent(s.userAgent)}
                        {isCurrent && <Badge variant="secondary">This browser</Badge>}
                      </ItemTitle>
                      <ItemDescription>
                        Signed in {new Date(s.createdAt).toLocaleString()}
                        {s.ipAddress ? ` · ${s.ipAddress}` : ""}
                      </ItemDescription>
                    </ItemContent>
                    {!isCurrent && (
                      <ItemActions>
                        <Button variant="outline" size="sm" onClick={() => void revoke(s.token)}>
                          Sign out
                        </Button>
                      </ItemActions>
                    )}
                  </Item>
                )
              })}
            </ItemGroup>
            <div className="flex flex-wrap items-center gap-3">
              {hasOthers && (
                <Button variant="outline" size="sm" onClick={() => void revokeOthers()}>
                  Sign out other sessions
                </Button>
              )}
              <Msg {...msg} />
            </div>
            <p className="text-xs text-muted-foreground">
              These are sign-in sessions. A browser already signed in to {brand().shortName} stays signed in until its session token
              expires. API tokens are managed under API tokens.
            </p>
          </>
        )}
      </CardContent>
      <ChangePasswordDialog
        open={open}
        onOpenChange={setOpen}
        onChanged={(revoked) => {
          setMsg({ ok: true, text: revoked ? "Password changed. Other sessions were signed out." : "Password changed." })
          void load()
        }}
      />
    </Card>
  )
}

function NoAccountCard({ mode }: { mode: Mode }) {
  const { me } = useAuth()
  return (
    <Card>
      <CardHeader>
        <CardTitle>Security</CardTitle>
        <CardDescription>Password and sign-in sessions.</CardDescription>
      </CardHeader>
      <CardContent>
        <Alert>
          <Info />
          <AlertDescription>
            {mode === "proxy"
              ? "The platform in front of this server signs you in, so your password and sessions are managed there."
              : `You signed in with the API token "${me?.token.name || "token"}", which has no password or sign-in sessions. Rotate or revoke it under API tokens.`}
          </AlertDescription>
        </Alert>
      </CardContent>
    </Card>
  )
}

export function AccountPanel() {
  const { me, signOut } = useAuth()
  const mode = modeOf(me)
  const via = mode === "account" ? "email and password" : mode === "proxy" ? "the platform" : `token "${me?.token.name || "token"}"`

  async function signOutEverywhere() {
    // Also end the Neon Auth session, so the next sign-in asks for the password.
    if (mode === "account") await authClient?.signOut().catch(() => {})
    await signOut()
  }

  return (
    <div className="flex flex-col gap-4">
      <ProfileCard mode={mode} />
      {mode === "account" ? <SecurityCard /> : <NoAccountCard mode={mode} />}
      <PasskeysSection />
      <Card>
        <CardHeader>
          <CardTitle>Sign out</CardTitle>
          <CardDescription>
            Signed in as {displayName(me)} via {via}
            {me?.token.scopes?.length ? ` (${me.token.scopes.join(", ")})` : ""}.
          </CardDescription>
          <CardAction>
            <Button variant="outline" size="sm" onClick={() => void signOutEverywhere()}>
              <LogOut />
              Sign out
            </Button>
          </CardAction>
        </CardHeader>
      </Card>
    </div>
  )
}
