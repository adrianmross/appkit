import { Badge } from "./ui/badge"

// What a status means to the person looking at it. "action" is theirs to
// act on, "stop" ended badly, "closed" is over, "win" is the good ending.
export type StatusTone = "win" | "action" | "stop" | "closed"
export type StatusMap = Record<string, { label: string; tone?: StatusTone }>

const TONE_CLASS: Record<StatusTone, string> = {
  win: "bg-brand-gold text-[#141615]",
  action: "bg-amber-500/15 text-amber-700 dark:bg-amber-500/20 dark:text-amber-400",
  stop: "bg-destructive/10 text-destructive dark:bg-destructive/15",
  closed: "bg-muted text-muted-foreground",
}

// A status the map doesn't know is shown as it is stored, untinted.
export function StatusBadge({ status, statuses }: { status: string; statuses: StatusMap }) {
  const known = statuses[status]
  return (
    <Badge variant="secondary" className={known?.tone && TONE_CLASS[known.tone]}>
      {known?.label ?? status}
    </Badge>
  )
}
