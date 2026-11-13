"use client"

import { useEffect, useState, type FormEvent } from "react"
import Link from "next/link"
import { api, EXPORT_MODE } from "../lib/api-client"
import { useAuth } from "../lib/auth"
import { authClient } from "../lib/betterauth"
import { brand } from "../lib/brand"
import { Alert, AlertTitle } from "./ui/alert"
import { Button } from "./ui/button"
import { Field, FieldGroup, FieldLabel } from "./ui/field"
import { Input } from "./ui/input"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./ui/card"
import { CommandBlock } from "./CommandBlock"
import { track } from "../lib/telemetry"
import { InputOTP, InputOTPGroup, InputOTPSlot } from "./ui/input-otp"
import { ThemeToggle } from "./ThemeToggle"
import { cn } from "../lib/utils"
import { passkeyError, passkeysUsable, signInWithPasskey } from "../lib/passkeys"
import { Fingerprint } from "lucide-react"

// A failed sign-in step: the message beside the brand's art, if it has any.
function FailNote({ className, children }: { className?: string; children: React.ReactNode }) {
  return (
    <Alert variant="destructive" className={cn("items-center", className)}>
      {/* A direct child, so the Alert lays it out beside the message; the art sets its own size. */}
      {brand().failArt}
      <AlertTitle>{children}</AlertTitle>
    </Alert>
  )
}

function VerifyStep({
  email,
  password,
  onBack,
  onVerified,
}: {
  email: string
  password: string
  onBack: () => void
  onVerified: () => Promise<void>
}) {
  const [otp, setOtp] = useState("")
  const [verifying, setVerifying] = useState(false)
  const [resending, setResending] = useState(false)
  const [localError, setLocalError] = useState<string | null>(null)

  async function sendCode() {
    if (!authClient) return
    setLocalError(null)
    const { error: sendError } = await authClient.emailOtp.sendVerificationOtp({ email, type: "email-verification" })
    if (sendError) setLocalError(sendError.message || "Could not send the code")
  }

  useEffect(() => {
    // Sends the verification email once on mount; the state it sets comes
    // from that request's response, not synchronously in the effect body.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void sendCode()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  async function onResend() {
    setResending(true)
    await sendCode()
    setResending(false)
  }

  async function onSubmit(event: FormEvent) {
    event.preventDefault()
    if (!authClient) return
    setVerifying(true)
    setLocalError(null)
    try {
      const { error: verifyError } = await authClient.emailOtp.verifyEmail({ email, otp })
      if (verifyError) throw new Error(verifyError.message || "That code didn't work")
      // Verifying the code doesn't itself start a session -- sign in again
      // now that the email is confirmed.
      const result = await authClient.signIn.email({ email, password })
      if (result.error) throw new Error(result.error.message || "Sign-in failed")
      await onVerified()
    } catch (err) {
      setLocalError(err instanceof Error ? err.message : "That code didn't work")
    } finally {
      setVerifying(false)
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Enter verification code</CardTitle>
        <CardDescription>Sent to {email}</CardDescription>
      </CardHeader>
      <CardContent className="pt-0">
        {localError && <FailNote className="mb-3">{localError}</FailNote>}
        <form onSubmit={onSubmit} className="flex flex-col items-center gap-4">
          <InputOTP maxLength={6} value={otp} onChange={setOtp} autoFocus>
            <InputOTPGroup>
              {Array.from({ length: 6 }, (_, i) => (
                <InputOTPSlot key={i} index={i} />
              ))}
            </InputOTPGroup>
          </InputOTP>
          <div className="flex w-full gap-2">
            <Button type="button" variant="outline" onClick={onBack} className="flex-1">
              Back
            </Button>
            <Button type="submit" disabled={verifying || otp.length < 6} className="flex-1">
              {verifying ? "Verifying…" : "Verify"}
            </Button>
          </div>
          <button
            type="button"
            className="text-sm text-muted-foreground underline-offset-4 hover:underline disabled:opacity-50"
            disabled={resending}
            onClick={onResend}
          >
            {resending ? "Sending…" : "Resend code"}
          </button>
        </form>
      </CardContent>
    </Card>
  )
}

// Forgot password: a code by email (Neon's shared sender delivers codes, not
// links), then a new password, then a normal sign-in.
function ResetStep({
  initialEmail,
  onBack,
  onReset,
}: {
  initialEmail: string
  onBack: () => void
  onReset: (email: string, password: string) => Promise<void>
}) {
  const [email, setEmail] = useState(initialEmail)
  const [sent, setSent] = useState(false)
  const [otp, setOtp] = useState("")
  const [password, setPassword] = useState("")
  const [submitting, setSubmitting] = useState(false)
  const [localError, setLocalError] = useState<string | null>(null)

  async function sendCode(event?: FormEvent) {
    event?.preventDefault()
    if (!authClient) return
    setSubmitting(true)
    setLocalError(null)
    const { error: sendError } = await authClient.emailOtp.requestPasswordReset({ email })
    if (sendError) setLocalError(sendError.message || "Could not send the code")
    else setSent(true)
    setSubmitting(false)
  }

  async function onSubmit(event: FormEvent) {
    event.preventDefault()
    if (!authClient) return
    setSubmitting(true)
    setLocalError(null)
    try {
      const { error: resetError } = await authClient.emailOtp.resetPassword({ email, otp, password })
      if (resetError) throw new Error(resetError.message || "That code didn't work")
      // Resetting doesn't start a session; sign in with the new password.
      await onReset(email, password)
    } catch (err) {
      setLocalError(err instanceof Error ? err.message : "That code didn't work")
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Reset your password</CardTitle>
        <CardDescription>
          {sent ? `If ${email} has an account, a code is on its way.` : "We'll email you a code."}
        </CardDescription>
      </CardHeader>
      <CardContent className="pt-0">
        {localError && <FailNote className="mb-3">{localError}</FailNote>}
        {!sent ? (
          <form onSubmit={sendCode}>
            <FieldGroup className="gap-4">
              <Field>
                <FieldLabel htmlFor="reset-email">Email</FieldLabel>
                <Input
                  id="reset-email"
                  type="email"
                  autoComplete="email"
                  required
                  autoFocus
                  value={email}
                  onChange={(event) => setEmail(event.target.value)}
                />
              </Field>
              <div className="flex w-full gap-2">
                <Button type="button" variant="outline" onClick={onBack} className="flex-1">
                  Back
                </Button>
                <Button type="submit" disabled={submitting} className="flex-1">
                  {submitting ? "Sending…" : "Send code"}
                </Button>
              </div>
            </FieldGroup>
          </form>
        ) : (
          <form onSubmit={onSubmit}>
            <FieldGroup className="gap-4">
              <Field>
                <FieldLabel>Code</FieldLabel>
                <InputOTP maxLength={6} value={otp} onChange={setOtp} autoFocus>
                  <InputOTPGroup>
                    {Array.from({ length: 6 }, (_, i) => (
                      <InputOTPSlot key={i} index={i} />
                    ))}
                  </InputOTPGroup>
                </InputOTP>
              </Field>
              <Field>
                <FieldLabel htmlFor="new-password">New password</FieldLabel>
                <Input
                  id="new-password"
                  type="password"
                  autoComplete="new-password"
                  required
                  minLength={8}
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                />
              </Field>
              <div className="flex w-full gap-2">
                <Button type="button" variant="outline" onClick={() => setSent(false)} className="flex-1">
                  Back
                </Button>
                <Button type="submit" disabled={submitting || otp.length < 6} className="flex-1">
                  {submitting ? "Resetting…" : "Reset and sign in"}
                </Button>
              </div>
              <button
                type="button"
                className="text-sm text-muted-foreground underline-offset-4 hover:underline disabled:opacity-50"
                disabled={submitting}
                onClick={() => void sendCode()}
              >
                Resend code
              </button>
            </FieldGroup>
          </form>
        )}
      </CardContent>
    </Card>
  )
}

function AccountSignIn({ initialMode = "sign-in" }: { initialMode?: "sign-in" | "sign-up" }) {
  const { signIn, error } = useAuth()
  const [step, setStep] = useState<"credentials" | "verify" | "reset">("credentials")
  const [mode, setMode] = useState<"sign-in" | "sign-up">(initialMode)
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const [name, setName] = useState("")
  const [submitting, setSubmitting] = useState(false)
  const [localError, setLocalError] = useState<string | null>(null)
  // Hidden until the server says sign-ups are open (sign-ups closed
  // pauses them), so a paused deployment never offers "Create account".
  const [signupsOpen, setSignupsOpen] = useState(false)

  useEffect(() => {
    api<{ signups_open?: boolean }>("/v1/auth/providers")
      .then((result) => setSignupsOpen(result.signups_open === true))
      .catch(() => {})
  }, [])

  async function finishSignIn() {
    if (!authClient) return
    const { data, error: tokenError } = await authClient.token()
    if (tokenError || !data?.token) throw new Error("Could not mint a session token")
    await signIn(data.token)
  }

  async function onSubmit(event: FormEvent) {
    event.preventDefault()
    if (!authClient) return
    setSubmitting(true)
    setLocalError(null)
    try {
      const result =
        mode === "sign-up"
          ? await authClient.signUp.email({ email, password, name: name.trim() || email })
          : await authClient.signIn.email({ email, password })
      if (result.error) {
        const message = result.error.message || "Sign-in failed"
        if (/verif/i.test(message) || result.error.code === "EMAIL_NOT_VERIFIED") {
          if (mode === "sign-up") track("signed_up")
          setStep("verify")
          return
        }
        throw new Error(message)
      }
      if (mode === "sign-up") track("signed_up")
      await finishSignIn()
    } catch (err) {
      setLocalError(err instanceof Error ? err.message : "Sign-in failed")
    } finally {
      setSubmitting(false)
    }
  }

  if (step === "reset") {
    return (
      <ResetStep
        initialEmail={email}
        onBack={() => setStep("credentials")}
        onReset={async (resetEmail, newPassword) => {
          if (!authClient) return
          const result = await authClient.signIn.email({ email: resetEmail, password: newPassword })
          if (result.error) throw new Error(result.error.message || "Sign-in failed")
          await finishSignIn()
        }}
      />
    )
  }

  if (step === "verify") {
    return (
      <VerifyStep
        email={email}
        password={password}
        onBack={() => setStep("credentials")}
        onVerified={finishSignIn}
      />
    )
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{mode === "sign-up" ? "Create an account" : "Sign in"}</CardTitle>
      </CardHeader>
      <CardContent className="pt-0">
        {(localError || error) && <FailNote className="mb-3">{localError || error}</FailNote>}
        <form onSubmit={onSubmit}>
          <FieldGroup className="gap-4">
            {mode === "sign-up" && (
              <Field>
                <FieldLabel htmlFor="name">Name</FieldLabel>
                <Input id="name" autoComplete="name" value={name} onChange={(event) => setName(event.target.value)} />
              </Field>
            )}
            <Field>
              <FieldLabel htmlFor="email">Email</FieldLabel>
              <Input
                id="email"
                type="email"
                autoComplete="email"
                required
                value={email}
                onChange={(event) => setEmail(event.target.value)}
              />
            </Field>
            <Field>
              <div className="flex items-center justify-between">
                <FieldLabel htmlFor="password">Password</FieldLabel>
                {mode === "sign-in" && (
                  <button
                    type="button"
                    className="text-sm text-muted-foreground underline-offset-4 hover:underline"
                    onClick={() => setStep("reset")}
                  >
                    Forgot password?
                  </button>
                )}
              </div>
              <Input
                id="password"
                type="password"
                autoComplete={mode === "sign-up" ? "new-password" : "current-password"}
                required
                value={password}
                onChange={(event) => setPassword(event.target.value)}
              />
            </Field>
            <Button type="submit" disabled={submitting}>
              {mode === "sign-up" ? "Create account" : "Sign in"}
            </Button>
            {mode === "sign-in" && <PasskeySignIn />}
            {(signupsOpen || mode === "sign-up") && (
              <button
                type="button"
                className="text-sm text-muted-foreground underline-offset-4 hover:underline"
                onClick={() => setMode(mode === "sign-up" ? "sign-in" : "sign-up")}
              >
                {mode === "sign-up" ? "Already have an account? Sign in" : "New here? Create an account"}
              </button>
            )}
            <p className="text-center text-xs text-muted-foreground">
              By continuing you agree to the{" "}
              <a href="/terms" className="underline underline-offset-4 hover:text-foreground">
                Terms
              </a>{" "}
              and{" "}
              <a href="/privacy" className="underline underline-offset-4 hover:text-foreground">
                Privacy
              </a>
              .
            </p>
          </FieldGroup>
        </form>
      </CardContent>
    </Card>
  )
}

function TokenSignIn({ bare = false }: { bare?: boolean }) {
  const { signIn, error } = useAuth()
  const [token, setTokenValue] = useState("")
  const [submitting, setSubmitting] = useState(false)
  const [localError, setLocalError] = useState<string | null>(null)

  async function onSubmit(event: FormEvent) {
    event.preventDefault()
    setSubmitting(true)
    setLocalError(null)
    try {
      await signIn(token.trim())
    } catch {
      setLocalError("That token was rejected.")
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Card className={bare ? "border-0 bg-transparent shadow-none ring-0" : undefined}>
      <CardHeader>
        {!bare && <CardTitle className="text-base">Sign in with an API token</CardTitle>}
        <CardDescription>Paste the token your server printed on first start, or create one with the CLI.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4 pt-0">
        <CommandBlock command={`${brand().cli} serve\n${brand().cli} tokens create`} />
        {(localError || error) && <FailNote>{localError || error}</FailNote>}
        <form onSubmit={onSubmit}>
          <FieldGroup className="gap-4">
            <Field>
              <FieldLabel htmlFor="token">API token</FieldLabel>
              <Input
                id="token"
                type="password"
                autoComplete="off"
                required
                value={token}
                onChange={(event) => setTokenValue(event.target.value)}
              />
            </Field>
            <Button type="submit" disabled={submitting}>
              Continue
            </Button>
          </FieldGroup>
        </form>
      </CardContent>
    </Card>
  )
}

function TryDemo() {
  const { startDemo, error } = useAuth()
  const [submitting, setSubmitting] = useState(false)
  const [localError, setLocalError] = useState<string | null>(null)

  async function onClick() {
    setSubmitting(true)
    setLocalError(null)
    try {
      await startDemo()
    } catch {
      setLocalError("Could not start the demo.")
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="flex flex-col gap-2 text-center">
      {(localError || error) && <FailNote className="justify-center">{localError || error}</FailNote>}
      <Button type="button" variant="outline" disabled={submitting} onClick={onClick}>
        {submitting ? "Starting…" : "Try it without an account"}
      </Button>
      <p className="text-xs text-muted-foreground">
        A throwaway sandbox with example data. Nothing you do there touches a real account.
      </p>
    </div>
  )
}

// Shown only when the server has passkeys on and the browser can use them.
// Usernameless: the device offers its passkeys for this site.
function PasskeySignIn() {
  const { refresh } = useAuth()
  const [enabled, setEnabled] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [localError, setLocalError] = useState<string | null>(null)

  useEffect(() => {
    if (!passkeysUsable()) return
    api<{ passkeys?: boolean }>("/v1/auth/providers")
      .then((result) => setEnabled(result.passkeys === true))
      .catch(() => {})
  }, [])

  async function onClick() {
    setSubmitting(true)
    setLocalError(null)
    try {
      await signInWithPasskey()
      await refresh()
    } catch (err) {
      setLocalError(passkeyError(err, "Passkey sign-in failed"))
    } finally {
      setSubmitting(false)
    }
  }

  if (!enabled) return null
  return (
    <div className="flex flex-col gap-2">
      {localError && <FailNote>{localError}</FailNote>}
      <Button type="button" variant="outline" disabled={submitting} onClick={onClick}>
        <Fingerprint />
        {submitting ? "Waiting for your passkey…" : "Sign in with a passkey"}
      </Button>
    </div>
  )
}

function ContinueAsOwner() {
  const { continueAsOwner } = useAuth()
  const [submitting, setSubmitting] = useState(false)
  const [localError, setLocalError] = useState<string | null>(null)

  async function onClick() {
    setSubmitting(true)
    setLocalError(null)
    try {
      await continueAsOwner()
    } catch {
      setLocalError("Could not sign in as the owner.")
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="flex flex-col gap-2 text-center">
      {localError && <FailNote className="justify-center">{localError}</FailNote>}
      <Button type="button" disabled={submitting} onClick={onClick}>
        {submitting ? "Signing in…" : "Continue as owner"}
      </Button>
    </div>
  )
}

export function SignInForm({
  allowTokenLogin: initialAllowTokenLogin,
  demoAvailable: initialDemoAvailable,
  initialMode,
}: {
  allowTokenLogin: boolean
  demoAvailable: boolean
  initialMode?: "sign-in" | "sign-up"
}) {
  const [allowTokenLogin, setAllowTokenLogin] = useState(initialAllowTokenLogin)
  const [demoAvailable, setDemoAvailable] = useState(initialDemoAvailable)
  const { proxySignedOut } = useAuth()

  // Export mode has no server to compute this ahead of time — check client-side.
  useEffect(() => {
    if (!EXPORT_MODE) return
    api<{ allow_token_login: boolean; demo_available: boolean }>("/v1/auth/providers")
      .then((result) => {
        setAllowTokenLogin(result.allow_token_login)
        setDemoAvailable(result.demo_available)
      })
      .catch(() => {})
  }, [])

  // Account sign-in (multi-tenant) leads; the token form is the way in otherwise, and tucks away behind it.
  const primary = authClient ? <AccountSignIn key={initialMode} initialMode={initialMode} /> : <PasskeySignIn />
  return (
    <div className="relative flex min-h-svh flex-col bg-background text-foreground">
      <ThemeToggle className="absolute top-4 right-4" />
      <main className="flex flex-1 flex-col items-center justify-center gap-6 px-4 py-12">
        <Link href="/" aria-label={brand().name}>
          {/* eslint-disable-next-line @next/next/no-img-element -- drawn SVG wordmark, not a photo next/image needs to optimize */}
          <img src={brand().wordmark.light} alt={brand().name} className="h-11 w-auto dark:hidden" />
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src={brand().wordmark.dark} alt="" aria-hidden className="hidden h-11 w-auto dark:block" />
        </Link>
        <div className="flex flex-col items-center gap-2 text-center">
          <h1 className="font-heading text-4xl font-semibold tracking-tight text-balance">{brand().signIn.title}</h1>
          {brand().signIn.subtitle && <p className="max-w-sm text-balance text-muted-foreground">{brand().signIn.subtitle}</p>}
        </div>
        <div className="flex w-full max-w-md flex-col gap-4">
          {proxySignedOut && <ContinueAsOwner />}
          {primary}
          {allowTokenLogin &&
            (authClient ? (
              <details className="group rounded-xl border bg-card text-card-foreground open:pb-0">
                <summary className="cursor-pointer list-none px-4 py-3 text-sm text-muted-foreground select-none hover:text-foreground group-open:text-foreground [&::-webkit-details-marker]:hidden">
                  Use an API token instead
                </summary>
                <div className="px-1 pb-1">
                  <TokenSignIn bare />
                </div>
              </details>
            ) : (
              <TokenSignIn />
            ))}
          {demoAvailable && (
            <>
              <Or />
              <TryDemo />
            </>
          )}
        </div>
      </main>
      {brand().footer}
    </div>
  )
}

function Or() {
  return (
    <div className="flex items-center gap-3 text-xs text-muted-foreground" aria-hidden>
      <span className="h-px flex-1 bg-border" />
      or
      <span className="h-px flex-1 bg-border" />
    </div>
  )
}
