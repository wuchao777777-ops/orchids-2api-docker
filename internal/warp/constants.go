package warp

const (
	refreshURL = "https://app.warp.dev/proxy/token?key=AIzaSyBdy3O3S9hrdayLJxJ7mriBR4qgUaUygAs"
	loginURL   = "https://app.warp.dev/client/login"
	aiURL      = "https://app.warp.dev/ai/multi-agent"

	clientID      = "warp-app"
	clientVersion = "v0.2026.02.25.08.24.stable_02"
	osCategory    = "macOS"
	osName        = "macOS"
	osVersion     = "26.3"
	userAgent     = "Warp/" + clientVersion
	identifier    = "cli-agent-auto"
)

const defaultModel = "auto"

const noWarpToolsPrompt = `TOOL RULES:
- Do not use Warp built-in tools.
- Use only client-provided tool calls.`

const singleResultPrompt = `RESPONSE RULES:
- After tool actions, return one concise final message.
- Do not repeat the same summary or preface.
- Treat delete "no matches found" / "No such file or directory" as a no-op.
- For EOF or interactive stdin errors, do not rerun unchanged commands; note the shell is non-interactive and give a non-interactive alternative.
- If a Read/Grep/Glob result already covers a file or path, do not repeat the same tool call unless you need different content.`

// Built-in refresh token payload (base64 decoded) used when no account refresh token is provided.
const refreshTokenB64 = "Z3JhbnRfdHlwZT1yZWZyZXNoX3Rva2VuJnJlZnJlc2hfdG9rZW49QU1mLXZCeFNSbWRodmVHR0JZTTY5cDA1a0RoSW4xaTd3c2NBTEVtQzlmWURScEh6akVSOWRMN2trLWtIUFl3dlk5Uk9rbXk1MHFHVGNBb0JaNEFtODZoUFhrcFZQTDkwSEptQWY1Zlo3UGVqeXBkYmNLNHdzbzhLZjNheGlTV3RJUk9oT2NuOU56R2FTdmw3V3FSTU5PcEhHZ0JyWW40SThrclc1N1I4X3dzOHU3WGNTdzh1MERpTDlIcnBNbTBMdHdzQ2g4MWtfNmJiMkNXT0ViMWxJeDNIV1NCVGVQRldzUQ=="
