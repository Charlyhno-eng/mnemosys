![Mnemosys banner](assets/mnemosys-banner.png)

# Mnemosys

---

Mnemosys is a browser-based workspace for organizational knowledge. It stores documentation as portable Markdown files in a user-selected vault, with nested folders, wiki links, backlinks, and a knowledge graph.

Search works locally across page names, metadata, and content. People and AI agents can collaborate through profiles and permissions; AI edits are submitted as proposals for human review. Page metadata lives in Markdown frontmatter, and the app uses no database.

---

## Quickstart

### Install

```bash
npm --prefix frontend install
```

### Run

Run these commands in two separate terminals:

```bash
go run ./apps/api
```

```bash
npm --prefix frontend run dev
```

Open the app, add a human profile on the Profiles page, then choose a storage folder in Settings. Add AI profiles on the same page and assign their rights there.

## Native MCP for local agents

For English walkthroughs, see [Write to a page with Python](docs/mcp-python.md) and [Write to a page with TypeScript](docs/mcp-typescript.md). Each includes a standalone, readable script that finds an AI profile by name, initializes MCP, reads a page revision, and proposes appending text for human review. The Python client uses the standard library; the TypeScript client runs directly on Node.js 24 without npm packages.

Start the API as described above, then configure your agent's MCP client with the Streamable HTTP URL `http://127.0.0.1:8080/mcp` (adjust the port if you change `-addr`). The endpoint implements protocol version `2025-06-18`, initialization, ping, tool discovery, and tool calls. It returns JSON responses and uses no sessions or persistent SSE stream; GET and DELETE return HTTP 405. No separate MCP process or dependency is required.

The endpoint is intended for trusted agents on the same machine. It requires a loopback peer and a localhost/loopback Host, and rejects browser origins that differ from the API origin. It has no user authentication or remote-access configuration; do not expose it through a reverse proxy. Clients must send `Content-Type: application/json`, `Accept: application/json, text/event-stream`, and the negotiated `MCP-Protocol-Version: 2025-06-18` header after initialization. Request bodies are limited to 1 MiB.

MCP tool calls always act as AI. Configure each AI profile on the Profiles page, then send its `id` from `GET /api/settings/application` in the `X-Mnemosys-Profile-ID` header on MCP requests. The backend applies that profile's permissions and uses its name as the owner of created pages. New configurations deny MCP tool calls without a profile ID. The local MCP endpoint does not authenticate callers; profile IDs identify a policy, not a secret credential. Only trusted local agents should be given access to this endpoint.

| Tool | Arguments | Behavior |
| --- | --- | --- |
| `search` | `query`; optional `mode`, `scope` | Search names, metadata and content using `names` (file/folder paths only), `lexical` (default), or `hybrid` (lexical plus semantic ranking) mode. |
| `read` | `path` | Read a Markdown page, including frontmatter and its current revision. |
| `explore` | Optional `path` | Return the recursive vault tree or a folder's contents. |
| `create` | `path`, `type`; optional `content`, `pageType` | Create a `document` or `directory` immediately with AI create permission. Parents must exist; page type defaults to `general`. An agent cannot enable `ai_editable` at creation. |
| `delete` | `path` | Delete a page or folder recursively with AI delete permission. |
| `update` | `path`, `content`, `baseRevision` | Submit replacement Markdown as a proposal requiring human review. |
| `link` | `path`, `target`, `baseRevision`; optional `label` | Propose appending an ordinary wiki link to an existing page or folder. Page targets use stable UUID links. |
| `validate` | `path`; optional `content` | Check a stored page or proposed content against its UUID, page type and resolvable wiki links. Returns `valid` and `issues`; does not approve or merge a proposal. |

Paths are relative to the vault; document filenames must end in `.md`. To update or link, first call `read`, then pass its `revision` as `baseRevision`. Both tools require AI edit permission and the page's human-controlled `ai_editable: true`. A stale revision returns an error with the current document. Successful calls return a proposal for review and merge in the Mnemosys interface. Proposals currently live in server memory and are lost when the API restarts.

## Verification

```bash
GOCACHE=/tmp/mnemosys-go-cache go test ./...
npm --prefix frontend run build
```
