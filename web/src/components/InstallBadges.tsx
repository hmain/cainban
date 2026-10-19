import { useMemo, useState } from "react";

/**
 * InstallBadges renders copy-paste "install / connect" badges for README files,
 * in the spirit of shields.io. Each badge is an SVG served from
 * `web/public/badges/*.svg`; clicking a repo badge links to the cainban Connect
 * page scoped to that repo (`/connect?repo=OWNER/REPO`).
 *
 * The component is deployment-agnostic: the badge image and link URLs are built
 * from `origin` (defaults to the running app's origin) so the same code renders
 * correct absolute URLs in local dev and in production.
 */

export type BadgeKind = "badge" | "connect" | "status";

type BadgeDef = {
  kind: BadgeKind;
  file: string;
  label: string;
  alt: string;
  description: string;
};

const BADGES: BadgeDef[] = [
  {
    kind: "badge",
    file: "cainban-badge.svg",
    label: "cainban",
    alt: "cainban",
    description: "Minimal badge — shows the project uses cainban.",
  },
  {
    kind: "connect",
    file: "cainban-connect.svg",
    label: "Connect with cainban",
    alt: "Connect with cainban",
    description: "Call-to-action button linking straight to the Connect page.",
  },
  {
    kind: "status",
    file: "cainban-status.svg",
    label: "Board status",
    alt: "cainban board status",
    description: "Dynamic status badge template for the repo's board.",
  },
];

function trimTrailingSlash(s: string): string {
  return s.replace(/\/+$/, "");
}

/** Build the image + link URLs for one badge given a base origin and repo. */
function badgeUrls(origin: string, file: string, repo: string) {
  const base = trimTrailingSlash(origin);
  const img = `${base}/badges/${file}`;
  const link = repo
    ? `${base}/connect?repo=${encodeURIComponent(repo)}`
    : `${base}/connect`;
  return { img, link };
}

function markdownSnippet(alt: string, img: string, link: string): string {
  return `[![${alt}](${img})](${link})`;
}

function htmlSnippet(alt: string, img: string, link: string): string {
  return `<a href="${link}"><img src="${img}" alt="${alt}" height="20" /></a>`;
}

function CopyButton({ text, label }: { text: string; label: string }) {
  const [copied, setCopied] = useState(false);
  const onCopy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // Clipboard can be unavailable (insecure context); fail quietly.
    }
  };
  return (
    <button
      type="button"
      className="copy-btn"
      onClick={() => void onCopy()}
      aria-label={`Copy ${label}`}
    >
      {copied ? "✓ Copied" : `Copy ${label}`}
    </button>
  );
}

export function InstallBadges({
  repo = "",
  origin,
}: {
  /** Optional OWNER/REPO to deep-link the Connect page. */
  repo?: string;
  /** Base origin for URLs; defaults to the app's own origin at runtime. */
  origin?: string;
}) {
  const base = useMemo(
    () =>
      origin ??
      (typeof window !== "undefined" ? window.location.origin : ""),
    [origin],
  );

  return (
    <div className="badge-gallery">
      {BADGES.map((b) => {
        const { img, link } = badgeUrls(base, b.file, repo);
        const md = markdownSnippet(b.alt, img, link);
        const html = htmlSnippet(b.alt, img, link);
        return (
          <section key={b.kind} className="badge-card">
            <div className="badge-card-head">
              <a href={link} target="_blank" rel="noopener noreferrer">
                <img src={img} alt={b.alt} height={20} />
              </a>
              <span className="badge-card-title">{b.label}</span>
            </div>
            <p className="hint small">{b.description}</p>

            <div className="badge-snippet">
              <span className="badge-snippet-label">Markdown</span>
              <code className="badge-snippet-code">{md}</code>
              <CopyButton text={md} label="Markdown" />
            </div>

            <div className="badge-snippet">
              <span className="badge-snippet-label">HTML</span>
              <code className="badge-snippet-code">{html}</code>
              <CopyButton text={html} label="HTML" />
            </div>
          </section>
        );
      })}
    </div>
  );
}

export default InstallBadges;
