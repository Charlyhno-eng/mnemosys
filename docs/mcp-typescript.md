# Write to a page with TypeScript

This standalone example connects a TypeScript app to the local Mnemosys MCP endpoint. It reads a page, then submits an updated version as a proposal for human review.

## 1. Start Mnemosys

From the Mnemosys repository, run these commands in separate terminals:

```bash
go run ./apps/api
npm --prefix frontend run dev
```

Open `http://localhost:5173`. Using a human profile:

- On **Profiles**, add an AI profile named `Writer` with **View** and **Edit** permissions.
- Create `demo.md`, set `ai_editable: true` in its Markdown frontmatter, and wait for autosave.

## 2. Save and run the TypeScript app

Save this as `write_page.ts` on the same machine as Mnemosys. Run it with **Node.js 24**; no npm packages are needed. Change `PAGE_PATH` and `ADDITION` to choose the page and text to append; the page path is relative to the vault.

```typescript
const BASE_URL = "http://127.0.0.1:8080";
const PROFILE_NAME = "Writer";
const PAGE_PATH = "demo.md";
const TIMEOUT_MILLISECONDS = 15_000;
const MCP_PROTOCOL_VERSION = "2025-06-18";

// This text is appended to the current page content.
const ADDITION = [
  "",
  "",
  "## TypeScript update",
  "",
  "Hello from my independent TypeScript app.",
  "",
].join("\n");

type Profile = {
  id: string;
  type: string;
  name: string;
};

type ApplicationSettings = {
  profiles: Profile[];
};

type JsonRpcError = {
  code: number;
  message: string;
};

type JsonRpcResponse<T> = {
  result?: T;
  error?: JsonRpcError;
};

type InitializedResult = {
  protocolVersion: string;
};

type McpTextBlock = {
  type: string;
  text?: string;
};

type McpToolResult = {
  content: McpTextBlock[];
  isError?: boolean;
};

type Page = {
  content: string;
  revision: string;
};

type UpdateResult = {
  proposal: {
    id: string;
  };
};

let requestId = 0;


async function getAiProfile(): Promise<Profile> {
  const response = await fetch(
    BASE_URL + "/api/settings/application",
    { signal: AbortSignal.timeout(TIMEOUT_MILLISECONDS) },
  );

  if (!response.ok) {
    throw new Error(
      `Unable to load Mnemosys profiles (HTTP ${response.status}): `
        + await response.text(),
    );
  }

  const settings = await response.json() as ApplicationSettings;
  const matches = settings.profiles.filter(
    profile => profile.type === "ai" && profile.name === PROFILE_NAME,
  );

  if (matches.length !== 1) {
    throw new Error(`Create exactly one AI profile named ${PROFILE_NAME}`);
  }

  return matches[0];
}


async function rpc<T>(
  profileId: string,
  method: string,
  params?: Record<string, unknown>,
): Promise<T>;
async function rpc(
  profileId: string,
  method: string,
  params: Record<string, unknown> | undefined,
  notification: true,
): Promise<void>;
async function rpc<T>(
  profileId: string,
  method: string,
  params: Record<string, unknown> = {},
  notification = false,
): Promise<T | void> {
  const payload: Record<string, unknown> = {
    jsonrpc: "2.0",
    method,
    params,
  };

  // MCP notifications have no JSON-RPC id and return HTTP 202 without a body.
  if (!notification) {
    payload.id = ++requestId;
  }

  const response = await fetch(BASE_URL + "/mcp", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Accept: "application/json, text/event-stream",
      "MCP-Protocol-Version": MCP_PROTOCOL_VERSION,
      "X-Mnemosys-Profile-ID": profileId,
    },
    body: JSON.stringify(payload),
    signal: AbortSignal.timeout(TIMEOUT_MILLISECONDS),
  });

  if (!response.ok) {
    throw new Error(`MCP HTTP ${response.status}: ${await response.text()}`);
  }

  if (notification) {
    if (response.status !== 202) {
      throw new Error("MCP initialization notification failed");
    }

    return;
  }

  const reply = await response.json() as JsonRpcResponse<T>;

  if (reply.error) {
    throw new Error(
      `JSON-RPC error ${reply.error.code}: ${reply.error.message}`,
    );
  }

  if (!("result" in reply)) {
    throw new Error("MCP response did not contain a result");
  }

  return reply.result as T;
}


async function callTool<T>(
  profileId: string,
  name: string,
  args: Record<string, string>,
): Promise<T> {
  const result = await rpc<McpToolResult>(
    profileId,
    "tools/call",
    {
      name,
      arguments: args,
    },
  );

  const textBlock = result.content.find(
    item => item.type === "text" && typeof item.text === "string",
  );

  if (!textBlock?.text) {
    throw new Error(`MCP tool ${name} returned no text result`);
  }

  const value = JSON.parse(textBlock.text) as T;
  if (result.isError) {
    throw new Error(JSON.stringify(value, null, 2));
  }

  return value;
}


async function main(): Promise<void> {
  console.log("=== Mnemosys MCP test ===");

  console.log(`Looking for the AI profile ${PROFILE_NAME}...`);
  const profile = await getAiProfile();
  console.log(`Using ${profile.name} (id=${profile.id})`);

  console.log("Initializing the MCP connection...");
  const initialized = await rpc<InitializedResult>(
    profile.id,
    "initialize",
    {
      protocolVersion: MCP_PROTOCOL_VERSION,
      capabilities: {},
      clientInfo: {
        name: "typescript-app",
        version: "1.0.0",
      },
    },
  );

  if (initialized.protocolVersion !== MCP_PROTOCOL_VERSION) {
    throw new Error(
      `Unsupported MCP version: ${initialized.protocolVersion}`,
    );
  }

  await rpc(
    profile.id,
    "notifications/initialized",
    {},
    true,
  );
  console.log("MCP connection initialized.");

  console.log(`Reading ${PAGE_PATH}...`);
  const page = await callTool<Page>(
    profile.id,
    "read",
    { path: PAGE_PATH },
  );
  console.log(`Page read. Current revision: ${page.revision}`);

  console.log("Submitting a page update for human review...");
  const result = await callTool<UpdateResult>(
    profile.id,
    "update",
    {
      path: PAGE_PATH,
      content: page.content + ADDITION,
      baseRevision: page.revision,
    },
  );

  console.log();
  console.log("========================================");
  console.log("MCP TEST SUCCEEDED");
  console.log("========================================");
  console.log(`Proposal created: ${result.proposal.id}`);
  console.log();
  console.log(`Open ${PAGE_PATH} in Mnemosys and review the proposal.`);
}


main().catch(error => {
  console.error("MCP request failed:", error);
  process.exitCode = 1;
});
```

Run the script with:

```bash
node write_page.ts
```

The script prints the proposal ID. Open `demo.md` in Mnemosys and review the proposal on that page before merging it. MCP updates require human review; they do not directly overwrite the page. Proposals are kept in memory, so merge them before restarting the API.

Permission errors mean the AI profile lacks **View** or **Edit** rights, or the page does not have `ai_editable: true`. If the page changed concurrently, read the latest version and submit your update again.

[Python equivalent](mcp-python.md)
