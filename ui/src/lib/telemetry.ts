// Browser error reports (Sentry) and product events (PostHog) for the hosted
// dashboard. Both are off unless the build sets their key, so self-hosted
// builds load nothing and send nothing. Keys come from the build
// (NEXT_PUBLIC_SENTRY_DSN, NEXT_PUBLIC_POSTHOG_KEY, NEXT_PUBLIC_POSTHOG_HOST).
//
// What is deliberately not collected: session replay, autocapture, cookies or
// local storage for analytics, query strings (record ids live there in
// the static build), breadcrumbs, and user identity.

import type { PostHog } from "posthog-js"
import { brand } from "./brand"

const SENTRY_DSN = process.env.NEXT_PUBLIC_SENTRY_DSN ?? ""
const POSTHOG_KEY = process.env.NEXT_PUBLIC_POSTHOG_KEY ?? ""
const POSTHOG_HOST = process.env.NEXT_PUBLIC_POSTHOG_HOST || "https://us.i.posthog.com"

let posthog: PostHog | null = null
let started = false

const stripQuery = (url: unknown) => (typeof url === "string" ? url.split(/[?#]/)[0] : url)

export function initTelemetry() {
  if (started || typeof window === "undefined") return
  started = true
  if (SENTRY_DSN) {
    void import("@sentry/browser").then((Sentry) => {
      Sentry.init({
        dsn: SENTRY_DSN,
        release: `${brand().slug}@${brand().version}`,
        beforeBreadcrumb: () => null,
        beforeSend(event) {
          delete event.user
          if (event.request) event.request = { url: stripQuery(event.request.url) as string }
          return event
        },
      })
    })
  }
  if (POSTHOG_KEY) {
    void import("posthog-js").then(({ default: ph }) => {
      ph.init(POSTHOG_KEY, {
        api_host: POSTHOG_HOST,
        autocapture: false,
        capture_pageview: "history_change",
        capture_pageleave: false,
        persistence: "memory",
        disable_session_recording: true,
        advanced_disable_flags: true,
        person_profiles: "identified_only",
        ip: false,
        respect_dnt: true,
        before_send(event) {
          if (!event) return event
          const p = event.properties ?? {}
          p.$current_url = stripQuery(p.$current_url)
          delete p.$referrer
          delete p.$referring_domain
          return event
        },
      })
      posthog = ph
    })
  }
}

// track records a named product event. It is a no-op when PostHog is off.
export function track(event: string) {
  posthog?.capture(event)
}
