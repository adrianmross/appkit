"use client"

// Passkey ceremonies against the Go server's WebAuthn routes. The browser's
// own JSON helpers (PublicKeyCredential.parse*FromJSON and toJSON) do the
// base64url work, so there is no WebAuthn library here.
import { api, ApiError, cookieSession } from "./api-client"

export type Passkey = {
  id: string
  name: string
  created_at: string
  last_used_at: string | null
  synced: boolean
}

type Begin<T> = { publicKey: T }

// Passkey sign-in sets the server's httpOnly session cookie, so it needs the
// dashboard and API on one origin, and a browser with the JSON helpers.
export function passkeysUsable(): boolean {
  return (
    cookieSession() &&
    typeof window !== "undefined" &&
    typeof PublicKeyCredential !== "undefined" &&
    typeof PublicKeyCredential.parseCreationOptionsFromJSON === "function" &&
    typeof PublicKeyCredential.parseRequestOptionsFromJSON === "function"
  )
}

// Turns a cancelled prompt or a refused passkey into one line for the user.
export function passkeyError(err: unknown, fallback: string): string {
  if (err instanceof DOMException && err.name === "NotAllowedError") return "Cancelled, or no passkey for this site on this device."
  if (err instanceof DOMException && err.name === "InvalidStateError") return "This device already has a passkey for your account."
  if (err instanceof ApiError || err instanceof Error) return err.message || fallback
  return fallback
}

export async function addPasskey(name: string): Promise<Passkey> {
  const begin = await api<Begin<PublicKeyCredentialCreationOptionsJSON>>("/v1/passkeys/register/begin", { method: "POST", body: {} })
  const credential = (await navigator.credentials.create({
    publicKey: PublicKeyCredential.parseCreationOptionsFromJSON(begin.publicKey),
  })) as PublicKeyCredential | null
  if (!credential) throw new Error("No passkey was created.")
  return api<Passkey>("/v1/passkeys/register/finish", { method: "POST", body: { name, credential: credential.toJSON() } })
}

// Usernameless: the authenticator offers the passkeys it holds for this
// site. On success the server has set the session cookie.
export async function signInWithPasskey(): Promise<void> {
  const begin = await api<Begin<PublicKeyCredentialRequestOptionsJSON>>("/v1/auth/passkey/login/begin", { method: "POST", body: {} })
  const credential = (await navigator.credentials.get({
    publicKey: PublicKeyCredential.parseRequestOptionsFromJSON(begin.publicKey),
  })) as PublicKeyCredential | null
  if (!credential) throw new Error("No passkey was chosen.")
  await api("/v1/auth/passkey/login/finish", { method: "POST", body: credential.toJSON() })
}
