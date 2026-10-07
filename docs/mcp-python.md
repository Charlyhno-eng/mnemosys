# Write to a page with Python

This standalone example connects a Python app to the local Mnemosys MCP endpoint. It reads a page, then submits an updated version as a proposal for human review.

## 1. Start Mnemosys

From the Mnemosys repository, run these commands in separate terminals:

```bash
go run ./apps/api
npm --prefix frontend run dev
```

Open `http://localhost:5173`. Using a human profile:

- On **Profiles**, add an AI profile named `Writer` with **View** and **Edit** permissions.
- Create `demo.md`, set `ai_editable: true` in its Markdown frontmatter, and wait for autosave.

## 2. Save and run the Python app

Save this as `write_page.py` on the same machine as Mnemosys. The example uses only the Python standard library. Change `PAGE_PATH` and `ADDITION` to choose the page and text to append; the page path is relative to the vault.

```python
import json
from urllib.error import HTTPError
from urllib.request import Request, urlopen


BASE_URL = "http://127.0.0.1:8080"
PROFILE_NAME = "Writer"
PAGE_PATH = "demo.md"
TIMEOUT_SECONDS = 15

# This text is appended to the current page content.
ADDITION = (
    "\n\n"
    "## Python update\n\n"
    "Hello from my independent Python app.\n"
)

MCP_PROTOCOL_VERSION = "2025-06-18"
request_id = 0


def get_ai_profile():
    """Find the single AI profile that this app should use."""

    with urlopen(
        BASE_URL + "/api/settings/application",
        timeout=TIMEOUT_SECONDS,
    ) as response:
        settings = json.load(response)

    matches = [
        profile
        for profile in settings["profiles"]
        if profile["type"] == "ai" and profile["name"] == PROFILE_NAME
    ]

    if len(matches) != 1:
        raise RuntimeError(
            f"Create exactly one AI profile named {PROFILE_NAME!r}"
        )

    return matches[0]


def rpc(profile_id, method, params=None, *, notification=False):
    """Send one JSON-RPC request to the local MCP endpoint."""

    global request_id
    request_id += 1

    payload = {
        "jsonrpc": "2.0",
        "method": method,
        "params": params or {},
    }

    # Notifications have no JSON-RPC id and return HTTP 202 without a body.
    if not notification:
        payload["id"] = request_id

    request = Request(
        BASE_URL + "/mcp",
        json.dumps(payload).encode("utf-8"),
        headers={
            "Content-Type": "application/json",
            "Accept": "application/json, text/event-stream",
            "MCP-Protocol-Version": MCP_PROTOCOL_VERSION,
            "X-Mnemosys-Profile-ID": profile_id,
        },
        method="POST",
    )

    try:
        with urlopen(request, timeout=TIMEOUT_SECONDS) as response:
            if notification:
                if response.status != 202:
                    raise RuntimeError("MCP initialization notification failed")
                return None

            reply = json.load(response)
    except HTTPError as error:
        body = error.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"HTTP {error.code}: {body}") from error

    if "error" in reply:
        raise RuntimeError(json.dumps(reply["error"], indent=2))

    return reply["result"]


def call_tool(profile_id, name, arguments):
    """Call an MCP tool and decode its JSON result."""

    result = rpc(
        profile_id,
        "tools/call",
        {
            "name": name,
            "arguments": arguments,
        },
    )

    text_blocks = [
        item["text"]
        for item in result.get("content", [])
        if item.get("type") == "text" and "text" in item
    ]
    if not text_blocks:
        raise RuntimeError(f"MCP tool {name!r} returned no text result")

    value = json.loads(text_blocks[0])
    if result.get("isError"):
        raise RuntimeError(json.dumps(value, indent=2))

    return value


def main():
    print("=== Mnemosys MCP test ===")

    print(f"Looking for the AI profile {PROFILE_NAME!r}...")
    profile = get_ai_profile()
    profile_id = profile["id"]
    print(f"Using {profile['name']} (id={profile_id})")

    print("Initializing the MCP connection...")
    initialized = rpc(
        profile_id,
        "initialize",
        {
            "protocolVersion": MCP_PROTOCOL_VERSION,
            "capabilities": {},
            "clientInfo": {
                "name": "python-app",
                "version": "1.0.0",
            },
        },
    )

    if initialized["protocolVersion"] != MCP_PROTOCOL_VERSION:
        raise RuntimeError(
            "Unsupported MCP version: " + initialized["protocolVersion"]
        )

    rpc(
        profile_id,
        "notifications/initialized",
        notification=True,
    )
    print("MCP connection initialized.")

    print(f"Reading {PAGE_PATH}...")
    page = call_tool(
        profile_id,
        "read",
        {"path": PAGE_PATH},
    )
    print(f"Page read. Current revision: {page['revision']}")

    print("Submitting a page update for human review...")
    result = call_tool(
        profile_id,
        "update",
        {
            "path": PAGE_PATH,
            "content": page["content"] + ADDITION,
            "baseRevision": page["revision"],
        },
    )

    proposal_id = result["proposal"]["id"]
    print()
    print("========================================")
    print("MCP TEST SUCCEEDED")
    print("========================================")
    print(f"Proposal created: {proposal_id}")
    print()
    print(f"Open {PAGE_PATH} in Mnemosys and review the proposal.")


if __name__ == "__main__":
    main()
```

Run the script with:

```bash
python3 write_page.py
```

The script prints the proposal ID. Open `demo.md` in Mnemosys and review the proposal on that page before merging it. MCP updates require human review; they do not directly overwrite the page. Proposals are kept in memory, so merge them before restarting the API.

Permission errors mean the AI profile lacks **View** or **Edit** rights, or the page does not have `ai_editable: true`. If the page changed concurrently, read the latest version and submit your update again.

[TypeScript equivalent](mcp-typescript.md)
