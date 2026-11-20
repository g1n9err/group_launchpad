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
import { AccountPage } from "./pages/AccountPage";
import { HomePage } from "./pages/HomePage";
import { useRoute } from "./lib/router";

export default function App() {
  const route = useRoute();

  if (route.page === "login") return <LoginPage />;

  return (
    <AppShell>
      {route.page === "home" && <HomePage />}
      {route.page === "overview" && <OverviewPage />}
      {(route.page === "groups" || route.page === "group-details") && <GroupsPage />}
      {route.page === "students" && <StudentsPage />}
      {route.page === "intake-forms" && <IntakeFormsPage />}
      {route.page === "integrations" && <IntegrationsPage />}
      {route.page === "automation" && <AutomationPage />}
      {route.page === "announcements" && <AnnouncementsPage />}
      {route.page === "settings" && <SettingsPage />}
      {route.page === "account" && <AccountPage />}
    </AppShell>
  );
}
