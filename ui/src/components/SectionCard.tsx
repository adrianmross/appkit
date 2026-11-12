"use client"

import { useEffect, useState, type ReactNode } from "react"
import { ChevronDown } from "lucide-react"
import { storageKey } from "../lib/brand"
import { cn } from "../lib/utils"
import { Card, CardContent, CardDescription, CardTitle } from "./ui/card"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "./ui/collapsible"

const key = (id: string) => storageKey(`section.${id}`)

function remembered(id: string): boolean | null {
  try {
    const v = localStorage.getItem(key(id))
    return v == null ? null : v === "1"
  } catch {
    return null
  }
}

// A detail-page card that folds away like History. Whether each section is
// open is remembered per browser; until then it opens when it has content.
export function SectionCard({
  id,
  title,
  summary,
  description,
  defaultOpen = true,
  className,
  actions,
  children,
}: {
  id: string
  title: string
  summary?: ReactNode
  actions?: ReactNode
  description?: ReactNode
  defaultOpen?: boolean
  className?: string
  children: ReactNode
}) {
  const [open, setOpen] = useState(defaultOpen)
  useEffect(() => {
    // Read after hydration so the server's render matches the first client one.
    const saved = remembered(id)
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (saved != null) setOpen(saved)
  }, [id])
  function toggle(next: boolean) {
    setOpen(next)
    try {
      localStorage.setItem(key(id), next ? "1" : "0")
    } catch {
      // Private windows: the section just won't stay folded.
    }
  }
  return (
    <Collapsible open={open} onOpenChange={toggle}>
      <Card id={id} className={cn("scroll-mt-4", className)}>
        {/* Actions sit outside the trigger (no button inside a button), just left of the chevron, and only while open. */}
        <div className="flex items-center gap-1 px-(--card-spacing)">
          <CollapsibleTrigger className="flex min-w-0 flex-1 items-baseline justify-between gap-3 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring">
            <span className="flex min-w-0 flex-col gap-1">
              <CardTitle className="text-base">{title}</CardTitle>
              {description && open && <CardDescription>{description}</CardDescription>}
            </span>
            <span className="shrink-0 text-xs text-muted-foreground">{summary}</span>
          </CollapsibleTrigger>
          {actions && open && <span className="flex shrink-0 items-center duration-200 animate-in fade-in">{actions}</span>}
          <CollapsibleTrigger aria-label={open ? `Collapse ${title}` : `Expand ${title}`} className="-mr-1 rounded-sm p-1 text-muted-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring">
            <ChevronDown className={cn("size-4 transition-transform", !open && "-rotate-90")} />
          </CollapsibleTrigger>
        </div>
        <CollapsibleContent>
          <CardContent className="flex flex-col gap-3">{children}</CardContent>
        </CollapsibleContent>
      </Card>
    </Collapsible>
  )
}
