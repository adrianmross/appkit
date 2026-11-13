"use client"

import { useCallback, useEffect, useRef, useState, type ReactNode } from "react"
import { usePathname, useRouter } from "next/navigation"
import { brand } from "../lib/brand"
import { setPref } from "../lib/settings"
import { usePref } from "../lib/use-pref"
import { Button } from "./ui/button"

// A step points at [data-tour=target] on a page matching `match`; `route` is
// where Next goes to get there. With `advanceOn`, the step also moves on by
// itself when the visitor navigates to a matching page (opening a record
// from a table, say).
export type TourStep = { target: string; route: string; match: RegExp; title: string; body: string; hint?: string; advanceOn?: RegExp }

export type TourConfig = {
  // The steps, or a function that works them out first: which record to
  // open, which steps an empty account has to skip.
  steps: TourStep[] | (() => Promise<TourStep[]>)
  // The welcome dialog's line under the wordmark, and the drawing above it.
  welcome: { body: string; art?: ReactNode }
}

const PAD = 6
// Everything fades: out before a step changes, in once the page is ready and the target is found.
const FADE = 220
// A target that never shows up (a missing card) gets a centered note instead.
const GIVE_UP = 3000

export function TourOverlay({ steps: source, welcome }: TourConfig) {
  const router = useRouter()
  const pathname = usePathname()
  const [steps, setSteps] = useState<TourStep[] | null>(Array.isArray(source) ? source : null)
  const [i, setI] = useState(0)
  const [box, setBox] = useState<DOMRect | null>(null)
  const [shown, setShown] = useState(false)
  const [missed, setMissed] = useState(false)
  const prev = useRef(pathname)
  // Replay ("go") skips the welcome; a first join shows it.
  const [started, setStarted] = useState(usePref("tour") === "go")

  useEffect(() => {
    if (Array.isArray(source)) return
    // A tour that can't be worked out has nothing to show; it ends quietly.
    source().then(setSteps, () => setSteps([]))
    // eslint-disable-next-line react-hooks/exhaustive-deps -- once per mount
  }, [])

  const step = started ? (steps?.[i] ?? null) : null
  const here = step ? step.match.test(pathname) : false

  // Fade out, then act: the next step, or the end.
  const go = useCallback(
    (n: number) => {
      setShown(false)
      setTimeout(() => setI(Math.max(0, Math.min(n, (steps?.length ?? 1) - 1))), FADE)
    },
    [steps],
  )
  const end = useCallback(() => {
    setShown(false)
    setTimeout(() => setPref("tour", "off"), FADE)
  }, [])

  // Take the visitor to the step's page, unless they are already on a matching one.
  useEffect(() => {
    if (step && !here) router.push(step.route)
  }, [step, here, router])

  // Going where the step asked by hand moves the tour on.
  useEffect(() => {
    const on = step?.advanceOn
    const came = !!on && prev.current !== pathname && on.test(pathname) && !on.test(prev.current)
    prev.current = pathname
    if (came) go(i + 1)
  }, [pathname, step, i, go])

  // Find the target (it may mount late), then follow it through scroll, resize and reflow.
  useEffect(() => {
    if (!step || !here) return
    let el: Element | null = null
    let raf = 0
    let obs: ResizeObserver | undefined
    let watch: MutationObserver | undefined
    const measure = () => {
      raf = 0
      if (el) setBox(el.getBoundingClientRect())
    }
    const schedule = () => {
      if (!raf) raf = requestAnimationFrame(measure)
    }
    const attach = (found: Element) => {
      el = found
      watch?.disconnect()
      found.scrollIntoView({ block: "center", behavior: "smooth" })
      obs = new ResizeObserver(schedule)
      obs.observe(found)
      window.addEventListener("scroll", schedule, true)
      window.addEventListener("resize", schedule)
      schedule()
    }
    const giveUp = setTimeout(() => setMissed(true), GIVE_UP)
    const sel = `[data-tour="${step.target}"]`
    const now = document.querySelector(sel)
    if (now) attach(now)
    else {
      watch = new MutationObserver(() => {
        const found = document.querySelector(sel)
        if (found) attach(found)
      })
      watch.observe(document.body, { childList: true, subtree: true })
    }
    return () => {
      setBox(null)
      setShown(false)
      setMissed(false)
      clearTimeout(giveUp)
      cancelAnimationFrame(raf)
      obs?.disconnect()
      watch?.disconnect()
      window.removeEventListener("scroll", schedule, true)
      window.removeEventListener("resize", schedule)
    }
  }, [step, here])

  // Fade in once the target (or the note that stands in for it) is ready.
  const ready = box !== null || missed
  useEffect(() => {
    if (!ready) return
    let b = 0
    const a = requestAnimationFrame(() => (b = requestAnimationFrame(() => setShown(true))))
    return () => {
      cancelAnimationFrame(a)
      cancelAnimationFrame(b)
    }
  }, [ready, step])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") end()
      else if (!started) return
      else if (e.key === "ArrowRight") go(i + 1)
      else if (e.key === "ArrowLeft") go(i - 1)
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [i, go, end, started])

  if (!started) return <Welcome {...welcome} onStart={() => setStarted(true)} onSkip={() => setPref("tour", "off")} />
  if (!step || !steps || !ready) return null
  const last = i === steps.length - 1
  // Beside the target, below it when there is room and above otherwise; centered when it isn't on the page.
  const place: React.CSSProperties = box
    ? {
        left: Math.min(Math.max(16, box.left - PAD), Math.max(16, window.innerWidth - 336)),
        ...(box.bottom + 220 < window.innerHeight ? { top: box.bottom + PAD + 12 } : { bottom: Math.max(16, window.innerHeight - box.top + PAD + 12) }),
      }
    : { left: "50%", top: "40%", transform: "translateX(-50%)" }
  return (
    <div className="pointer-events-none fixed inset-0 z-[100] transition-opacity duration-300 motion-reduce:transition-none" style={{ opacity: shown ? 1 : 0 }}>
      {/* The dim is the hole's shadow, so the highlighted page stays clickable. The whole layer fades; nothing slides. */}
      {box ? (
        <div
          className="absolute rounded-lg ring-2 ring-primary"
          style={{ top: box.top - PAD, left: box.left - PAD, width: box.width + PAD * 2, height: box.height + PAD * 2, boxShadow: "0 0 0 9999px rgb(0 0 0 / 0.5)" }}
        />
      ) : (
        <div className="absolute inset-0 bg-black/50" />
      )}
      <div
        role="dialog"
        aria-label={step.title}
        className="pointer-events-auto absolute w-80 max-w-[calc(100vw-2rem)] rounded-lg border bg-popover p-4 text-popover-foreground shadow-lg"
        style={place}
      >
        <h2 className="font-medium">{step.title}</h2>
        <p className="mt-1 text-sm text-muted-foreground">{step.body}</p>
        {step.hint && <p className="mt-2 text-sm font-medium">{step.hint}</p>}
        <div className="mt-3 flex items-center gap-2">
          <div className="flex gap-1" aria-label={`Step ${i + 1} of ${steps.length}`}>
            {steps.map((s, n) => (
              <button
                key={s.target}
                type="button"
                aria-label={`Go to step ${n + 1}`}
                onClick={() => go(n)}
                className={`size-1.5 rounded-full transition-colors ${n === i ? "bg-primary" : "bg-muted-foreground/30 hover:bg-muted-foreground/60"}`}
              />
            ))}
          </div>
          <Button variant="ghost" size="sm" className="ml-auto" onClick={end}>
            Skip
          </Button>
          {i > 0 && (
            <Button variant="outline" size="sm" onClick={() => go(i - 1)}>
              Back
            </Button>
          )}
          <Button size="sm" autoFocus onClick={() => (last ? end() : go(i + 1))}>
            {last ? "Done" : "Next"}
          </Button>
        </div>
      </div>
    </div>
  )
}

function Welcome({ body, art, onStart, onSkip }: TourConfig["welcome"] & { onStart: () => void; onSkip: () => void }) {
  const [shown, setShown] = useState(false)
  useEffect(() => {
    const id = requestAnimationFrame(() => setShown(true))
    return () => cancelAnimationFrame(id)
  }, [])
  const leave = (then: () => void) => {
    setShown(false)
    setTimeout(then, FADE)
  }
  return (
    <div className="fixed inset-0 z-[100] flex items-center justify-center bg-black/50 p-4 transition-opacity duration-300 motion-reduce:transition-none" style={{ opacity: shown ? 1 : 0 }}>
      <div role="dialog" aria-modal="true" aria-labelledby="tour-welcome" className="w-full max-w-md rounded-xl border bg-popover p-6 text-center text-popover-foreground shadow-xl">
        {art}
        <h2 id="tour-welcome" className="mt-2 flex flex-col items-center gap-2">
          <span className="text-sm text-muted-foreground">Welcome to</span>
          {/* eslint-disable-next-line @next/next/no-img-element -- drawn SVG wordmark, not a photo next/image needs to optimize */}
          <img src={brand().wordmark.light} alt={brand().name} className="h-7 w-auto dark:hidden" />
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src={brand().wordmark.dark} alt="" aria-hidden className="hidden h-7 w-auto dark:block" />
        </h2>
        <p className="mt-4 text-sm text-muted-foreground">{body}</p>
        <div className="mt-5 flex flex-col gap-2 sm:flex-row-reverse">
          <Button className="flex-1" autoFocus onClick={() => leave(onStart)}>
            Take the tutorial
          </Button>
          <Button variant="outline" className="flex-1" onClick={() => leave(onSkip)}>
            Skip for now
          </Button>
        </div>
        <p className="mt-3 text-xs text-muted-foreground">You can come back to it any time in Settings › About.</p>
      </div>
    </div>
  )
}
