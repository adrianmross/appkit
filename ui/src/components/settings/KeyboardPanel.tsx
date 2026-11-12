"use client"

import { Fragment } from "react"
import { Keyboard } from "lucide-react"
import { BINDINGS, keyLabels, useKeyPrefs } from "../../lib/keybindings"
import { cn } from "../../lib/utils"
import { FieldLegend, FieldSet } from "../ui/field"
import { Item, ItemActions, ItemContent, ItemDescription, ItemGroup, ItemMedia, ItemSeparator, ItemTitle } from "../ui/item"
import { Kbd, KbdGroup } from "../ui/kbd"
import { Switch } from "../ui/switch"

export function Keys({ combo }: { combo: string }) {
  return (
    <span className="flex flex-wrap items-center justify-end gap-1.5">
      {combo.split("|").map((alt, n) => (
        <Fragment key={n}>
          {n > 0 && <span className="text-xs text-muted-foreground">or</span>}
          {alt.split(" ").map((step, i) => (
            <Fragment key={i}>
              {i > 0 && <span className="text-xs text-muted-foreground">then</span>}
              <KbdGroup>
                {keyLabels(step).map((k) => (
                  <Kbd key={k}>{k}</Kbd>
                ))}
              </KbdGroup>
            </Fragment>
          ))}
        </Fragment>
      ))}
    </span>
  )
}

export function KeyboardPanel() {
  const { master, isOn, setMaster, setOn } = useKeyPrefs()
  const groups = [...new Set(BINDINGS.map((b) => b.group))]
  return (
    <div className="flex flex-col gap-6">
      <Item variant="outline">
        <ItemMedia variant="icon">
          <Keyboard />
        </ItemMedia>
        <ItemContent>
          <ItemTitle>Keyboard shortcuts</ItemTitle>
          <ItemDescription>
            Turn everything off at once. Single-key shortcuts never fire while you are typing in a field.
          </ItemDescription>
        </ItemContent>
        <ItemActions>
          <Switch checked={master} onCheckedChange={setMaster} aria-label="Enable keyboard shortcuts" />
        </ItemActions>
      </Item>
      {groups.map((group) => (
        <FieldSet key={group} className="gap-2">
          <FieldLegend variant="label">{group}</FieldLegend>
          <ItemGroup className="gap-0 rounded-lg border">
            {BINDINGS.filter((b) => b.group === group).map((b, i) => (
              <Fragment key={b.id}>
                {i > 0 && <ItemSeparator className="my-0" />}
                <Item size="sm" className={cn("gap-4", !master && "opacity-50")}>
                  <ItemContent>
                    <ItemTitle className="font-normal">{b.label}</ItemTitle>
                  </ItemContent>
                  <ItemActions className="gap-4">
                    <Keys combo={b.combo} />
                    <Switch checked={isOn(b.id)} disabled={!master} onCheckedChange={(on) => setOn(b.id, on)} aria-label={`Enable: ${b.label}`} />
                  </ItemActions>
                </Item>
              </Fragment>
            ))}
          </ItemGroup>
        </FieldSet>
      ))}
    </div>
  )
}
