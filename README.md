# n8n-tui

A fast, keyboard-driven Terminal User Interface for [n8n](https://n8n.io) workflow automation instances.

Manage workflows, inspect executions node by node, fire webhooks with custom payloads and watch executions stream in live, all without opening a browser.

## Features

| View | What it does |
|------|--------------|
| **Workflows** | List/detail workspace with active state, tags, node list and webhook URLs. Search, filter by active state, sort, activate/deactivate, trigger, delete. |
| **Executions** | Execution history with status and duration. Filter by workflow and status, lazy-loads older pages, retry and delete. |
| **Execution inspector** | Node-by-node breakdown: timing, item counts, output JSON, error messages, descriptions and stack traces. Jumps to the failing node. |
| **Webhook** | Send `GET/POST/PUT/PATCH/DELETE/HEAD` requests to production (`/webhook/`) or test (`/webhook-test/`) endpoints with a JSON payload editor and custom headers. |
| **Logs** | Live stream of execution starts, per-node completions, errors and final status. |
| **Credentials** | Credential metadata and n8n variables. Values are masked by default, and credential secrets are never fetched. |
| **Health** | n8n version, process health, DB readiness, API auth, latency, execution mode, running/waiting counts. |
| **Settings** | Configured instances. Switch between them live. |

## Install

```sh
go install github.com/kaypee90/n8n-tui/cmd/n8n-tui@latest
```

Or build a static binary:

```sh
CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=$(git describe --tags --always)" -o n8n-tui ./cmd/n8n-tui
```

Requires Go 1.24+. Runs on Linux, macOS and Windows (WSL or native terminals).

## Configure

Create an API key in n8n (**Settings → n8n API**), then create `~/.config/n8n-tui/config.yaml`
(or `$XDG_CONFIG_HOME/n8n-tui/config.yaml`):

```yaml
instances:
  - name: production
    url: https://n8n.example.com
    api_key: ${N8N_API_KEY}
  - name: staging
    url: https://n8n-staging.example.com
    api_key: ${N8N_STAGING_API_KEY}
    # Optional: basic auth for a reverse proxy in front of n8n
    # username: ${PROXY_USER}
    # password: ${PROXY_PASSWORD}
    # Optional: separate host for webhooks (N8N_WEBHOOK_URL / queue-mode webhook processors)
    # webhook_url: https://hooks.example.com
    # Optional: skip TLS verification for self-signed dev setups
    # insecure_skip_verify: true
default_instance: production
refresh_interval: 5s   # polling interval for auto refresh and the log stream
timeout: 10s           # per-request timeout
```

`n8n-tui --example-config` prints this template.

**Secrets.** `${VAR}` references are expanded from the environment when the config loads, so keys don't have to be written to disk. If you do put a literal secret in the file, n8n-tui warns you when the file is readable by other users (`chmod 600` it). API keys and variable values are masked in the UI.

**No config file?** n8n-tui falls back to `N8N_URL`, `N8N_API_KEY`, and optionally `N8N_USERNAME`/`N8N_PASSWORD`:

```sh
N8N_URL=http://localhost:5678 N8N_API_KEY=... n8n-tui
```

## Usage

```sh
n8n-tui                       # connect to default_instance
n8n-tui --instance staging    # connect to a specific instance
n8n-tui --config ./dev.yaml   # use another config file
n8n-tui --version
```

### Key bindings

| Key | Action |
|-----|--------|
| `q` / `ctrl+c` | Quit |
| `?` | Help |
| `ctrl+p` | Search actions and views; `↑/↓` selects, `enter` runs, `esc` closes |
| `!` | Read the full last message or error in a scrollable pane |
| `r` | Refresh |
| `enter` | Select / view details |
| `space` | Toggle workflow active/inactive |
| `t` | Trigger workflow (via its Webhook node) |
| `d` | Delete workflow / execution (asks for confirmation) |
| `e` | Executions (of the selected workflow) |
| `w` | Webhook trigger (pre-filled from the selected workflow) |
| `l` | Live log stream |
| `c` | Credentials & variables |
| `h` | Instance health |
| `s` | Settings / switch instance |
| `1`–`7` | Jump to a view |
| `[` / `]` | Previous / next view |
| `↑/↓` / `j/k` | Move selection or scroll the focused pane |
| `g/G` / `home/end` | First / last row |
| `pgup/pgdown` / `ctrl+u/ctrl+d` | Page / half-page navigation |
| `esc` | Close a dialog, leave the detail pane, clear workflow filters, then go back |
| `/` | Filter workflows by name, tag or ID; `enter` keeps the filter, `esc` clears it |
| `f` / `o` | Workflows: cycle all/active/inactive; sort by updated time/name |
| `f` / `a` | Executions: cycle status filter / show all workflows |
| `R` | Retry a failed execution |
| `tab` | Switch pane or field |
| `ctrl+s` / `ctrl+n` | Webhook: send / cycle through known webhook triggers |
| `p` / `x` / `G` | Logs: pause / clear / follow |
| `v` | Credentials: reveal or mask variable values |

### Keyboard-first workspace

Inspired by [Lazygit](https://github.com/jesseduffield/lazygit), n8n-tui uses bordered panes,
visible focus, numbered views, contextual shortcuts and a searchable action menu.

- **Browse and inspect together.** Workflows appear beside a scrollable preview with tags,
  timestamps, webhook URLs and nodes. `tab` switches focus; `enter` opens execution history.
- **Adapt to your terminal.** Wide terminals show panes side by side. Narrow, tall terminals
  stack them; shorter terminals show the focused pane. `tab` always reaches the other pane.
  Tables hide secondary columns first, and `▸` marks selection even without color.
- **Find actions without memorizing keys.** Press `ctrl+p`, type a few words, then press
  `enter`. Destructive actions still use their confirmation dialog. `?` opens scrollable
  keyboard help; the footer always reserves room for help and quit outside text entry.
- **Keep your place.** Refresh preserves workflow selection and the inspector's selected
  node run and scroll position. Errors appear before node timing metadata. Retrying an
  execution loads the new execution in the inspector; `esc` returns to history.
  Execution polling pauses when more than 250 historical rows are loaded, so older pages
  don't disappear while browsing. Press `r` to return to the newest executions.
- **Read failures.** Error notifications remain until replaced or dismissed. `!` opens the
  full last message, including text too long for the status bar. A failed refresh keeps
  previously loaded workflows visible.

Text fields capture letter shortcuts while editing. Use `esc` to leave the editor,
`ctrl+p` to navigate through the action menu, or `ctrl+c` to quit at any time.
Changing webhook presets preserves your selected production/test mode.
The minimum terminal size is 40 columns × 12 rows; 100 × 30 or larger is recommended.
Below the minimum size, only `q` and `ctrl+c` are accepted until you resize the terminal.

## How it maps to the n8n API

n8n-tui uses the public REST API (`/api/v1`) with the `X-N8N-API-KEY` header, plus a few unauthenticated endpoints:

| Feature | Endpoint |
|---------|----------|
| Workflows | `GET /workflows`, `POST /workflows/{id}/activate`, `POST /workflows/{id}/deactivate`, `DELETE /workflows/{id}` |
| Executions | `GET /executions`, `GET /executions/{id}?includeData=true`, `DELETE /executions/{id}`, `POST /executions/{id}/retry` |
| Credentials / variables | `GET /credentials`, `GET /variables` |
| Webhooks | `{url}/webhook/{path}`, `{url}/webhook-test/{path}` |
| Health | `GET /healthz`, `GET /healthz/readiness`, `GET /rest/settings` (version and mode, if exposed) |

Some limits come from n8n itself:

- **Triggering:** the public API has no "run workflow" endpoint, so `t` calls the workflow's Webhook trigger node. A workflow without one can't be started remotely. Production webhooks only work for *active* workflows. Test webhooks need "Listen for test event" running in the editor.
- **Live logs:** n8n has no push API for executions, so the stream polls at `refresh_interval`. It keeps running in the background while you use other views.
- **Version-dependent endpoints:** execution retry (n8n ≥ 1.83), credential listing and variables (license-dependent) can be missing. When they are, n8n-tui says so and keeps working.

## Development

```sh
go run ./cmd/n8n-tui        # run
go test -race ./...         # test
go vet ./...
```

Layout:

```
cmd/n8n-tui/          entrypoint and flags
internal/api/         n8n REST client (resty)
internal/config/      config loading (viper), env expansion, validation
internal/models/      API types: workflows, executions, node run data
internal/tui/         Bubble Tea app, one file per view
internal/tui/components/  table, confirm dialog, formatting helpers
```
