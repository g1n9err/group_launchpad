import { Card } from "../components/ui";
import { useSettings } from "../i18n";
import { IconAnnouncements, IconAutomation, IconCheck, IconGroups, IconIntegrations, IconIntakeForms, IconOverview, IconStudents } from "../components/icons";
import { PlatformLogo } from "../components/PlatformLogo";

const guides = [
  { title: "home.workspace", description: "home.workspaceDesc", icon: IconOverview, links: [
    ["nav.overview", "/overview", IconOverview], ["nav.groups", "/groups", IconGroups], ["nav.students", "/students", IconStudents],
  ] },
  { title: "home.communication", description: "home.communicationDesc", icon: IconAnnouncements, links: [
    ["nav.intakeForms", "/intake-forms", IconIntakeForms], ["nav.announcements", "/announcements", IconAnnouncements],
  ] },
  { title: "home.setup", description: "home.setupDesc", icon: IconIntegrations, links: [
    ["nav.integrations", "/integrations", IconIntegrations], ["nav.automation", "/automation", IconAutomation],
  ] },
] as const;

const steps = [
  { icon: IconIntegrations, key: 1, href: "#/integrations", link: "nav.integrations" },
  { icon: IconGroups, key: 2, href: "#/groups/new", link: "nav.groups" },
  { icon: IconOverview, key: 3, href: "#/overview", link: "nav.overview" },
] as const;

export function HomePage() {
  const { t } = useSettings();

  return (
    <div className="home-page space-y-7">
      <section className="home-hero home-hero--launchpad">
        <div className="home-hero-content">
          <p className="home-eyebrow"><span className="home-eyebrow-mark" aria-hidden="true"><IconGroups className="h-4 w-4" /></span>{t("home.eyebrow")}</p>
          <h1>{t("home.title")}</h1>
          <p className="home-description">{t("home.description")}</p>
          <div className="mt-6 flex flex-wrap gap-3">
            <a href="#/overview" className="app-action">{t("home.openWorkspace")}</a>
            <a href="#/groups/new" className="home-secondary-action">{t("home.createGroup")}</a>
          </div>
        </div>
        <aside className="home-platform-panel home-workflow-preview" aria-label={t("home.platformsLabel")}>
          <div className="home-preview-topline">
            <span className="home-preview-kicker">{t("home.previewLabel")}</span>
            <span className="home-preview-mark" aria-hidden="true"><IconCheck className="h-3.5 w-3.5" /></span>
          </div>
          <div className="home-preview-title">
            <span className="home-preview-icon"><IconGroups className="h-5 w-5" /></span>
            <div><h2>{t("home.platformPanelTitle")}</h2><p>{t("home.platformPanelDescription")}</p></div>
          </div>
          <div className="home-preview-flow">
            <div className="home-preview-step"><span className="home-preview-step-icon"><IconStudents className="h-4 w-4" /></span><div><strong>{t("home.previewStudents")}</strong><small>{t("home.previewStudentsDesc")}</small></div><span className="home-preview-check"><IconCheck className="h-3.5 w-3.5" /></span></div>
            <span className="home-preview-connector" aria-hidden="true" />
            <div className="home-preview-step"><span className="home-preview-step-icon"><IconIntegrations className="h-4 w-4" /></span><div><strong>{t("home.previewServices")}</strong><small>{t("home.previewServicesDesc")}</small></div><span className="home-preview-check"><IconCheck className="h-3.5 w-3.5" /></span></div>
            <span className="home-preview-connector" aria-hidden="true" />
            <div className="home-preview-step"><span className="home-preview-step-icon"><IconOverview className="h-4 w-4" /></span><div><strong>{t("home.previewProgress")}</strong><small>{t("home.previewProgressDesc")}</small></div><span className="home-preview-check"><IconCheck className="h-3.5 w-3.5" /></span></div>
          </div>
          <div className="home-preview-platforms" aria-label={t("home.platformsLabel")}>
            <PlatformLogo platform="slack" /><PlatformLogo platform="classroom" /><PlatformLogo platform="drive" /><PlatformLogo platform="forms" />
            <span>{t("home.platformCountLabel")}</span>
          </div>
        </aside>
      </section>

      <section>
        <div className="mb-4">
          <p className="home-section-eyebrow">{t("home.getStartedEyebrow")}</p>
          <h2 className="mt-1 text-xl font-bold tracking-tight text-text">{t("home.stepsTitle")}</h2>
          <p className="mt-1 text-sm text-muted">{t("home.stepsDescription")}</p>
        </div>
        <div className="home-steps">
          {steps.map(({ icon: Icon, key: step, href, link }) => (
            <Card key={step} className={`home-step-card home-step-card-${step} p-5`}>
              <div className="home-step-topline">
                <span className="home-step-icon"><Icon className="h-5 w-5" /></span>
                <div className={`home-step-stickers home-step-stickers-${step}`} aria-hidden="true">
                  {step === 1 && <><PlatformLogo platform="slack" /><PlatformLogo platform="classroom" /></>}
                  {step === 2 && <><span className="home-step-sticker-avatar">A</span><span className="home-step-sticker-avatar home-step-sticker-avatar-2">S</span><span className="home-step-sticker-form"><IconIntakeForms className="h-4 w-4" /></span></>}
                  {step === 3 && <><PlatformLogo platform="drive" /><PlatformLogo platform="forms" /></>}
                </div>
                <span className="home-step-number">{t("home.stepLabel", { number: step })}</span>
              </div>
              <h3>{t(`home.step${step}Title`)}</h3>
              <p>{t(`home.step${step}Description`)}</p>
              <a href={href}>
                <span>{t(link)}</span>
                <span className="home-step-link-icon" aria-hidden="true"><Icon className="h-4 w-4" /></span>
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
              <div className="home-guide-heading">
                <span className="home-guide-icon"><guide.icon className="h-5 w-5" /></span>
                <h3>{t(guide.title)}</h3>
              </div>
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
