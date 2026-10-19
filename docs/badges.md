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

## Dynamic status badge (live counts)

The MCP server exposes a **public**, no-auth endpoint that renders a live
status badge straight from the board:

```
GET /badge/{owner}/{repo}.svg
```

The badge shows the current task counts as `todo|doing|done` in the colored
half and a label in the dark half. It is safe to embed in a public README: it
exposes only the three per-status counts, never a task title, description, or
any other board content. Unknown or unreadable repos render a gray
`not found` badge (HTTP 200) rather than a broken image.

Responses carry `Content-Type: image/svg+xml` and
`Cache-Control: no-cache, max-age=60`, so README viewers see near-live counts.

### URL format

```
https://mcp.cainban.example.com/badge/OWNER/REPO.svg
```

Replace `mcp.cainban.example.com` with your MCP API origin and `OWNER/REPO`
with the target repository (e.g. `hmain/cainban`).

### Query parameters

| Param | Values | Default | Effect |
| ----- | ------ | ------- | ------ |
| `style` | `flat`, `flat-square`, `plastic` | `flat` | Badge shape (shields.io style). |
| `label` | any text | `cainban` | Left (dark) half text. |
| `color` | a name (`blue`, `green`, `red`, `orange`, `yellow`, `gray`, …) or a hex literal (`#2563eb`, `2563eb`, `abc`) | `blue` | Right (message) half fill. |

Unrecognized `style`/`color` values fall back to the defaults, and the `label`
is XML-escaped, so untrusted query input can never inject markup.

### Examples

Default blue flat badge:

```markdown
[![cainban board status](https://mcp.cainban.example.com/badge/OWNER/REPO.svg)](https://cainban.example.com/connect?repo=OWNER/REPO)
```

Green, square, custom label:

```markdown
[![tasks](https://mcp.cainban.example.com/badge/OWNER/REPO.svg?style=flat-square&label=tasks&color=green)](https://cainban.example.com/connect?repo=OWNER/REPO)
```

HTML:

```html
<a href="https://cainban.example.com/connect?repo=OWNER/REPO">
  <img src="https://mcp.cainban.example.com/badge/OWNER/REPO.svg" alt="cainban board status" height="20" />
</a>
```

### Static fallback template

`cainban-status.svg` still ships as a static template showing `cainban | board`
for deployments that can't reach the dynamic endpoint.

```markdown
[![cainban board status](https://cainban.example.com/badges/cainban-status.svg)](https://cainban.example.com/connect?repo=OWNER/REPO)
```

## Where the files live

- SVGs: `web/public/badges/*.svg` (served at `/badges/*.svg`).
- Gallery page: `web/src/pages/BadgesPage.tsx` (route `/badges`).
- Reusable component: `web/src/components/InstallBadges.tsx`.
- Dynamic badge endpoint: `src/systems/mcp/badge.go` (route `GET /badge/{owner}/{repo}.svg`).
