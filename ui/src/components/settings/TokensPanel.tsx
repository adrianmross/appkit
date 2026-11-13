"use client"

import { useEffect, useState } from "react"
import { Check, CircleAlert, Copy, KeyRound, Trash2 } from "lucide-react"
import { api } from "../../lib/api-client"
import { brand } from "../../lib/brand"
import type { Page, Token } from "../../lib/types"
import { CommandBlock } from "../CommandBlock"
import { Alert, AlertDescription, AlertTitle } from "../ui/alert"
import { Badge } from "../ui/badge"
import { Button } from "../ui/button"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "../ui/empty"
import { Field, FieldDescription, FieldGroup, FieldLabel, FieldLegend, FieldSeparator, FieldSet } from "../ui/field"
import { Input } from "../ui/input"
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput } from "../ui/input-group"
import { Item, ItemActions, ItemContent, ItemDescription, ItemGroup, ItemMedia, ItemTitle } from "../ui/item"
import { Spinner } from "../ui/spinner"
import { ToggleGroup, ToggleGroupItem } from "../ui/toggle-group"

// With `server`, the exports become one copyable block with the server's variable too.
export function Secret({ value, server }: { value: string; server?: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <Alert className="border-amber-500/50 bg-amber-500/10">
      <KeyRound />
      <AlertTitle>Copy this token now</AlertTitle>
      <AlertDescription className="flex flex-col gap-2">
        It is shown once and cannot be recovered.
        <InputGroup className="bg-background">
          <InputGroupInput readOnly value={value} aria-label="New token" className="font-mono text-xs" onFocus={(e) => e.currentTarget.select()} />
          <InputGroupAddon align="inline-end">
            <InputGroupButton
              size="icon-xs"
              aria-label="Copy token"
              onClick={async () => {
                await navigator.clipboard.writeText(value)
                setCopied(true)
                setTimeout(() => setCopied(false), 1500)
              }}
            >
              {copied ? <Check /> : <Copy />}
            </InputGroupButton>
          </InputGroupAddon>
        </InputGroup>
        {server ? (
          <CommandBlock wrap command={`export ${brand().envPrefix}_SERVER=${server}\nexport ${brand().envPrefix}_TOKEN=${value}`} />
        ) : (
          <code className="text-[11px] break-all">export {brand().envPrefix}_TOKEN={value}</code>
        )}
      </AlertDescription>
    </Alert>
  )
}

function TokenList({ tokens, revoke, session }: { tokens: Token[]; revoke: (id: string) => void; session?: boolean }) {
  return (
    <ItemGroup className="gap-2">
      {tokens.map((t) => (
        <Item key={t.id} variant="outline" size="sm">
          <ItemMedia variant="icon">
            <KeyRound />
          </ItemMedia>
          <ItemContent className="min-w-0">
            <ItemTitle className="max-w-full">
              <span className="truncate">{session ? t.client || "Browser" : t.name}</span>
              {session && t.current && <Badge>This session</Badge>}
              {!session &&
                t.scopes.map((s) => (
                  <Badge key={s} variant="secondary">
                    {s}
                  </Badge>
                ))}
            </ItemTitle>
            <ItemDescription className="text-xs">
              {session ? t.ip || "unknown IP" : <code>{t.prefix}…</code>} · created {new Date(t.created_at).toLocaleDateString()} ·{" "}
              {t.last_used_at ? "used " + new Date(t.last_used_at).toLocaleDateString() : "never used"}
              {t.expires_at && !session
                ? " · " + (new Date(t.expires_at) < new Date() ? "expired " : "expires ") + new Date(t.expires_at).toLocaleDateString()
                : ""}
            </ItemDescription>
          </ItemContent>
          <ItemActions>
            <Button size="icon-sm" variant="ghost" aria-label={session ? "Sign out this session" : `Revoke ${t.name}`} onClick={() => revoke(t.id)}>
              <Trash2 />
            </Button>
          </ItemActions>
        </Item>
      ))}
    </ItemGroup>
  )
}

export function TokensPanel() {
  const [tokens, setTokens] = useState<Token[] | null>(null)
  const [name, setName] = useState("")
  const [scope, setScope] = useState("write")
  const [days, setDays] = useState("0")
  const [secret, setSecret] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)

  const load = () =>
    api<Page<Token>>("/v1/tokens?limit=100")
      .then((page) => setTokens((page.data || []).filter((t) => !t.revoked_at && !t.client_id)))
      .catch((err) => setError(err.message))

  useEffect(() => {
    void load()
  }, [])

  async function create() {
    if (!name.trim()) return
    setBusy(true)
    setError("")
    try {
      const issued = await api<{ secret: string }>("/v1/tokens", {
        method: "POST",
        body: { name: name.trim(), scopes: scope === "write" ? ["read", "write"] : ["read"], expires_in_days: Number(days) },
      })
      setSecret(issued.secret)
      setName("")
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not create the token")
    } finally {
      setBusy(false)
    }
  }

  async function revoke(id: string) {
    setError("")
    try {
      await api(`/v1/tokens/${encodeURIComponent(id)}`, { method: "DELETE" })
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not revoke the token")
    }
  }

  async function signOutOthers() {
    for (const t of sessions) if (!t.current) await revoke(t.id)
  }

  // Dashboard sign-ins mint a token named LoginTokenName; they are sessions, not keys.
  const sessions = (tokens || []).filter((t) => t.name === "login")
  const keys = (tokens || []).filter((t) => t.name !== "login")

  return (
    <FieldGroup>
      <FieldSet>
        <FieldLegend>New token</FieldLegend>
        <FieldDescription>
          Tokens let the <code>{brand().cli}</code> CLI and agents call the API as you. {brand().tokensNote}
        </FieldDescription>
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor="token-name">Name</FieldLabel>
            <Input
              id="token-name"
              placeholder="e.g. laptop CLI"
              value={name}
              maxLength={80}
              onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && void create()}
            />
          </Field>
          <Field>
            <FieldLabel>Access</FieldLabel>
            <div>
              <ToggleGroup type="single" variant="outline" value={scope} onValueChange={(v) => v && setScope(v)}>
                <ToggleGroupItem value="read">
                  Read only
                </ToggleGroupItem>
                <ToggleGroupItem value="write">
                  Read &amp; write
                </ToggleGroupItem>
              </ToggleGroup>
            </div>
          </Field>
          <Field>
            <FieldLabel>Expires</FieldLabel>
            <div>
              <ToggleGroup type="single" variant="outline" value={days} onValueChange={(v) => v && setDays(v)}>
                <ToggleGroupItem value="0">Never</ToggleGroupItem>
                <ToggleGroupItem value="30">30 days</ToggleGroupItem>
                <ToggleGroupItem value="90">90 days</ToggleGroupItem>
                <ToggleGroupItem value="365">1 year</ToggleGroupItem>
              </ToggleGroup>
            </div>
          </Field>
          <Button onClick={create} disabled={busy || !name.trim()} className="self-start">
            {busy && <Spinner />}
            Generate token
          </Button>
        </FieldGroup>
      </FieldSet>
      {secret && <Secret value={secret} />}
      {error && (
        <Alert variant="destructive">
          <CircleAlert />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      <FieldSeparator />
      <FieldSet>
        <FieldLegend variant="label">Active tokens</FieldLegend>
        {tokens == null ? (
          <Spinner className="text-muted-foreground" />
        ) : keys.length === 0 ? (
          <Empty className="border p-4">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <KeyRound />
              </EmptyMedia>
              <EmptyTitle>No active tokens</EmptyTitle>
              <EmptyDescription>Generate one above to use the CLI or an agent.</EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <TokenList tokens={keys} revoke={revoke} />
        )}
      </FieldSet>
      {sessions.length > 0 && (
        <FieldSet>
          <FieldLegend variant="label">Dashboard sign-ins</FieldLegend>
          <FieldDescription>
            Each browser sign-in has its own session and lapses after 30 days without use. Signing out ends only that one.
          </FieldDescription>
          <TokenList tokens={sessions} revoke={revoke} session />
          {sessions.some((t) => !t.current) && (
            <Button variant="outline" className="self-start" onClick={signOutOthers}>
              Sign out all other sessions
            </Button>
          )}
        </FieldSet>
      )}
    </FieldGroup>
  )
}
