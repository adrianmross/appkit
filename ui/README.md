# @adrianmross/appkit-ui

The dashboard pieces every appkit app shares: the shadcn components, sign-in, the CLI consent screens, the settings panels, and the API client under them. It expects the app's Go server to serve appkit's platform routes (`/v1/me`, `/v1/tokens`, `/v1/audit`, `/v1/passkeys`, `/v1/oauth`, `/v1/auth/*`, `/v1/events`).

## Using it in a Next app

The package ships TypeScript source, not a build. The app compiles it:

```ts
// next.config.ts
const nextConfig = { transpilePackages: ["@adrianmross/appkit-ui"] }
```

Import the theme once, from the app's global stylesheet. It brings Tailwind, the fonts and the design tokens, and tells Tailwind to scan this package for class names. Override any token after the import.

```css
@import "@adrianmross/appkit-ui/theme.css";
```

Name the product once, in a module that both the root layout and the client providers import, so it runs on the server and in the browser before anything renders:

```ts
// lib/brand.ts
import { configure } from "@adrianmross/appkit-ui"

configure({
  name: "Acme Tracker",
  shortName: "Acme",
  slug: "acme",
  cli: "acme",
  envPrefix: "ACME",
  apiUrl: process.env.NEXT_PUBLIC_ACME_API_URL || "",
  serverApiUrl: process.env.ACME_API_URL || "http://127.0.0.1:8787",
})
```

`slug` decides the localStorage keys (`acme.token`, `acme.keys`) and the hosted build's token cookie (`acme_token`, `__Host-acme_token` in production). See `src/lib/brand.ts` for every field.

Two entry points:

- `@adrianmross/appkit-ui`: components, hooks and the browser API client.
- `@adrianmross/appkit-ui/server`: `serverApi`, `getMe`, the route handlers (`proxyRoute`, `signInRoute`, `signOutRoute`, `demoRoute`) and `authProxy` for `proxy.ts`. These read cookies, so import them only from server code.

Build-time switches the package reads directly: `NEXT_PUBLIC_EXPORT_MODE=1` (static export served by the Go binary), `NEXT_PUBLIC_NEON_AUTH_URL`, `NEXT_PUBLIC_SENTRY_DSN`, `NEXT_PUBLIC_POSTHOG_KEY`, `NEXT_PUBLIC_POSTHOG_HOST`.

## Working on it

```sh
npm install
```

```sh
npm run typecheck
```

```sh
npm test
```
