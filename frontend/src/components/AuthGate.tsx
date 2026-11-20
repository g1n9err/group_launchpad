// AuthGate wraps protected page content: when locked, renders a prompt
// instead of mounting the children. Children (and their hooks — including
// useQuery) are NOT mounted when locked, so no protected fetches fire.

import { useEffect, useState, type ReactNode } from "react";
import { useSettings } from "../i18n";
import { isUnlocked, onAuthChange } from "../lib/auth";
import { Card } from "./ui";

export function AuthGate({ children }: { children: ReactNode }) {
  const { t } = useSettings();
  const [unlocked, setUnlocked] = useState(isUnlocked());

  useEffect(() => {
    const unsubscribe = onAuthChange(() => setUnlocked(isUnlocked()));
    return unsubscribe;
  }, []);

  if (!unlocked) {
    return (
      <Card className="border-dashed p-8 text-center">
        <p className="text-sm text-muted">{t("auth.required")}</p>
      </Card>
    );
  }
  return <>{children}</>;
}
