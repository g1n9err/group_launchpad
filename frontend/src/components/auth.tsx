// Coordinator session controls and in-memory credential lifecycle.

import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { fireLock, isUnlocked, onAuthChange } from "../lib/auth";
import { navigate } from "../lib/router";
import { useSettings } from "../i18n";

export function useCoordinatorLock() {
  const queryClient = useQueryClient();
  useEffect(() => onAuthChange(() => queryClient.clear()), [queryClient]);
  return {
    isUnlocked: isUnlocked(),
    lock: fireLock,
  };
}

export function CoordinatorSessionControl() {
  const { t } = useSettings();
  const [unlocked, setUnlocked] = useState(isUnlocked());

  useEffect(() => onAuthChange(() => setUnlocked(isUnlocked())), []);

  if (!unlocked) {
    return (
      <a href="#/login" className="app-header-control">
        {t("auth.signIn")}
      </a>
    );
  }

  return (
    <button type="button" onClick={() => {
      fireLock();
      navigate("login");
    }} className="app-header-control app-header-danger">
      {t("auth.lock")}
    </button>
  );
}
