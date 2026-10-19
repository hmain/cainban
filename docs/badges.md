# cainban README badges

Embeddable badges that link to the cainban Connect page, so anyone browsing a
repository can wire the board up to their AI agent in one click.

Generate ready-to-paste snippets at `/badges` in the web app (there's a **Badges**
link in the signed-in header). Enter an `owner/repo` to deep-link the Connect
page to a specific repository, or leave it blank for a generic badge.

## Available badges

| Badge | File | Use |
| ----- | ---- | --- |
| `cainban` | `badges/cainban-badge.svg` | Minimal mark — "this project uses cainban". |
| Connect with cainban | `badges/cainban-connect.svg` | Call-to-action button to the Connect page. |
| Board status | `badges/cainban-status.svg` | Dynamic status badge template (see below). |

All badges are flat, shields.io-style SVGs that render crisply at the standard
20px height and match cainban's branding (dark `#0f1116` label, accent-blue
`#2563eb` message, three-bar kanban mark).

## Usage

Replace `cainban.example.com` with your deployment origin and `OWNER/REPO` with
the target repository.

### Markdown

```markdown
[![Connect with cainban](https://cainban.example.com/badges/cainban-connect.svg)](https://cainban.example.com/connect?repo=OWNER/REPO)
```

### HTML

```html
<a href="https://cainban.example.com/connect?repo=OWNER/REPO">
  <img src="https://cainban.example.com/badges/cainban-connect.svg" alt="Connect with cainban" height="20" />
</a>
```

### Simple badge

```markdown
[![cainban](https://cainban.example.com/badges/cainban-badge.svg)](https://cainban.example.com/connect?repo=OWNER/REPO)
```

## Dynamic status badge

`cainban-status.svg` ships as a static template showing `cainban | board`. To
render a live board count, serve the SVG from an endpoint that substitutes the
message text and recomputes the message-rect width (roughly 6.5px per character
plus padding). Example status strings: `3 in progress`, `12 tasks`, `idle`.

```markdown
[![cainban board status](https://cainban.example.com/badges/cainban-status.svg)](https://cainban.example.com/connect?repo=OWNER/REPO)
```

## Where the files live

- SVGs: `web/public/badges/*.svg` (served at `/badges/*.svg`).
- Gallery page: `web/src/pages/BadgesPage.tsx` (route `/badges`).
- Reusable component: `web/src/components/InstallBadges.tsx`.
