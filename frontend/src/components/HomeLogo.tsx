import { useSettings } from "../i18n";

export function HomeLogo({ className, size, wordmark = false }: {
  className: string;
  size: number;
  wordmark?: boolean;
}) {
  const { t } = useSettings();

  return (
    <a href="#/" aria-label={t("nav.logoHome")} className={className}
      onClick={(event) => {
        if (!event.altKey && !event.ctrlKey && !event.metaKey && !event.shiftKey) window.scrollTo(0, 0);
      }}>
      <img src="/favicon.png" width={size} height={size} alt="" />
      {wordmark && <span className="app-wordmark">Group <strong>Launchpad</strong></span>}
    </a>
  );
}
