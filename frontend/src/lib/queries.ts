import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, apiUrl, type GroupCreate, type Group } from "./api";

// Data hooks: the typed openapi-fetch client enforces the API contract at
// compile time — if openapi.yaml changes, `npm run gen` regenerates schema.d.ts
// and TypeScript flags every drifted call site.

export function useGroups() {
  return useQuery({
    queryKey: ["groups"],
    queryFn: async () => {
      const { data, error } = await api.GET("/api/groups", {
        params: { query: { limit: 50, offset: 0 } },
      });
      if (error) throw error;
      return data;
    },
  });
}

export function useAllGroups(enabled = true) {
  return useQuery({
    queryKey: ["groups", "all"],
    enabled,
    queryFn: async ({ signal }) => {
      const items: Group[] = [];
      let offset = 0;
      for (;;) {
        const { data, error } = await api.GET("/api/groups", {
          params: { query: { limit: 100, offset } },
          signal,
        });
        if (!data) throw error ?? new Error("Could not load groups");
        items.push(...data.data);
        offset += data.data.length;
        if (!data.data.length || offset >= data.page.total) return items;
      }
    },
  });
}

export function useIntakeGroups(enabled: boolean) {
  const query = useAllGroups(enabled);
  return { ...query, data: query.data?.filter((group) => group.status !== "archived") };
}

export function useCreateGroup() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: GroupCreate) => {
      const { data, error } = await api.POST("/api/groups", { body });
      if (error) throw error;
      return data;
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["groups"] }),
  });
}

// Google connection state; retry: false — a 503 (not configured) is a stable
// state, not something worth hammering.
export function useGoogleStatus() {
  return useQuery({
    queryKey: ["google-status"],
    retry: false,
    queryFn: async () => {
      const { data, error } = await api.GET("/api/integrations/google/status");
      if (error) throw error;
      return data;
    },
  });
}

// Slack connection state (server-side bot token — no connect flow).
export function useSlackStatus() {
  return useQuery({
    queryKey: ["slack-status"],
    retry: false,
    queryFn: async () => {
      const { data, error } = await api.GET("/api/integrations/slack/status");
      if (error) throw error;
      return data;
    },
  });
}

// Onboarding report for one group (refreshable).
export function groupOnboardingOptions(groupId: string | undefined) {
  return {
    queryKey: ["onboarding", groupId],
    enabled: groupId !== undefined,
    staleTime: 30_000,
    queryFn: async ({ signal }: { signal: AbortSignal }) => {
      const { data, error } = await api.GET("/api/groups/{groupId}/onboarding", {
        params: { path: { groupId: groupId as string } },
        signal,
      });
      if (!data) throw error ?? new Error("Could not load onboarding");
      return data;
    },
  };
}

export function useGroupOnboarding(groupId: string | undefined) {
  return useQuery(groupOnboardingOptions(groupId));
}

// Browser-navigation URL that starts the Google OAuth flow on the API origin.
export const googleConnectUrl = () => apiUrl("/api/integrations/google/connect");
