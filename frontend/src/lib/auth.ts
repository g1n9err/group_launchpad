import type { components } from "./schema";
import { useSyncExternalStore } from "react";

export type AuthUser = components["schemas"]["AuthUser"];
export type UserRole = components["schemas"]["UserRole"];
type AuthSession = components["schemas"]["AuthSession"];

type SessionState = { token: string; user: AuthUser; expiresAt: number };
let session: SessionState | null = null;
let refreshPromise: Promise<boolean> | null = null;
type AuthListener = () => void;
const listeners = new Set<AuthListener>();

function notify(): void {
  listeners.forEach((listener) => listener());
}

export function onAuthChange(listener: AuthListener): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function isUnlocked(): boolean {
  return session !== null;
}

export function currentUser(): AuthUser | null {
  return session?.user ?? null;
}

export function useAuthUser(): AuthUser | null {
  return useSyncExternalStore(onAuthChange, currentUser, () => null);
}

export function currentSession(): Readonly<SessionState> | null {
  return session;
}

export function storeSession(value: AuthSession): void {
  session = {
    token: value.token,
    user: value.user,
    expiresAt: Date.parse(value.expiresAt),
  };
  notify();
}

export function getAuthHeader(): Record<string, string> {
  return session ? { Authorization: `Bearer ${session.token}` } : {};
}

export function lock(): void {
  session = null;
  notify();
}

export const fireLock = lock;

// Keep short-lived tokens in memory only. Refresh is serialized so concurrent
// API requests never try to rotate the same one-time token more than once.
export async function refreshSession(): Promise<boolean> {
  if (!session) return false;
  if (refreshPromise) return refreshPromise;
  const oldToken = session.token;
  refreshPromise = (async () => {
    try {
      const response = await fetch(`${import.meta.env.VITE_API_URL ?? ""}/api/auth/refresh`, {
        method: "POST",
        headers: { Authorization: `Bearer ${oldToken}` },
        cache: "no-store",
      });
      if (!response.ok) {
        if (session?.token === oldToken) lock();
        return false;
      }
      const next = await response.json() as AuthSession;
      if (session?.token !== oldToken) return false;
      storeSession(next);
      return true;
    } catch {
      if (session?.token === oldToken) lock();
      return false;
    } finally {
      refreshPromise = null;
    }
  })();
  return refreshPromise;
}

export function rejectSession(token: string): void {
  if (session?.token === token) lock();
}
