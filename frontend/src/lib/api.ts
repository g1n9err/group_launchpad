import createClient from "openapi-fetch";
import type { components, paths } from "./schema";
import { getAuthHeader } from "./auth";

// Empty baseUrl in dev — the Vite dev server proxies /api to the Go backend
// (same-origin, zero CORS). In production VITE_API_URL points at the public
// API origin and is baked in at build time (Vercel). This is the ONLY place
// that knows where the API lives.
//
// Coordinator Basic auth headers are injected per-request from the in-memory
// credential store (lib/auth.ts) — never persisted, never in URLs.

// authFetch wraps global fetch to inject the Authorization header from the
// in-memory store on every request — picking up lock/unlock transitions
// without recreating the openapi-fetch client.
function authFetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const auth = getAuthHeader();
  if (!auth.Authorization) return fetch(input, init);
  const headers = new Headers(init?.headers);
  if (!headers.has("Authorization")) headers.set("Authorization", auth.Authorization);
  return fetch(input, { ...init, headers });
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
