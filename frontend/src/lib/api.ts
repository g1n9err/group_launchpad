import createClient from "openapi-fetch";
import type { components, paths } from "./schema";
import { currentSession, getAuthHeader, refreshSession, rejectSession } from "./auth";

// Empty baseUrl in dev — the Vite dev server proxies /api to the Go backend
// (same-origin, zero CORS). In production VITE_API_URL points at the public
// API origin and is baked in at build time (Vercel). This is the ONLY place
// that knows where the API lives.
//
// Bearer tokens are injected from the in-memory session store — never
// persisted in browser storage or placed in URLs.

// authFetch wraps global fetch to inject the Authorization header from the
// in-memory store on every request — picking up lock/unlock transitions
// without recreating the openapi-fetch client.
async function authFetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const rawURL = input instanceof Request ? input.url : input.toString();
  if (new URL(rawURL, window.location.href).pathname === "/api/auth/login") {
    return fetch(input, init);
  }
  let session = currentSession();
  if (session && session.expiresAt - Date.now() < 120_000) {
    await refreshSession();
    session = currentSession();
  }
  const auth = getAuthHeader();
  const headers = new Headers(init?.headers);
  // The session store is authoritative; this also replaces any stale header
  // captured by a long-lived component before a token rotation.
  if (auth.Authorization) headers.set("Authorization", auth.Authorization);
  else headers.delete("Authorization");
  const response = await fetch(input, { ...init, headers });
  if (response.status === 401 && session) rejectSession(session.token);
  return response;
}

export const api = createClient<paths>({
  baseUrl: import.meta.env.VITE_API_URL ?? "",
  fetch: authFetch as typeof fetch,
});

// apiUrl builds a full URL for browser NAVIGATIONS (not fetch) — e.g. the
// Google OAuth connect link, which must hit the API origin directly.
export function apiUrl(path: string): string {
  return `${import.meta.env.VITE_API_URL ?? ""}${path}`;
}

export type Group = components["schemas"]["Group"];
export type GroupCreate = components["schemas"]["GroupCreate"];
export type ApiError = components["schemas"]["Error"]["error"];
export type GroupStatus = components["schemas"]["GroupStatus"];
