import { safeReturnTo } from "../../lib/utils";
import type { PublicConfig } from "../../types";

// The two markers below need different lifetimes, so they live in different
// storages.
//
// "Already attempted" is about one browsing context: a fresh tab should try
// again, while a reload after a refusal must not bounce, so it belongs in
// sessionStorage.
//
// "Signed out" is a decision the person made about this browser. Keeping it in
// sessionStorage would let the very next tab sign them straight back in from
// the still-live provider session, which is no sign-out at all, so it belongs
// in localStorage and is lifted only when a session exists again.
const ATTEMPTED_KEY = "appstore.sso.silentAttempted";
const SIGNED_OUT_KEY = "appstore.sso.signedOut";

type Scope = "tab" | "browser";

function store(scope: Scope): Storage {
  return scope === "tab" ? window.sessionStorage : window.localStorage;
}

// Screens that must never start a silent attempt: the login screen and the
// error screens are where a loop would come from, and everything below
// /api, /mcp and friends is not a browser page at all.
const EXCLUDED_PATHS = ["/login", "/403", "/admin/bootstrap"];
const EXCLUDED_PREFIXES = [
  "/api/",
  "/mcp",
  "/health",
  "/readyz",
  "/docs",
  "/openapi.json",
  "/momento/",
];

function readFlag(scope: Scope, key: string): boolean {
  try {
    return store(scope).getItem(key) === "true";
  } catch {
    // Private modes and blocked site data throw. Reading that as "already
    // attempted" is the safe answer: the alternative is a redirect loop.
    return true;
  }
}

function writeFlag(scope: Scope, key: string, value: boolean) {
  try {
    if (value) store(scope).setItem(key, "true");
    else store(scope).removeItem(key);
  } catch {
    // Nothing to do; readFlag already fails closed.
  }
}

/**
 * Records a deliberate sign-out, which suppresses silent sign-in until the
 * person signs in again. The provider session usually outlives this app's
 * session, so without this marker the next tab would hand them right back a
 * session they just ended.
 */
export function markSignedOut() {
  writeFlag("browser", SIGNED_OUT_KEY, true);
  writeFlag("tab", ATTEMPTED_KEY, true);
}

/** Lifts the suppression once a session exists again. */
export function clearSilentSsoState() {
  writeFlag("browser", SIGNED_OUT_KEY, false);
  writeFlag("tab", ATTEMPTED_KEY, false);
}

export function isSilentSsoPath(pathname: string): boolean {
  if (EXCLUDED_PATHS.includes(pathname)) return false;
  return !EXCLUDED_PREFIXES.some((prefix) => pathname.startsWith(prefix));
}

/**
 * Decides whether to try signing in without showing a login screen.
 *
 * prompt=none either answers with a code at once or comes back with
 * login_required, so trying it on every page load would bounce the browser
 * between the provider and this app forever. Three guards stop that: one
 * attempt per tab session, no attempt after a deliberate sign-out, and the
 * sso=none marker the callback leaves in the address when it was refused.
 */
export function shouldAttemptSilentSso({
  config,
  pathname,
  search,
}: {
  config: PublicConfig | undefined;
  pathname: string;
  search: string;
}): boolean {
  if (!config?.oidcEnabled || !config.oidcConfigured || !config.oidcAutoLogin)
    return false;
  if (!isSilentSsoPath(pathname)) return false;
  const marker = new URLSearchParams(search).get("sso");
  if (marker === "none" || marker === "error") return false;
  if (readFlag("browser", SIGNED_OUT_KEY)) return false;
  if (readFlag("tab", ATTEMPTED_KEY)) return false;
  return true;
}

/** Address the browser is sent to for a silent attempt that returns to `returnTo`. */
export function silentSsoUrl(returnTo: string): string {
  return `/api/v1/auth/oidc/login?prompt=none&returnTo=${encodeURIComponent(safeReturnTo(returnTo))}`;
}

/**
 * Sends the browser to the provider as a top-level navigation. A hidden
 * iframe would break under third-party cookie blocking and depends on the
 * provider allowing frames; a plain redirect needs neither.
 */
export function beginSilentSso(returnTo: string) {
  // Marked before leaving so a re-render during the navigation cannot start
  // a second attempt.
  writeFlag("tab", ATTEMPTED_KEY, true);
  window.location.assign(silentSsoUrl(returnTo));
}
