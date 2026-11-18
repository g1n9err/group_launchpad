import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, apiUrl, type GroupCreate } from "./api";

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

// Browser-navigation URL that starts the Google OAuth flow on the API origin.
export const googleConnectUrl = () => apiUrl("/api/integrations/google/connect");
