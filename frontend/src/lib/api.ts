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
//
// openapi-fetch calls fetch(new Request(url, init)) — a Request object with
// the serialized body and headers inside. The Authorization override MUST
// start from the incoming Request's headers: passing fresh (empty) headers
// to fetch(request, { headers }) REPLACES them per the fetch spec and would
// strip Content-Type, breaking every form submission with 415.
async function authFetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const rawURL = input instanceof Request ? input.url : input.toString();
  if (new URL(rawURL, window.location.href).pathname === "/api/auth/login") {
    // credentials:"include" is required for the login response's HttpOnly
    // session cookie to be stored when the API is on another origin.
    return fetch(input, { ...init, credentials: "include" });
  }
  let session = currentSession();
  if (session && session.expiresAt - Date.now() < 120_000) {
    await refreshSession();
    session = currentSession();
  }
  const auth = getAuthHeader();
  const base = input instanceof Request ? input.headers : init?.headers;
  const headers = new Headers(base);
  // The session store is authoritative; this also replaces any stale header
  // captured by a long-lived component before a token rotation.
  if (auth.Authorization) headers.set("Authorization", auth.Authorization);
  else headers.delete("Authorization");
  // credentials:"include" lets the HttpOnly /api/auth session cookie flow
  // (login sets it; resume rotates it) even when the API lives on another
  // origin. Data endpoints never receive it (cookie Path=/api/auth).
  const response = await fetch(input, { ...init, headers, credentials: "include" });
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
