# Connect an AI assistant (MCP)

Hejmo can work with an AI assistant such as Claude or ChatGPT through **MCP** (Model Context Protocol). You can then say "Put milk on the shopping list" or "Plan five quick dinners for next week", and the assistant does it on your box. This is optional and **off by default**. Hejmo has no AI of its own: you bring your own assistant.

## What an assistant can and cannot do

- It can work with **events**, **lists**, **tasks** (with routines, templates and stars) and the **meal plan**: only the areas you choose for its token, each either read only or read and change ("change" includes adding, ticking off and deleting).
- A fifth area, **diet and allergies** (health data), is set apart: it is off for every new token, and the most you can give is read only. Free-text allergies always arrive with the note "not checked automatically, not filterable". Only a parent changes them, in Hejmo.
- It can **never** change settings, create or revoke tokens, manage people, displays or phones. Those are not offered as tools.
- A token belongs to the parent who creates it, and the assistant acts as that person. Children get no MCP token.
- **What the assistant reads, it sends to its provider** so the AI can answer. Give it only the areas it needs.

## Switch it on and create a token

1. Open **Settings › AI assistants (MCP)** and switch **MCP** on. Save.
2. Choose **Create the first MCP token**. Give it a name (one token per assistant and device, for example "Claude on Mama's laptop") and choose per area: **No**, **Read only** (the default) or **Read and change**.
3. The box shows the token **once**. Copy it into your assistant right away. If you lose it, revoke the token and create a new one.

The page also shows the **MCP address**, for example `https://hejmo.local/mcp`. It always starts with `https://`: the box speaks MCP only encrypted.

## Enter it in your assistant

Every assistant that supports remote MCP servers asks for the same two things:

- the **address** (some call it "server URL" or "remote MCP server"),
- the **token**, sent as `Authorization: Bearer <token>` (some call it "API key" or "bearer token").

The exact menus differ between assistants and change often; look for "connectors", "integrations" or "MCP servers" in your assistant's settings and check its own help for custom connectors.

## Reachability: usually only at home

Most boxes are reachable only on your home network. A cloud assistant such as the Claude or ChatGPT website cannot reach such a box. An assistant that runs **on a device in your home network** (for example an app on your laptop at home) can.

If your box uses its **own certificate**, the device the assistant runs on has to trust the box first: open **Trust the box** on the MCP page and follow it. With a public name and certificate nothing more is needed.

## Switching off and revoking

- **Switch MCP off** on the same page: no assistant gets in at once, also not one that is connected. The tokens stay stored and work again when you switch MCP on.
- **Revoke a token** on its page ("Revoke access"): that assistant cannot reach Hejmo any more. What it already entered stays.
- A token made for MCP works only for MCP, and no other API token works for MCP.

## For developers

The box serves MCP (streamable HTTP, stateless) at `/mcp`. The tools mirror the REST API functions for events, lists, tasks, the meal plan and (read only) diet and allergies (not creating, changing, deleting or syncing calendars: `POST /calendars`, `PATCH` and `DELETE /calendars/{id}`, `POST /calendars/{id}/sync` are no tools), run as the token's person with the token's scopes; a tool the scopes do not allow is not listed. See [`api/openapi.yaml`](../api/openapi.yaml) for `GET` and `PUT /api/v1/mcp` and `POST /api/v1/mcp/tokens`.
