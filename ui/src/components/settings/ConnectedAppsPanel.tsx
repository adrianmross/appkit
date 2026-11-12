"use client"

import { useEffect, useState } from "react"
import { CircleAlert, Plug, Unplug } from "lucide-react"
import { api } from "../../lib/api-client"
import { brand } from "../../lib/brand"
import type { Page, Token } from "../../lib/types"
import { Alert, AlertDescription } from "../ui/alert"
import { Badge } from "../ui/badge"
import { Button } from "../ui/button"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "../ui/empty"
import { FieldDescription, FieldGroup, FieldLegend, FieldSet } from "../ui/field"
import { Item, ItemActions, ItemContent, ItemDescription, ItemGroup, ItemMedia, ItemTitle } from "../ui/item"
import { Spinner } from "../ui/spinner"

// Apps that signed in through OAuth (a ChatGPT app, a Claude connector). Each
// row is one consent: a refresh only swaps its token, and disconnecting
// revokes the token and the refresh token behind it.
export function ConnectedAppsPanel() {
  const [apps, setApps] = useState<Token[] | null>(null)
  const [error, setError] = useState("")

  const load = () =>
    api<Page<Token>>("/v1/tokens?limit=100")
      .then((page) => setApps((page.data || []).filter((t) => t.client_id && !t.revoked_at)))
      .catch((err) => setError(err.message))

  useEffect(() => {
    void load()
  }, [])

  async function disconnect(id: string) {
    setError("")
    try {
      await api(`/v1/tokens/${encodeURIComponent(id)}`, { method: "DELETE" })
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not disconnect the app")
    }
  }

  return (
    <FieldGroup>
      <FieldSet>
        <FieldLegend>Connected apps</FieldLegend>
        <FieldDescription>
          Apps you approved to use {brand().shortName} as you. They never get approve access. Disconnecting one signs it out at once.
        </FieldDescription>
        {error && (
          <Alert variant="destructive">
            <CircleAlert />
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        {apps == null ? (
          <Spinner className="text-muted-foreground" />
        ) : apps.length === 0 ? (
          <Empty className="border p-4">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <Plug />
              </EmptyMedia>
              <EmptyTitle>No connected apps</EmptyTitle>
              <EmptyDescription>Apps you connect from ChatGPT or Claude show up here.</EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <ItemGroup className="gap-2">
            {apps.map((t) => (
              <Item key={t.id} variant="outline" size="sm">
                <ItemMedia variant="icon">
                  <Plug />
                </ItemMedia>
                <ItemContent className="min-w-0">
                  <ItemTitle className="max-w-full">
                    <span className="truncate">{t.name}</span>
                    {t.scopes.map((s) => (
                      <Badge key={s} variant="secondary">
                        {s}
                      </Badge>
                    ))}
                  </ItemTitle>
                  <ItemDescription className="text-xs">
                    connected {new Date(t.created_at).toLocaleDateString()} ·{" "}
                    {t.last_used_at ? "used " + new Date(t.last_used_at).toLocaleDateString() : "not used yet"}
                  </ItemDescription>
                </ItemContent>
                <ItemActions>
                  <Button size="icon-sm" variant="ghost" aria-label={`Disconnect ${t.name}`} onClick={() => disconnect(t.id)}>
                    <Unplug />
                  </Button>
                </ItemActions>
              </Item>
            ))}
          </ItemGroup>
        )}
      </FieldSet>
    </FieldGroup>
  )
}
