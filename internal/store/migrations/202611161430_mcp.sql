-- MCP server (ticket 7.7, ADR 0018): off by default; a token made for MCP
-- works only there, and an ordinary API token never does.
ALTER TABLE households ADD COLUMN mcp_enabled INTEGER NOT NULL DEFAULT 0 CHECK (mcp_enabled IN (0, 1));
ALTER TABLE api_tokens ADD COLUMN mcp INTEGER NOT NULL DEFAULT 0 CHECK (mcp IN (0, 1));
