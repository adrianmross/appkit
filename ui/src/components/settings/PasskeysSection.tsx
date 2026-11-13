"use client"

import { useEffect, useState, type FormEvent } from "react"
import { CircleAlert, Fingerprint, Trash2 } from "lucide-react"
import { api } from "../../lib/api-client"
import type { Page } from "../../lib/types"
import { addPasskey, passkeyError, passkeysUsable, type Passkey } from "../../lib/passkeys"
import { ConfirmDialog } from "../ConfirmDialog"
import { Alert, AlertDescription } from "../ui/alert"
import { Badge } from "../ui/badge"
import { Button } from "../ui/button"
import { Field, FieldDescription, FieldLegend, FieldSet } from "../ui/field"
import { Input } from "../ui/input"
import { Item, ItemActions, ItemContent, ItemDescription, ItemGroup, ItemMedia, ItemTitle } from "../ui/item"
import { Spinner } from "../ui/spinner"

// Passkeys sign this account in without a password. Hidden unless the
// server has them on (a WebAuthn RP ID is set) and this browser can use them.
export function PasskeysSection() {
  const [enabled, setEnabled] = useState(false)
  const [keys, setKeys] = useState<Passkey[] | null>(null)
  const [name, setName] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [removing, setRemoving] = useState<Passkey | null>(null)

  const load = () =>
    api<Page<Passkey>>("/v1/passkeys")
      .then((page) => setKeys(page.data || []))
      .catch((err) => setError(err.message))

  useEffect(() => {
    if (!passkeysUsable()) return
    api<{ passkeys?: boolean }>("/v1/auth/providers")
      .then((result) => {
        if (!result.passkeys) return
        setEnabled(true)
        void load()
      })
      .catch(() => {})
  }, [])

  async function add(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    setError("")
    try {
      await addPasskey(name.trim())
      setName("")
      await load()
    } catch (err) {
      setError(passkeyError(err, "Could not add the passkey"))
    } finally {
      setBusy(false)
    }
  }

  async function remove() {
    if (!removing) return
    setBusy(true)
    setError("")
    try {
      await api(`/v1/passkeys/${encodeURIComponent(removing.id)}`, { method: "DELETE" })
      setRemoving(null)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not remove the passkey")
    } finally {
      setBusy(false)
    }
  }

  if (!enabled) return null
  return (
    <FieldSet>
      <FieldLegend variant="label">Passkeys</FieldLegend>
      <FieldDescription>
        Sign in with your device&apos;s fingerprint, face, or screen lock instead of a password. Your password or API token still works
        if you lose a device.
      </FieldDescription>
      <form onSubmit={add}>
        <Field orientation="horizontal">
          <Input
            aria-label="Passkey name"
            placeholder="e.g. MacBook"
            value={name}
            maxLength={80}
            onChange={(e) => setName(e.target.value)}
          />
          <Button type="submit" disabled={busy || !name.trim()}>
            {busy ? <Spinner /> : <Fingerprint />}
            Add passkey
          </Button>
        </Field>
      </form>
      {error && (
        <Alert variant="destructive">
          <CircleAlert />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      {keys == null ? (
        <Spinner className="text-muted-foreground" />
      ) : keys.length === 0 ? (
        <p className="text-sm text-muted-foreground">No passkeys yet.</p>
      ) : (
        <ItemGroup className="gap-2">
          {keys.map((k) => (
            <Item key={k.id} variant="outline" size="sm">
              <ItemMedia variant="icon">
                <Fingerprint />
              </ItemMedia>
              <ItemContent className="min-w-0">
                <ItemTitle className="max-w-full">
                  <span className="truncate">{k.name}</span>
                  {k.synced && <Badge variant="secondary">synced</Badge>}
                </ItemTitle>
                <ItemDescription className="text-xs">
                  added {new Date(k.created_at).toLocaleDateString()} ·{" "}
                  {k.last_used_at ? "used " + new Date(k.last_used_at).toLocaleDateString() : "never used"}
                </ItemDescription>
              </ItemContent>
              <ItemActions>
                <Button size="icon-sm" variant="ghost" aria-label={`Remove ${k.name}`} onClick={() => setRemoving(k)}>
                  <Trash2 />
                </Button>
              </ItemActions>
            </Item>
          ))}
        </ItemGroup>
      )}
      <ConfirmDialog
        open={removing != null}
        onOpenChange={(open) => !open && setRemoving(null)}
        title={`Remove ${removing?.name ?? "this passkey"}?`}
        description="It will no longer sign you in. Sessions it already started stay signed in; revoke those under API tokens."
        confirmLabel="Remove"
        danger
        busy={busy}
        onConfirm={() => void remove()}
      />
    </FieldSet>
  )
}
