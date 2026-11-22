import { useEffect, useSyncExternalStore } from "react";
import { useQuery } from "@tanstack/react-query";
import { useAuthUser, getAuthHeader } from "./auth";
import { apiUrl } from "./api";
import type { Group } from "./api";

const STORAGE_KEY = "launchpad.active-slack-workspace";
const listeners = new Set<() => void>();
let activeWorkspaceId = typeof localStorage === "undefined" ? "" : localStorage.getItem(STORAGE_KEY) ?? "";

function notify() { listeners.forEach((listener) => listener()); }
function subscribe(listener: () => void) {
  listeners.add(listener);
  const onStorage = (event: StorageEvent) => { if (event.key === STORAGE_KEY) { activeWorkspaceId = event.newValue ?? ""; notify(); } };
  window.addEventListener("storage", onStorage);
  return () => { listeners.delete(listener); window.removeEventListener("storage", onStorage); };
}
function snapshot() { return activeWorkspaceId; }

export function setActiveWorkspaceId(id: string) {
  activeWorkspaceId = id;
  if (id) localStorage.setItem(STORAGE_KEY, id); else localStorage.removeItem(STORAGE_KEY);
  notify();
}

export type WorkspaceAssignment = { groupId: string; workspaceId?: string; workspaceName: string };
export type SlackWorkspaceOption = { id: string; teamName: string; teamId: string; status: string };

export function useWorkspaceScope() {
  const user = useAuthUser();
  const isStaff = user?.role === "admin" || user?.role === "coordinator";
  const selectedWorkspaceId = useSyncExternalStore(subscribe, snapshot, () => "");
  const assignments = useQuery({
    queryKey: ["group-slack-workspaces"], enabled: Boolean(isStaff),
    queryFn: async () => {
      const response = await fetch(apiUrl("/api/groups/slack-workspaces"), { headers: getAuthHeader(), cache: "no-store" });
      if (!response.ok) throw new Error(String(response.status));
      return (await response.json()) as { data: WorkspaceAssignment[] };
    },
  });
  const workspaces = useQuery({
    queryKey: ["slack-workspaces"], enabled: Boolean(isStaff), retry: false,
    queryFn: async () => {
      const response = await fetch(apiUrl("/api/integrations/slack/workspaces"), { headers: getAuthHeader(), cache: "no-store" });
      if (!response.ok) throw new Error(String(response.status));
      return (await response.json()) as { data: SlackWorkspaceOption[] };
    },
  });
  useEffect(() => {
    if (!selectedWorkspaceId || selectedWorkspaceId === "__default__" || !workspaces.data) return;
    if (!workspaces.data.data.some((workspace) => workspace.id === selectedWorkspaceId)) setActiveWorkspaceId("");
  }, [selectedWorkspaceId, workspaces.data]);
  const assignmentByGroup = new Map((assignments.data?.data ?? []).map((assignment) => [assignment.groupId, assignment]));
  const filterGroups = <T extends Group>(groups: T[] | undefined): T[] | undefined => {
    if (!groups || !selectedWorkspaceId || !assignments.data) return groups;
    if (selectedWorkspaceId === "__default__") return groups.filter((group) => !assignmentByGroup.get(group.id)?.workspaceId);
    return groups.filter((group) => assignmentByGroup.get(group.id)?.workspaceId === selectedWorkspaceId);
  };
  const workspaceName = workspaces.data?.data.find((workspace) => workspace.id === selectedWorkspaceId)?.teamName;
  return {
    selectedWorkspaceId,
    setSelectedWorkspaceId: setActiveWorkspaceId,
    assignments: assignments.data?.data ?? [],
    assignmentByGroup,
    workspaces: workspaces.data?.data ?? [],
    workspacesPending: workspaces.isPending,
    workspacesFailed: workspaces.isError,
    assignmentsPending: assignments.isPending,
    workspaceName,
    filterGroups,
  };
}
