# AGENTS.md

## Project

Mnemosys is an enterprise memory and knowledge platform. It centralizes an organization's documentation and knowledge so that they remain structured, connected, reliable, portable, and usable by both people and AI agents. The product draws inspiration from Confluence, Notion, and Obsidian. Its current focus is a fast and pleasant browser-based Markdown documentation experience backed by files owned by the user.

## Implemented features

- A filesystem-backed Markdown vault with a configurable storage location and no database.
- A nested page and folder tree with create, read, edit, rename, move, drag-and-drop, and recursive delete operations.
- A Markdown editor with preview mode, autosave, undo/redo history, formatting tools, image upload, and drag-and-drop media insertion.
- YAML-style frontmatter containing a stable immutable UUID, name, description, and page type for every page. Existing pages receive an ID automatically, and duplicate IDs are repaired at startup.
- Four built-in page types: General, Business documentation, Technical documentation, and Incident documentation.
- Custom page types and graph colors managed from application settings and persisted in `config/config.toml`.
- A knowledge graph showing folder hierarchy and explicit Obsidian-style `[[wiki links]]`, with search, filters, zoom, layout controls, and page-type colors.
- Stable links using `[[id:<uuid>]]` or `[[<uuid>]]`, so references survive page renames and moves.
- Backlinks and navigation through wiki links.
- A dashboard with space browsing and title/path filtering.
- A right-side, scrollable settings drawer for page types and vault location.
- An English-only interface.
- A REST API for pages, folders, graph data, application settings, storage browsing, and media.
- Backend protections for path traversal, symbolic links, invalid page types, malformed JSON, unsupported images, upload size limits, and concurrent mutations.
- Backend unit and HTTP integration tests covering document lifecycle, metadata, stable IDs, graph relations, settings, storage, assets, invalid inputs, and concurrent access.

## Current stack

- Backend: Go, primarily using the standard library.
- Frontend: React, React Router, TypeScript, Vite, and project-local CSS.
- Storage: Markdown files and uploaded media inside `Mnemosys-Vault`.
- Configuration: TOML in `config/config.toml`.
- API: HTTP/REST with JSON payloads.
- Database: none. SQLite is not used by the current application.

## Data layout

The location selected by the user is stored in `config/config.toml`. Mnemosys creates a `Mnemosys-Vault` directory within that location and stores the Markdown tree and its media there. Application settings such as page types are also stored in `config/config.toml`. Page metadata is stored directly in each Markdown file's frontmatter.

## Quality and security

The backend is critical. Every backend feature must have relevant tests before it is considered complete. Tests should cover business logic, errors, edge cases, concurrent access, user input, and unexpected behavior. Use integration tests where appropriate. Treat any security vulnerability, data leak, authorization bypass, injection, data corruption, or critical error as blocking. Never consider a feature complete without checking its error cases and security implications. Backend code must be robust, as deterministic as practical, and maintainable.

Test the frontend when appropriate, while keeping backend reliability and security as the highest priority.

## Rules for agents

1. Understand the existing implementation before changing it.
2. Implement only what the current task requires.
3. Prefer the simplest solution that fully satisfies the requirement.
4. Avoid unnecessary dependencies and premature abstractions.
5. Always test backend changes.
6. Never ignore, swallow, or conceal an error.
7. Preserve existing data and behavior unless the task explicitly changes them.
8. Verify every feature end to end before considering it complete.
9. Fix regressions immediately.
10. Keep the current application stable before expanding its scope.

## Code organization

```text
mnemosys/
├── apps/
│   └── api/
│       └── main.go                 # HTTP server entry point
├── backend/
│   └── documents/
│       ├── config.go               # TOML configuration and page types
│       ├── handler.go              # REST routes and HTTP validation
│       ├── handler_test.go         # HTTP integration tests
│       ├── model.go                # API and domain models
│       ├── repository.go           # Filesystem, frontmatter, graph, and assets
│       ├── service.go              # Synchronized application operations
│       └── service_test.go         # Domain, filesystem, and concurrency tests
├── config/
│   └── config.toml                 # Persisted application configuration
├── frontend/
│   ├── public/                     # Browser logo and favicon
│   └── src/
│       ├── components/             # Dialog, tree, graph, link picker, preview
│       ├── lib/
│       │   └── api.ts              # Typed REST client
│       ├── App.tsx                 # Main application and settings UI
│       ├── index.css               # Application styling
│       └── main.tsx                # React entry point
├── assets/                         # Source branding assets
├── README.md
└── go.mod
```

The backend is organized by feature. The current `documents` module keeps its models, HTTP handlers, business operations, filesystem persistence, configuration, graph construction, and tests together. Do not add database layers, workers, services, or modules that the current feature set does not need.
