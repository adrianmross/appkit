"use client"

import { useRouter } from "next/navigation"
import { AlertCircle } from "lucide-react"
import { Alert, AlertAction, AlertDescription } from "./ui/alert"
import { Button } from "./ui/button"

// Shown when one section's fetch fails or times out — the point of the
// whole per-section-Suspense design: this never blocks the rest of the
// page, and there's always a way out of it besides a full page refresh.
export function SectionError({ label, onRetry }: { label: string; onRetry?: () => void }) {
  const router = useRouter()
  return (
    <Alert variant="destructive">
      <AlertCircle />
      <AlertDescription>Couldn&apos;t load {label}.</AlertDescription>
      <AlertAction>
        <Button variant="outline" size="sm" onClick={onRetry ?? (() => router.refresh())}>
          Retry
        </Button>
      </AlertAction>
    </Alert>
  )
}
