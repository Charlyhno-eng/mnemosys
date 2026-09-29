![Mnemosys banner](assets/mnemosys-banner.png)

# Mnemosys

---

Mnemosys is a shared workspace for durable organizational knowledge. It keeps documentation in portable Markdown files owned by the user, organized through nested folders, ordinary wiki links, and a knowledge graph.

Each page stores stable identity metadata, its page type, owner name, configurable team, concerned application name, application-relative folder path, modification actor, AI-touch state, and timestamp in Markdown frontmatter. Descriptions are not stored. Pages include `ai_editable: false` by default; a human can enable AI editing by editing the Markdown frontmatter. The backend enforces this per-page protection in addition to global AI permissions. Metadata is kept exclusively in the Markdown file, is not duplicated in an application panel, and is excluded from rendered Markdown previews.

The home page includes a Profile and access overview showing the active identity, the current profile's rights, and the AI permission matrix. Its profile and AI-rights controls open the persisted settings form; human profiles have full access and can manage AI view, create, edit, and delete rights, while AI profiles cannot raise their own permissions. Storage is selected locally and pages, folders, and media are kept inside a dedicated `Mnemosys-Vault` directory. There is no database.

AI document changes are submitted as Markdown work items with explicit statuses: draft, in review, needs human input, approved, rejected, or merged. The home page shows a line-based diff, lets the user edit the proposed Markdown, change its collaboration status, merge it, or reject it. A proposal never changes the stored page until it is explicitly merged; resolved proposals remain visible as history.

Pages can reference files or folders with an ordinary wiki link such as `[[architecture]]` or `[[architecture|Architecture]]`. The editor link picker creates these links, backlinks identify references, and the graph displays each link edge.

The home search supports two complementary modes. Lexical mode is the default and ranks exact terms across file and folder names, paths, metadata, and Markdown content. Hybrid mode keeps every lexical signal and augments page results with local semantic ranking derived from term context across the vault. Results identify whether they matched lexically, semantically, or both. Search remains local and requires no external embedding service or database.

Multiple people and agents can edit through the same Mnemosys server without silently overwriting one another. Every document read includes a content revision, and autosave submits that revision with its update. Content updates require `baseRevision` at both the REST and service layers; missing revisions are rejected before any accompanying move or write. If another writer saved first, Mnemosys keeps the local draft, returns the latest saved Markdown to clients with view permission, and asks the editor to review it before either loading the latest version or explicitly replacing it. Edits to different pages do not conflict, while same-page conflicts are resolved deliberately.

Concurrent AI submissions for the same page remain separate proposals with independent IDs and review statuses; new submissions do not replace other proposals or resolved history. Only the Human profile can merge a proposal. If the stored page no longer matches its original content, merging returns an edit conflict with the current document and leaves both the page and proposal unchanged. Review the latest page and submit a revised proposal instead of retrying the stale merge.

This is revision-based collaboration through one server process, not live character-by-character coediting or automatic merging. Filesystem mutations are serialized within that process. Independent server processes and direct external file edits are not coordinated. The browser/REST profile remains shared application state rather than a separate authenticated identity per person; MCP calls always use AI permissions. Proposals and their history remain in memory only and are lost on server restart.

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

## Native MCP for local agents

Start the API as described above, then configure your agent's MCP client with the Streamable HTTP URL `http://127.0.0.1:8080/mcp` (adjust the port if you change `-addr`). The endpoint implements protocol version `2025-06-18`, initialization, ping, tool discovery, and tool calls. It returns JSON responses and uses no sessions or persistent SSE stream; GET and DELETE return HTTP 405. No separate MCP process or dependency is required.

The endpoint is intended for trusted agents on the same machine. It requires a loopback peer and a localhost/loopback Host, and rejects browser origins that differ from the API origin. It has no user authentication or remote-access configuration; do not expose it through a reverse proxy. Clients must send `Content-Type: application/json`, `Accept: application/json, text/event-stream`, and the negotiated `MCP-Protocol-Version: 2025-06-18` header after initialization. Request bodies are limited to 1 MiB.

All tool calls require AI view permission and always act as AI, regardless of the profile selected in the browser. A human can configure AI permissions in Settings.

| Tool | Arguments | Behavior |
| --- | --- | --- |
| `search` | `query`; optional `mode`, `scope` | Search names, metadata and content using `lexical` (default) or `hybrid` mode. |
| `read` | `path` | Read a Markdown page, including frontmatter and its current revision. |
| `explore` | Optional `path` | Return the recursive vault tree or a folder's contents. |
| `create` | `path`, `type`; optional `content`, `pageType` | Create a `document` or `directory` immediately with AI create permission. Parents must exist; page type defaults to `general`. An agent cannot enable `ai_editable` at creation. |
| `update` | `path`, `content`, `baseRevision` | Submit replacement Markdown as a proposal requiring human review. |
| `link` | `path`, `target`, `baseRevision`; optional `label` | Propose appending an ordinary wiki link to an existing page or folder. Page targets use stable UUID links. |
| `validate` | `path`; optional `content` | Check a stored page or proposed content against its UUID, page type and resolvable wiki links. Returns `valid` and `issues`; does not approve or merge a proposal. |

Paths are relative to the vault; document filenames must end in `.md`. To update or link, first call `read`, then pass its `revision` as `baseRevision`. Both tools require AI edit permission and the page's human-controlled `ai_editable: true`. A stale revision returns an error with the current document. Successful calls return a proposal for review and merge in the Mnemosys interface. Proposals currently live in server memory and are lost when the API restarts.

## Verification

```bash
GOCACHE=/tmp/mnemosys-go-cache go test ./...
npm --prefix frontend run build
```
