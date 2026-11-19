import { AppShell } from "./components/layout";
import { OverviewPage } from "./pages/OverviewPage";
import { IntakeFormsPage } from "./pages/IntakeFormsPage";
import { GroupsPage } from "./pages/GroupsPage";
import { StudentsPage } from "./pages/StudentsPage";
import { IntegrationsPage } from "./pages/IntegrationsPage";
import { AutomationPage } from "./pages/AutomationPage";
import { AnnouncementsPage } from "./pages/AnnouncementsPage";
import { SettingsPage } from "./pages/SettingsPage";
import { useRoute } from "./lib/router";

export default function App() {
  const route = useRoute();

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
