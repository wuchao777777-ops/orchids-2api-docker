# WARP Upstream Notes

These notes track facts verified against WARP's open-source client and the pinned
`warp-proto-apis` revision used by that client.

## Auth Storage

The stable Windows app stores the persisted user at:

`%LOCALAPPDATA%\warp\Warp\data\dev.warp.Warp-User`

The file is encrypted with Windows DPAPI. After decryption, the refresh token is
stored at `id_token.refresh_token`. A top-level `refresh_token` may be legacy or
empty.

## Multi-Agent Transport

The client posts protobuf requests to:

`https://app.warp.dev/ai/multi-agent`

Responses are `text/event-stream`; each SSE `data:` payload is base64-url-safe
protobuf for `warp.multi_agent.v1.ResponseEvent`.

## Protocol Source

WARP currently pins:

`https://github.com/warpdotdev/warp-proto-apis.git@c67de64fc4949f693a679552dc88cebc9f7d0180`

The public `warpdotdev/warp` repository currently points at this same proto
revision. A shallow checkout of that repository did not include the Rust
application sources that consume the multi-agent API, so the reliable comparison
surface is the pinned proto API, not a visible client implementation.

The useful files are under `apis/multi_agent/v1`:

- `request.proto`
- `response.proto`
- `task.proto`

The generated Go code in that repository is not published as an importable Go
module. Importing it directly would require vendoring/generated code or adding a
local generation step. A generated-proto request builder was tested and reverted
after live requests started failing with HTTP 400; production currently keeps
the byte template because that shape is known to be accepted by Warp.

## Borrowed Behaviors

- Prefer nested persisted tokens such as `id_token.refresh_token`.
- Match official WARP headers for client version and OS metadata.
- Parse SSE base64 protobuf response events.
- Keep fallback tool field numbers aligned with `task.proto`.

## Current Request Shape

Decoded with the pinned `request.proto`, `realRequestTemplate` is a valid
`warp.multi_agent.v1.Request` with:

- `settings.model_config.base = "claude-4-5-opus"`
- `settings.model_config.cli_agent = "cli-agent-auto"`
- `settings.supports_parallel_tool_calls = true`
- `settings.supports_reasoning_message = true`
- `settings.web_search_enabled = true`
- `settings.supports_v4a_file_diffs = true`
- `settings.supported_tools` includes shell, file, grep, MCP, subagent,
  document, and prompt-suggestion tool types.
- `settings.supported_cli_agent_tools` includes long-running shell output,
  grep, glob, read files, and search codebase.

The request builder patches only the `model_config.base` value in this template.
It does not currently populate the newer `coding`, `computer_use_agent`,
`base_model_context_window_limit`, `custom_model_providers`,
`supports_bundled_skills`, `supports_research_agent`, or
`supports_orchestration_v2` fields.

## Model Discovery and Availability

Official Warp runs with one logged-in user's model configuration. Orchids-2api
runs a multi-account pool, so a model list assembled as a union across accounts
can expose models that a selected account cannot actually call.

Live verification showed that GraphQL visibility is not the same as callability:
`workspace.availableLlms(includeAllConfigurableLlms: true)` can return models
such as Claude/Gemini variants that later fail at `/ai/multi-agent` with errors
like `the requested base model (...) is not allowed for your account` or `No
model available`.

Current behavior:

- Prefer `user.llms.agentMode.choices` as the trusted callable model source.
- Use `workspace.availableLlms` only as a fallback when agentMode returns no
choices.
- Keep `auto-open` as the default Warp model.
- Map old auto aliases (`auto`, `auto-efficient`, `auto-genius`) to `auto-open`.
- Retry Warp HTTP 400 model-availability failures once with `auto-open`.
- Classify Warp model-availability errors separately from generic client 400s so
  retry/account-switch policy can treat them as account/model availability, not
  malformed client input.

## Known Differences

- Official `Request.Settings.ModelConfig` is role-specific. We send one patched
  base model and retain the template's `cli_agent = "cli-agent-auto"`.
- Official `MCPContext.resources` and `MCPContext.tools` are deprecated in favor
  of grouped `MCPContext.servers`. We still send top-level tools for
  compatibility.
- Official `ResponseEvent.StreamFinished.should_refresh_model_config` tells the
  client when its model config is stale. We now parse and log this signal, but
  do not yet trigger an automatic model refresh.
- Official `StreamFinished` contains request cost and conversation usage
  metadata. We currently keep token usage only.
- Official model refresh is tied to a single user's current model configuration.
  We still need a per-account allowedness cache/probe layer if we want the
  model page to represent every pooled account precisely.
- The public client repository's full multi-agent consumer code is not present,
  so further behavioral parity should be validated by generated proto round-trip
  tests and live debug traces.

## Recommended Next Step

Do not replace `realRequestTemplate` wholesale until we can capture an official
accepted request for the same client version and verify byte-for-byte parity.
The safer next step is a low-concurrency per-account model probe/cache:

- Probe only selected/default candidate models, not the full configurable
  catalog.
- Cache `(account_id, model_id) -> allowed/unavailable` with a short TTL.
- Use the cache during account selection so a request for a specific model picks
  an account known to allow it.
- Consume `should_refresh_model_config` by scheduling a serialized Warp refresh
  and invalidating the relevant allowedness cache.
