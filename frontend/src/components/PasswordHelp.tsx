import { useSettings } from "../i18n";

export function PasswordHelp() {
  const { t } = useSettings();
  return <a href="#/forgot-password" className="mt-4 block text-center text-sm font-semibold text-primary hover:underline">
    {t("login.forgotPassword")}
  </a>;
}
