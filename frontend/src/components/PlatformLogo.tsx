export type Platform = "slack" | "classroom" | "drive" | "forms";

export function PlatformLogo({ platform }: { platform: Platform }) {
  return (
    <i className={`platform-mark ${platform}-mark`} aria-hidden="true">
      <svg viewBox="0 0 32 32" className="h-[18px] w-[18px]" fill="none">
        {platform === "slack" && <>
          <path d="M11 4.5a2 2 0 1 1 3.9-.9l1.2 5.2-3.9.9L11 4.5Z" fill="#36C5F0" />
          <path d="M20.9 27.5a2 2 0 1 1-3.9.9l-1.2-5.2 3.9-.9 1.2 5.2Z" fill="#2EB67D" />
          <path d="M27.5 11a2 2 0 1 1 .9 3.9l-5.2 1.2-.9-3.9 5.2-1.2Z" fill="#ECB22E" />
          <path d="M4.5 20.9a2 2 0 1 1-.9-3.9l5.2-1.2.9 3.9-5.2 1.2Z" fill="#E01E5A" />
          <path d="m17 9.2 3.9-.9 1.2 5.2-3.9.9L17 9.2Zm-6.1 9.3 3.9-.9 1.2 5.2-3.9.9-1.2-5.2Z" fill="#36C5F0" />
          <path d="m18.2 17 5.2-1.2.9 3.9-5.2 1.2-.9-3.9Zm-9.3-6.1 5.2-1.2.9 3.9-5.2 1.2-.9-3.9Z" fill="#2EB67D" />
        </>}
        {platform === "classroom" && <>
          <path d="M5 7.5A2.5 2.5 0 0 1 7.5 5h17A2.5 2.5 0 0 1 27 7.5v15a2.5 2.5 0 0 1-2.5 2.5h-17A2.5 2.5 0 0 1 5 22.5v-15Z" fill="#34A853" />
          <path d="M9 12h14v9H9z" fill="#E8F0FE" />
          <path d="M12 12V9.8a4 4 0 0 1 8 0V12" stroke="#4285F4" strokeWidth="2.5" />
          <circle cx="16" cy="16.2" r="1.5" fill="#FBBC04" />
        </>}
        {platform === "drive" && <>
          <path d="M12 4h7l9 16h-7L12 4Z" fill="#0F9D58" />
          <path d="M12 4h7L9.5 21h-7L12 4Z" fill="#4285F4" />
          <path d="M2.5 21h21l3.5 6H6l-3.5-6Z" fill="#FFBA00" />
        </>}
        {platform === "forms" && <>
          <path d="M8 3h11l6 6v19H8a3 3 0 0 1-3-3V6a3 3 0 0 1 3-3Z" fill="#673AB7" />
          <path d="M19 3v6h6" fill="#B39DDB" />
          <path d="M10 14h10M10 18h10M10 22h7" stroke="#fff" strokeWidth="1.8" strokeLinecap="round" />
        </>}
      </svg>
    </i>
  );
}
