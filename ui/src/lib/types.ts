// The platform's API shapes: what /v1/me, /v1/tokens, /v1/audit and
// /v1/events answer in every app built on appkit.

export type Page<T> = {
  data: T[]
  page: { total: number; has_more: boolean; next_cursor?: string }
}

export type Me = {
  user?: { id?: string; name?: string; email?: string; avatar?: string; announcements_seen?: string; created_at?: string }
  token: { id?: string; name?: string; scopes?: string[] }
  // The trusted-proxy owner, who may change their email.
  proxy?: boolean
}

export type Token = {
  id: string
  name: string
  prefix: string
  scopes: string[]
  created_at: string
  last_used_at: string | null
  revoked_at: string | null
  client?: string
  ip?: string
  current?: boolean
  client_id?: string
  expires_at?: string
}

export type Audit = {
  id: string
  actor_name?: string
  action: string
  entity_type: string
  entity_id: string
  summary: string
  created_at: string
}

// One entry of the live feed. The platform fields are fixed; what the event
// is about (subject_type, subject_id) and its payload (data) are the app's,
// as are any extra top-level fields an older API still sends.
export type Event = {
  id?: string
  kind?: string
  title?: string
  body?: string
  occurred_at?: string
  actor?: { name?: string; token_id?: string }
  subject_type?: string
  subject_id?: string
  data?: Record<string, unknown>
  [field: string]: unknown
}
