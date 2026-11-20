// Minimal hash router (no dependency): "#/groups", "#/groups/<uuid>", …
// Gives back/forward support and shareable URLs while the UI stays a SPA.

import { useCallback, useSyncExternalStore } from "react";

export interface Route {
  page: string; // "overview" | "groups" | "students" | …
  id?: string; // e.g. group id for "#/groups/<id>"
}

function parse(hash: string): Route {
  const clean = hash.replace(/^#\/?/, "");
  const [page = "", id] = clean.split("/");
  if (!page) return { page: "home" };
  return { page, id };
}

// getSnapshot MUST return a stable value for Object.is() comparison.
// Strings compare by value; objects would be new references every call,
// causing an infinite re-render loop. We snapshot the raw hash string and
// derive the Route object after the hook returns.
function currentHash(): string {
  return window.location.hash;
}

function subscribe(cb: () => void) {
  window.addEventListener("hashchange", cb);
  return () => window.removeEventListener("hashchange", cb);
}

export function useRoute(): Route {
  const hash = useSyncExternalStore(subscribe, currentHash);
  return parse(hash);
}

export function navigate(to: string) {
  window.location.hash = to.startsWith("#") ? to : `#/${to.replace(/^\//, "")}`;
}

/** Convenience hook: route + setter-style navigate. */
export function useRouter(): [Route, (to: string) => void] {
  const route = useRoute();
  const go = useCallback((to: string) => navigate(to), []);
  return [route, go];
}
