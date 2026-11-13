import { Card, CardContent, CardHeader } from "./ui/card"
import { Skeleton } from "./ui/skeleton"

export function HeaderSkeleton() {
  return (
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div className="flex items-start gap-3">
        <Skeleton className="size-10 shrink-0 rounded-md" />
        <Skeleton className="mt-1 size-9 shrink-0 rounded-full" />
        <div className="flex flex-col gap-1.5">
          <Skeleton className="h-6 w-48" />
          <Skeleton className="h-4 w-32" />
        </div>
      </div>
      <Skeleton className="h-6 w-20 rounded-full" />
    </div>
  )
}

export function AttributesSkeleton() {
  return (
    <Card>
      <CardContent className="grid grid-cols-2 gap-4 pt-6 sm:grid-cols-3">
        {Array.from({ length: 6 }, (_, i) => (
          <div key={i} className="flex flex-col gap-1.5">
            <Skeleton className="h-3 w-16" />
            <Skeleton className="h-5 w-full" />
          </div>
        ))}
      </CardContent>
    </Card>
  )
}

function CardSkeleton({ lines = 2 }: { lines?: number }) {
  return (
    <Card>
      <CardHeader>
        <Skeleton className="h-5 w-32" />
      </CardHeader>
      <CardContent className="flex flex-col gap-2">
        {Array.from({ length: lines }, (_, i) => (
          <Skeleton key={i} className="h-10 w-full" />
        ))}
      </CardContent>
    </Card>
  )
}

export function ArtifactsSkeleton() {
  return <CardSkeleton lines={2} />
}

export function EventsSkeleton() {
  return (
    <Card>
      <CardContent className="flex items-center justify-between py-4">
        <Skeleton className="h-5 w-28" />
      </CardContent>
    </Card>
  )
}
