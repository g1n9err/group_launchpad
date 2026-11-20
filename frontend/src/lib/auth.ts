// In-memory coordinator credential store. Basic auth credentials are held
// ONLY in this module — never in localStorage, sessionStorage, cookies, or
// URLs. TanStack Query caches holding protected roster data are cleared on
// lock via the callback mechanism below.

let credentials: string | null = null; // "Basic <base64>" header value

type AuthListener = () => void;
const listeners = new Set<AuthListener>();

function notify(): void {
  listeners.forEach((fn) => fn());
}

export function onAuthChange(listener: AuthListener): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function isUnlocked(): boolean {
  return credentials !== null;
}

export function getAuthHeader(): Record<string, string> {
  return credentials ? { Authorization: credentials } : {};
}

export function unlock(username: string, password: string): void {
  credentials = "Basic " + btoa(username + ":" + password);
  notify();
}

export function lock(): void {
  credentials = null;
  notify();
}

// fireLock is an alias for lock() — both clear credentials and notify.
export function fireLock(): void {
  lock();
}
