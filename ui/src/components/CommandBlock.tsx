"use client"

import { useState } from "react"
import { Copy, Check } from "lucide-react"

// A copyable CLI command, for help text and empty states. `wrap`
// is for prose such as an agent prompt.
export function CommandBlock({ command, wrap }: { command: string; wrap?: boolean }) {
  const [copied, setCopied] = useState(false)

  async function onCopy() {
    await navigator.clipboard.writeText(command)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  return (
    // Toned down: a muted panel a step off the page, light gray in light mode and dark gray in dark.
    <div className="flex items-start gap-3 rounded-md bg-muted px-4 py-3 font-mono text-xs text-foreground ring-1 ring-foreground/5">
      <pre className={"flex-1 overflow-x-auto " + (wrap ? "whitespace-pre-wrap" : "whitespace-pre")}>{command}</pre>
      <button
        type="button"
        onClick={onCopy}
        aria-label={wrap ? "Copy" : "Copy command"}
        className="shrink-0 rounded-sm border border-foreground/15 px-1.5 py-0.5 text-[11px] text-muted-foreground hover:bg-foreground/5 hover:text-foreground"
      >
        {copied ? <Check className="size-3" /> : <Copy className="size-3" />}
      </button>
    </div>
  )
}
