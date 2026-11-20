// AuthGate wraps protected page content: when locked, renders a prompt
// instead of mounting the children. Children (and their hooks — including
// useQuery) are NOT mounted when locked, so no protected fetches fire.

import type { ReactNode } from "react";
import { useSettings } from "../i18n";
import { useAuthUser, type UserRole } from "../lib/auth";
import { Card } from "./ui";

export function AuthGate({ children, roles }: { children: ReactNode; roles?: readonly UserRole[] }) {
  const { t } = useSettings();
  const user = useAuthUser();

  if (!user) {
    return (
      <Card className="border-dashed p-8 text-center">
        <p className="text-sm text-muted">{t("auth.required")}</p>
        <a href="#/login" className="app-action mt-4">{t("auth.signIn")}</a>
      </Card>
    );
  }
  if (roles && !roles.includes(user.role)) {
    return <Card className="p-8 text-center text-sm text-muted">{t("auth.forbidden")}</Card>;
  }
  return <>{children}</>;
}
