import type { ReactNode } from "react"

// Everything that names the product. The app calls configure() once, in a
// module both its root layout and its client providers import, before
// anything renders. Nothing in this package hardcodes a product name.
export type Brand = {
  /** Shown in titles and image alt text: "Acme Tracker". */
  name: string
  /** Used inside sentences: "Sign in the Acme CLI?". */
  shortName: string
  /** What the user's records are called, inside a sentence: "keeps your applications". */
  records: string
  /** Prefix of localStorage keys (`slug.token`) and cookies (`slug_token`). */
  slug: string
  /** The CLI binary, for commands the dashboard shows. */
  cli: string
  /** Prefix of the environment variables in copyable commands: `PREFIX_TOKEN`. */
  envPrefix: string
  /** Release name sent to error reporting, with `slug@` in front. */
  version: string
  /** Browser to API. "" means the dashboard and the API share an origin. */
  apiUrl: string
  /** The Next server to the API (hosted build only). */
  serverApiUrl: string
  /** Image URLs. `dark` is the one shown on dark backgrounds. */
  wordmark: { light: string; dark: string }
  mark: { light: string; dark: string }
  /** What each token scope allows, in the words of the consent screen. A scope not listed shows as its name. */
  scopes: Record<string, string>
  /** The sign-in page's headline and the line under it. */
  signIn: { title: string; subtitle: string }
  /** Rendered under every page that has a footer. */
  footer?: ReactNode
  /** Drawn beside a failed sign-in step, in place of the default icon. */
  failArt?: ReactNode
  /** Follows the first sentence of the API tokens panel, for what is particular to the app's tokens. */
  tokensNote?: ReactNode
}

let current: Brand = {
  name: "App",
  shortName: "App",
  records: "data",
  slug: "app",
  cli: "app",
  envPrefix: "APP",
  version: "0.0.0",
  apiUrl: "",
  serverApiUrl: "http://127.0.0.1:8787",
  wordmark: { light: "/brand/wordmark.svg", dark: "/brand/wordmark-reverse.svg" },
  mark: { light: "/brand/mark.svg", dark: "/brand/mark-reverse.svg" },
  scopes: { read: "See your data and settings.", write: "Create and change your data. This cannot approve anything." },
  signIn: { title: "Sign in", subtitle: "" },
}

export function configure(brand: Partial<Brand>) {
  current = { ...current, ...brand }
}

export const brand = () => current

/** localStorage key for `name`: "token" -> "acme.token". */
export const storageKey = (name: string) => `${current.slug}.${name}`

/**
 * The hosted build's httpOnly token cookie. The __Host- prefix makes browsers
 * refuse the cookie unless it is Secure, has Path=/ and sets no Domain, so a
 * subdomain or a plain-http response cannot plant or overwrite it. It needs
 * HTTPS, so it is production only.
 */
export const tokenCookie = (production = process.env.NODE_ENV === "production") =>
  (production ? "__Host-" : "") + `${current.slug}_token`
