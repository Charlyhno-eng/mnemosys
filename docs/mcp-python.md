# Write to a page with Python

## 1. Start Mnemosys

From the Mnemosys repository, run these commands in separate terminals:

```bash
go run ./apps/api
npm --prefix frontend run dev
```

Open `http://localhost:5173`. Using a human profile:

- On **Profiles**, add an AI profile named `Writer` with **View** and **Edit** permissions.
- Create `demo.md`, set `ai_editable: true` in its Markdown frontmatter, and wait for autosave.

## 2. Run your independent Python app

Save this as `write_page.py` anywhere on the same machine. It uses only the Python standard library. Change `PATH` and `ADDITION` to choose the page and text to append; `PATH` is relative to the vault.

```python
import json
from urllib.error import HTTPError
from urllib.request import Request, urlopen

BASE = "http://127.0.0.1:8080"
PROFILE_NAME = "Writer"
PATH = "demo.md"
ADDITION = "\n\n## Python update\n\nHello from my independent app.\n"

with urlopen(BASE + "/api/settings/application", timeout=15) as response:
    profiles = json.load(response)["profiles"]
matches = [p for p in profiles if p["type"] == "ai" and p["name"] == PROFILE_NAME]
if len(matches) != 1:
    raise RuntimeError("Create exactly one AI profile named " + PROFILE_NAME)

request_id = 0

def rpc(method, params=None, notification=False):
    global request_id
    request_id += 1
    payload = {"jsonrpc": "2.0", "method": method, "params": params or {}}
    if not notification:
        payload["id"] = request_id
    request = Request(BASE + "/mcp", json.dumps(payload).encode(), headers={
        "Content-Type": "application/json",
        "Accept": "application/json, text/event-stream",
        "MCP-Protocol-Version": "2025-06-18",
        "X-Mnemosys-Profile-ID": matches[0]["id"],
    }, method="POST")
    try:
        with urlopen(request, timeout=15) as response:
            if notification:
                if response.status != 202:
                    raise RuntimeError("Initialization notification failed")
                return
            reply = json.load(response)
    except HTTPError as error:
        raise RuntimeError(error.read().decode()) from error
    if "error" in reply:
        raise RuntimeError(reply["error"])
    return reply["result"]

def tool(name, arguments):
    result = rpc("tools/call", {"name": name, "arguments": arguments})
    value = json.loads(result["content"][0]["text"])
    if result.get("isError"):
        raise RuntimeError(value)
    return value

initialized = rpc("initialize", {
    "protocolVersion": "2025-06-18", "capabilities": {},
    "clientInfo": {"name": "python-app", "version": "1.0.0"},
})
if initialized["protocolVersion"] != "2025-06-18":
    raise RuntimeError("Unsupported MCP version")
rpc("notifications/initialized", notification=True)
page = tool("read", {"path": PATH})
result = tool("update", {
    "path": PATH, "content": page["content"] + ADDITION,
    "baseRevision": page["revision"],
})
print("Proposal created:", result["proposal"]["id"])
```

```bash
python3 write_page.py
```

The script prints a proposal ID. In Mnemosys, open `demo.md`, review the proposal and click **Merge proposal** to save the addition. MCP updates require human review; they do not directly overwrite the page. Merge before restarting the server: proposals are kept in memory.

Permission errors mean the profile lacks View/Edit rights or the page lacks `ai_editable: true`. If the page changed concurrently, rerun the script after reviewing the latest content.

[TypeScript equivalent](mcp-typescript.md)
