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

/**
 * Build the image + link URLs for the DYNAMIC status badge. The image is served
 * by the MCP API's public `GET /badge/{owner}/{repo}.svg` endpoint (a different
 * origin from the web app), so it takes the MCP origin; the link still points
 * at the web app's Connect page. A repo is required (the endpoint is per-repo),
 * so callers only render this when a repo is set.
 */
function dynamicBadgeUrls(webOrigin: string, mcpOrigin: string, repo: string) {
  const web = trimTrailingSlash(webOrigin);
  const mcp = trimTrailingSlash(mcpOrigin);
  const img = `${mcp}/badge/${repo}.svg`;
  const link = `${web}/connect?repo=${encodeURIComponent(repo)}`;
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
  mcpOrigin,
}: {
  /** Optional OWNER/REPO to deep-link the Connect page. */
  repo?: string;
  /** Base origin for URLs; defaults to the app's own origin at runtime. */
  origin?: string;
  /**
   * MCP API origin that serves the dynamic badge endpoint
   * (`/badge/{owner}/{repo}.svg`). Defaults to VITE_MCP_API, else the app
   * origin. When no MCP origin can be resolved the Dynamic Status Badge card is
   * omitted.
   */
  mcpOrigin?: string;
}) {
  const base = useMemo(
    () =>
      origin ??
      (typeof window !== "undefined" ? window.location.origin : ""),
    [origin],
  );

  const mcpBase = useMemo(() => {
    if (mcpOrigin) return mcpOrigin;
    const fromEnv =
      typeof import.meta !== "undefined"
        ? (import.meta as unknown as { env?: Record<string, string> }).env
            ?.VITE_MCP_API
        : undefined;
    return fromEnv ?? base;
  }, [mcpOrigin, base]);

  // The dynamic status badge is per-repo and lives on the MCP API, so render it
  // only when both a repo and an MCP origin are known.
  const dyn =
    repo && mcpBase
      ? dynamicBadgeUrls(base, mcpBase, repo)
      : null;

  return (
    <div className="badge-gallery">
      {dyn && (
        <section key="dynamic" className="badge-card">
          <div className="badge-card-head">
            <a href={dyn.link} target="_blank" rel="noopener noreferrer">
              <img src={dyn.img} alt="cainban live board status" height={20} />
            </a>
            <span className="badge-card-title">Dynamic Status Badge</span>
          </div>
          <p className="hint small">
            Live <code>todo|doing|done</code> counts for{" "}
            <code>{repo}</code>, served by the MCP API. Supports{" "}
            <code>?style=</code>, <code>?label=</code> and <code>?color=</code>.
          </p>

          <div className="badge-snippet">
            <span className="badge-snippet-label">Markdown</span>
            <code className="badge-snippet-code">
              {markdownSnippet("cainban live board status", dyn.img, dyn.link)}
            </code>
            <CopyButton
              text={markdownSnippet(
                "cainban live board status",
                dyn.img,
                dyn.link,
              )}
              label="Markdown"
            />
          </div>

          <div className="badge-snippet">
            <span className="badge-snippet-label">HTML</span>
            <code className="badge-snippet-code">
              {htmlSnippet("cainban live board status", dyn.img, dyn.link)}
            </code>
            <CopyButton
              text={htmlSnippet("cainban live board status", dyn.img, dyn.link)}
              label="HTML"
            />
          </div>
        </section>
      )}
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
