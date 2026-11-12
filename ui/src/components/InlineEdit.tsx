"use client"

import { useRef, useState, type KeyboardEvent } from "react"
import { CircleAlert } from "lucide-react"
import { cn } from "../lib/utils"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select"
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip"

// The one inline-edit primitive. The value is plain text that is itself
// editable, so focusing it changes nothing but a faint ring. Saves run in the
// background on blur (or Enter on one line) while the text keeps showing what
// was typed. Only a failure shows anything: a small retry mark. Escape reverts.
//
// onSave may return a promise; the caller applies the server's copy before it
// resolves. A rejection shows the retry mark.
function useBackgroundSave(value: string, onSave: (next: string) => unknown) {
  const [pending, setPending] = useState<string | null>(null)
  const [failed, setFailed] = useState(false)
  const seq = useRef(0)

  function save(next: string) {
    const n = ++seq.current
    setPending(next)
    setFailed(false)
    Promise.resolve()
      .then(() => onSave(next))
      .then(
        () => n === seq.current && setPending(null),
        () => n === seq.current && setFailed(true),
      )
  }

  // Drops a failed edit and returns what the field should show. An edit
  // still in flight is kept: it will land.
  function revert() {
    if (!failed) return pending ?? value
    seq.current++
    setPending(null)
    setFailed(false)
    return value
  }

  return { shown: pending ?? value, failed, save, revert }
}

// Negative margin cancels the padding, so the hover tint and focus ring sit
// just outside the text without moving it.
const editable =
  "-mx-1 min-w-0 cursor-text rounded-sm px-1 outline-none transition-colors hover:bg-muted/50 focus:bg-transparent focus:ring-1 focus:ring-ring/60"

function RetryMark({ onRetry }: { onRetry: () => void }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          aria-label="Not saved. Retry"
          className="mt-[0.2em] shrink-0 rounded-full text-destructive outline-none focus-visible:ring-1 focus-visible:ring-ring"
          onClick={onRetry}
        >
          <CircleAlert className="size-3.5" />
        </button>
      </TooltipTrigger>
      <TooltipContent>Not saved. Click to retry, or press Escape in the field to undo.</TooltipContent>
    </Tooltip>
  )
}

export function InlineText({
  value,
  placeholder = "—",
  onSave,
  disabled,
  multiline,
  type,
  label,
  className = "",
}: {
  value: string
  placeholder?: string
  onSave: (next: string) => unknown
  disabled?: boolean
  // Enter adds a line instead of saving.
  multiline?: boolean
  // A native date input, borderless so it reads as text.
  type?: "date"
  label?: string
  className?: string
}) {
  const textRef = useRef<HTMLDivElement>(null)
  const dateRef = useRef<HTMLInputElement>(null)
  const { shown, failed, save, revert } = useBackgroundSave(value, onSave)

  function read() {
    if (dateRef.current) return dateRef.current.value
    const el = textRef.current
    if (!el) return shown
    return multiline ? el.innerText.replace(/\n$/, "") : (el.textContent || "").replace(/\s*\n\s*/g, " ")
  }

  function write(text: string) {
    if (dateRef.current) dateRef.current.value = text
    else if (textRef.current) textRef.current.textContent = text
  }

  if (disabled) {
    return (
      <div className={cn("min-w-0 break-words", multiline && "whitespace-pre-wrap", className)}>
        {value || <span className="text-muted-foreground">{placeholder}</span>}
      </div>
    )
  }

  function onBlur() {
    const next = read()
    if (next !== shown || failed) save(next)
    // An emptied contentEditable can keep a stray <br>, which hides the placeholder.
    else if (!next) write("")
  }

  function onKeyDown(e: KeyboardEvent<HTMLElement>) {
    if (e.key === "Escape") {
      e.preventDefault()
      write(revert())
      e.currentTarget.blur()
    } else if (e.key === "Enter" && !multiline && !e.nativeEvent.isComposing) {
      e.preventDefault()
      e.currentTarget.blur()
    }
  }

  const common = { "aria-label": label, "aria-invalid": failed || undefined, onBlur, onKeyDown }

  return (
    <div className={cn("flex min-w-0 items-start gap-1", className)}>
      {type === "date" ? (
        <input
          ref={dateRef}
          type="date"
          // Uncontrolled so a half-typed date isn't reset; a new value re-mounts it.
          key={shown}
          defaultValue={shown}
          className={cn(editable, "h-[1.5em] flex-1 border-0 bg-transparent p-0 px-1")}
          {...common}
        />
      ) : (
        <div
          ref={textRef}
          role="textbox"
          aria-multiline={multiline || undefined}
          contentEditable="plaintext-only"
          suppressContentEditableWarning
          spellCheck={multiline ? undefined : false}
          data-placeholder={placeholder}
          className={cn(
            editable,
            "flex-1 break-words empty:before:text-muted-foreground empty:before:content-[attr(data-placeholder)]",
            multiline && "whitespace-pre-wrap",
          )}
          {...common}
        >
          {shown}
        </div>
      )}
      {failed && <RetryMark onRetry={() => save(read())} />}
    </div>
  )
}

export function InlineSelect({
  value,
  options,
  onSave,
  disabled,
  placeholder = "—",
  label,
  className = "",
}: {
  value: string
  options: [string, string][]
  onSave: (next: string) => unknown
  disabled?: boolean
  placeholder?: string
  label?: string
  className?: string
}) {
  const { shown, failed, save } = useBackgroundSave(value, onSave)

  if (disabled) {
    const text = options.find(([v]) => v === value)?.[1]
    return <div className={className}>{text || <span className="text-muted-foreground">{placeholder}</span>}</div>
  }

  return (
    <div className={cn("flex min-w-0 items-start gap-1", className)}>
      <Select value={shown} onValueChange={(next) => next !== shown && save(next)}>
        <SelectTrigger
          aria-label={label}
          aria-invalid={failed || undefined}
          className={cn(
            editable,
            "h-auto gap-1 border-0 bg-transparent p-0 px-1 text-[length:inherit] text-inherit shadow-none select-auto data-[size=default]:h-auto dark:bg-transparent dark:hover:bg-muted/50 aria-invalid:ring-0 data-placeholder:text-muted-foreground [&>svg]:size-3.5 [&>svg]:opacity-0 hover:[&>svg]:opacity-100 focus-visible:ring-1 focus-visible:ring-ring/60",
          )}
        >
          <SelectValue placeholder={placeholder} />
        </SelectTrigger>
        <SelectContent>
          {options.map(([v, text]) => (
            <SelectItem key={v} value={v}>
              {text}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {failed && <RetryMark onRetry={() => save(shown)} />}
    </div>
  )
}
