import createClient from "openapi-fetch";
import type { components, paths } from "./schema";

// Empty baseUrl in dev — the Vite dev server proxies /api to the Go backend
// (same-origin, zero CORS). In production VITE_API_URL points at the public
// API origin and is baked in at build time (Vercel). This is the ONLY place
// that knows where the API lives.
export const api = createClient<paths>({ baseUrl: import.meta.env.VITE_API_URL ?? "" });

// apiUrl builds a full URL for browser NAVIGATIONS (not fetch) — e.g. the
// Google OAuth connect link, which must hit the API origin directly.
export function apiUrl(path: string): string {
  return `${import.meta.env.VITE_API_URL ?? ""}${path}`;
}

export type Group = components["schemas"]["Group"];
export type GroupCreate = components["schemas"]["GroupCreate"];
export type ApiError = components["schemas"]["Error"]["error"];
export type GroupStatus = components["schemas"]["GroupStatus"];
