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

The MCP server exposes a **signed URL** endpoint that renders a live
status badge straight from the board:

```
GET /badge/{owner}/{repo}.svg?sig={signature}&exp={expiry}
```

The badge shows the current task counts as `todo|doing|done` in the colored
half and a label in the dark half. Responses carry `Content-Type: image/svg+xml`
and `Cache-Control: no-cache, max-age=60`, so README viewers see near-live counts.

### Security: Signed URLs

Dynamic badges require a signed URL to prevent:

- **Repo enumeration**: An attacker cannot probe which repos exist on your cainban instance.
- **Activity inference**: Task counts are private unless you choose to share them via a signed badge.

Without a valid signature, the endpoint returns an "unauthorized" badge (HTTP 200
so embeds degrade gracefully, not a broken image).

To generate a signed badge URL, use the **`create_badge_url`** MCP tool:

```json
{
  "name": "create_badge_url",
  "arguments": {
    "repo": "hmain/cainban",
    "expires_in": "30d"
  }
}
```

This returns a path like `/badge/hmain/cainban.svg?sig=abc123def456&exp=1735689600`
that you prepend your API URL to.

The tool requires authentication, so only authorized users can create valid badge URLs.

### Configuration

Set the `CAINBAN_BADGE_SECRET` environment variable to enable badge signing.
This should be a random string (e.g., 32+ characters). Without it, the badge
endpoint returns a "not configured" badge.

```bash
export CAINBAN_BADGE_SECRET="your-random-secret-here"
```

### URL format

```
https://mcp.cainban.example.com/badge/OWNER/REPO.svg?sig=SIGNATURE&exp=EXPIRY
```

Replace `mcp.cainban.example.com` with your MCP API origin and use the full
signed URL returned by `create_badge_url`.

### Query parameters

| Param | Values | Default | Effect |
| ----- | ------ | ------- | ------ |
| `sig` | (required) | — | HMAC-SHA256 signature. |
| `exp` | Unix timestamp | — | Expiry time; 0 or omitted means no expiry. |
| `style` | `flat`, `flat-square`, `plastic` | `flat` | Badge shape (shields.io style). |
| `label` | any text | `cainban` | Left (dark) half text. |
| `color` | a name (`blue`, `green`, `red`, `orange`, `yellow`, `gray`, …) or a hex literal (`#2563eb`, `2563eb`, `abc`) | `blue` | Right (message) half fill. |

Unrecognized `style`/`color` values fall back to the defaults, and the `label`
is XML-escaped, so untrusted query input can never inject markup.

### Examples

First, generate a signed URL using the `create_badge_url` tool. The tool returns
a path like `/badge/OWNER/REPO.svg?sig=abc123&exp=1735689600`.

Default blue flat badge (with your signed URL):

```markdown
[![cainban board status](https://mcp.cainban.example.com/badge/OWNER/REPO.svg?sig=YOUR_SIGNATURE&exp=EXPIRY)](https://cainban.example.com/connect?repo=OWNER/REPO)
```

Green, square, custom label (pass these options to `create_badge_url`):

```markdown
[![tasks](https://mcp.cainban.example.com/badge/OWNER/REPO.svg?sig=YOUR_SIGNATURE&exp=EXPIRY&style=flat-square&label=tasks&color=green)](https://cainban.example.com/connect?repo=OWNER/REPO)
```

HTML:

```html
<a href="https://cainban.example.com/connect?repo=OWNER/REPO">
  <img src="https://mcp.cainban.example.com/badge/OWNER/REPO.svg?sig=YOUR_SIGNATURE&exp=EXPIRY" alt="cainban board status" height="20" />
</a>
```

### MCP Tool: `create_badge_url`

Generate signed badge URLs via the MCP API:

```json
{
  "name": "create_badge_url",
  "arguments": {
    "repo": "hmain/cainban",
    "expires_in": "30d",
    "style": "flat-square",
    "label": "tasks",
    "color": "green"
  }
}
```

| Argument | Required | Default | Description |
| -------- | -------- | ------- | ----------- |
| `repo` | No | Connection's default | The `owner/repo` to create a badge for. Must be authorized by your token. |
| `expires_in` | No | `30d` | Duration until expiry: `7d`, `30d`, `1y`, or `0` for no expiry. Max: 1 year. |
| `style` | No | `flat` | Badge style: `flat`, `flat-square`, `plastic`. |
| `label` | No | `cainban` | Left-side label text. |
| `color` | No | `blue` | Right-side fill color (name or hex). |

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
