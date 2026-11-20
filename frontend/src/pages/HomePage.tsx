import { Card } from "../components/ui";
import { useSettings } from "../i18n";
import { IconAnnouncements, IconAutomation, IconGroups, IconIntegrations, IconIntakeForms, IconOverview, IconStudents } from "../components/icons";

const guides = [
  { title: "home.workspace", description: "home.workspaceDesc", links: [
    ["nav.overview", "/overview", IconOverview], ["nav.groups", "/groups", IconGroups], ["nav.students", "/students", IconStudents],
  ] },
  { title: "home.communication", description: "home.communicationDesc", links: [
    ["nav.intakeForms", "/intake-forms", IconIntakeForms], ["nav.announcements", "/announcements", IconAnnouncements],
  ] },
  { title: "home.setup", description: "home.setupDesc", links: [
    ["nav.integrations", "/integrations", IconIntegrations], ["nav.automation", "/automation", IconAutomation],
  ] },
] as const;

export function HomePage() {
  const { t } = useSettings();

  return (
    <div className="home-page space-y-7">
      <section className="home-hero">
        <div className="home-hero-content">
          <p className="home-eyebrow">{t("home.eyebrow")}</p>
          <h1>{t("home.title")}</h1>
          <p className="home-description">{t("home.description")}</p>
          <div className="mt-6 flex flex-wrap gap-3">
            <a href="#/overview" className="app-action">{t("home.openWorkspace")}</a>
            <a href="#/groups/new" className="home-secondary-action">{t("home.createGroup")}</a>
          </div>
        </div>
        <aside className="home-platform-panel" aria-label={t("home.platformsLabel")}>
          <div className="home-platform-panel-heading">
            <div>
              <span>{t("home.platformsLabel")}</span>
              <h2>{t("home.platformPanelTitle")}</h2>
            </div>
            <strong>04</strong>
          </div>
          <p className="home-platform-panel-description">{t("home.platformPanelDescription")}</p>
          <ul>
            <li><i className="platform-mark slack-mark">S</i><span>Slack</span></li>
            <li><i className="platform-mark classroom-mark">C</i><span>Google Classroom</span></li>
            <li><i className="platform-mark drive-mark">D</i><span>Google Drive</span></li>
            <li><i className="platform-mark forms-mark">F</i><span>Google Forms</span></li>
          </ul>
        </aside>
      </section>

      <section>
        <div className="mb-4">
          <h2 className="text-xl font-bold tracking-tight text-text">{t("home.stepsTitle")}</h2>
          <p className="mt-1 text-sm text-muted">{t("home.stepsDescription")}</p>
        </div>
        <div className="home-steps">
          {([1, 2, 3] as const).map((step) => (
            <Card key={step} className="home-step-card p-5">
              <span className="home-step-number">0{step}</span>
              <h3>{t(`home.step${step}Title`)}</h3>
              <p>{t(`home.step${step}Description`)}</p>
              <a href={step === 1 ? "#/integrations" : step === 2 ? "#/groups/new" : "#/overview"}>
                {t(step === 1 ? "nav.integrations" : step === 2 ? "nav.groups" : "nav.overview")}
                <span aria-hidden="true"> ↗</span>
              </a>
            </Card>
          ))}
        </div>
      </section>

      <section>
        <div className="mb-4">
          <h2 className="text-xl font-bold tracking-tight text-text">{t("home.navigationTitle")}</h2>
          <p className="mt-1 text-sm text-muted">{t("home.navigationDescription")}</p>
        </div>
        <div className="home-guides">
          {guides.map((guide) => (
            <Card key={guide.title} className="home-guide-card p-5">
              <h3>{t(guide.title)}</h3>
              <p>{t(guide.description)}</p>
              <ul>
                {guide.links.map(([label, href, Icon]) => (
                  <li key={label}>
                    <a href={`#${href}`}><Icon className="h-4 w-4" />{t(label)}</a>
                  </li>
                ))}
              </ul>
            </Card>
          ))}
        </div>
      </section>
    </div>
  );
}
