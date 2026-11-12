"use client"

import { createAuthClient } from "better-auth/client"
import { emailOTPClient, jwtClient } from "better-auth/client/plugins"

// Only defined when the app is deployed with Neon Auth configured.
const baseURL = process.env.NEXT_PUBLIC_NEON_AUTH_URL

export const authClient = baseURL ? createAuthClient({ baseURL, plugins: [jwtClient(), emailOTPClient()] }) : null
