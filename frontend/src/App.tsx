import { AppShell } from "./components/layout";
import { OverviewPage } from "./pages/OverviewPage";
import { IntakeFormsPage } from "./pages/IntakeFormsPage";
import { GroupsPage } from "./pages/GroupsPage";
import { StudentsPage } from "./pages/StudentsPage";
import { IntegrationsPage } from "./pages/IntegrationsPage";
import { AutomationPage } from "./pages/AutomationPage";
import { AnnouncementsPage } from "./pages/AnnouncementsPage";
import { SettingsPage } from "./pages/SettingsPage";
import { LoginPage } from "./pages/LoginPage";
import { SignupPage } from "./pages/SignupPage";
import { AccountPage } from "./pages/AccountPage";
import { HomePage } from "./pages/HomePage";
import { useRoute } from "./lib/router";
import { useAuthUser, useIsResuming } from "./lib/auth";
import { AuthGate } from "./components/AuthGate";
import { Card } from "./components/ui";
import { useSettings } from "./i18n";

export default function App() {
  const route = useRoute();
  const user = useAuthUser();
  const resuming = useIsResuming();
  const { t } = useSettings();

  if (route.page === "login") return <LoginPage />;
  if (route.page === "signup") return <SignupPage />;
  if (route.page === "home") return <AppShell><HomePage /></AppShell>;
  if (route.page === "account") return <AppShell><AccountPage /></AppShell>;

  // While the HttpOnly session cookie is being rotated into a session, do
  // not flash the sign-in prompt on a page refresh.
  if (!user) {
    if (resuming) return <AppShell>{null}</AppShell>;
    return <AppShell><AuthGate><></></AuthGate></AppShell>;
  }

  const staffRole = user.role === "admin" || user.role === "coordinator";
  const groupReader = user.role === "lecturer" || user.role === "ta";
  const groupRoute = route.page === "groups" || route.page === "group-details";
  const groupCreation = route.page === "groups" && route.id === "new";
  if (!staffRole && !(groupReader && groupRoute && !groupCreation)) {
    return <AppShell><Card className="p-8 text-center text-sm text-muted">{t("auth.forbidden")}</Card></AppShell>;
  }

  return (
    <AppShell>
      {route.page === "overview" && <OverviewPage />}
      {(route.page === "groups" || route.page === "group-details") && <GroupsPage />}
      {route.page === "students" && <StudentsPage />}
      {route.page === "intake-forms" && <IntakeFormsPage />}
      {route.page === "integrations" && <IntegrationsPage />}
      {route.page === "automation" && <AutomationPage />}
      {route.page === "announcements" && <AnnouncementsPage />}
      {route.page === "settings" && <SettingsPage />}
    </AppShell>
  );
}
