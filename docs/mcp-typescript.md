# Write to a page with TypeScript

## 1. Start Mnemosys

From the Mnemosys repository, run these commands in separate terminals:

```bash
go run ./apps/api
npm --prefix frontend run dev
```

Open `http://localhost:5173`. Using a human profile:

- On **Profiles**, add an AI profile named `Writer` with **View** and **Edit** permissions.
- Create `demo.md`, set `ai_editable: true` in its Markdown frontmatter, and wait for autosave.

## 2. Run your independent TypeScript app

Save this as `write_page.ts` anywhere on the same machine. Run it with **Node.js 24**; no npm packages are needed. Change `PATH` and `ADDITION` to choose the page and text to append; `PATH` is relative to the vault.

```typescript
const BASE = "http://127.0.0.1:8080";
const PROFILE_NAME = "Writer";
const PATH = "demo.md";
const ADDITION = "\n\n## TypeScript update\n\nHello from my independent app.\n";

type Profile = { id: string; type: string; name: string };
type Page = { content: string; revision: string };
type ToolResult = { content: { text: string }[]; isError?: boolean };
let requestId = 0;
let profileId: string;

async function rpc<T>(method: string, params = {}, notification = false): Promise<T> {
  const id = ++requestId;
  const response = await fetch(BASE + "/mcp", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Accept: "application/json, text/event-stream",
      "MCP-Protocol-Version": "2025-06-18",
      "X-Mnemosys-Profile-ID": profileId,
    },
    body: JSON.stringify({ jsonrpc: "2.0", id: notification ? undefined : id, method, params }),
    signal: AbortSignal.timeout(15_000),
  });
  if (!response.ok) throw new Error(await response.text());
  if (notification) {
    if (response.status !== 202) throw new Error("Initialization notification failed");
    return undefined as T;
  }
  const reply = await response.json();
  if (reply.error) throw new Error(JSON.stringify(reply.error));
  return reply.result as T;
}

async function tool<T>(name: string, arguments_: Record<string, string>): Promise<T> {
  const result = await rpc<ToolResult>("tools/call", { name, arguments: arguments_ });
  const value = JSON.parse(result.content[0].text);
  if (result.isError) throw new Error(JSON.stringify(value));
  return value as T;
}

async function main() {
  const response = await fetch(BASE + "/api/settings/application", { signal: AbortSignal.timeout(15_000) });
  if (!response.ok) throw new Error(await response.text());
  const settings: { profiles: Profile[] } = await response.json();
  const matches = settings.profiles.filter(p => p.type === "ai" && p.name === PROFILE_NAME);
  if (matches.length !== 1) throw new Error("Create exactly one AI profile named " + PROFILE_NAME);
  profileId = matches[0].id;
  const initialized = await rpc<{ protocolVersion: string }>("initialize", {
    protocolVersion: "2025-06-18", capabilities: {},
    clientInfo: { name: "typescript-app", version: "1.0.0" },
  });
  if (initialized.protocolVersion !== "2025-06-18") throw new Error("Unsupported MCP version");
  await rpc("notifications/initialized", {}, true);
  const page = await tool<Page>("read", { path: PATH });
  const result = await tool<{ proposal: { id: string } }>("update", {
    path: PATH, content: page.content + ADDITION, baseRevision: page.revision,
  });
  console.log("Proposal created:", result.proposal.id);
}

main().catch(error => { console.error(error); process.exitCode = 1; });
```

```bash
node write_page.ts
```

The script prints a proposal ID. In Mnemosys, open `demo.md`, review the proposal and click **Merge proposal** to save the addition. MCP updates require human review; they do not directly overwrite the page. Merge before restarting the server: proposals are kept in memory.

Permission errors mean the profile lacks View/Edit rights or the page lacks `ai_editable: true`. If the page changed concurrently, rerun the script after reviewing the latest content.

[Python equivalent](mcp-python.md)
