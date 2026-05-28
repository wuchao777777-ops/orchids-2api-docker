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

The useful files are under `apis/multi_agent/v1`:

- `request.proto`
- `response.proto`
- `task.proto`

The generated Go code in that repository is not published as an importable Go
module. Importing it directly would require vendoring/generated code or adding a
local generation step.

## Borrowed Behaviors

- Prefer nested persisted tokens such as `id_token.refresh_token`.
- Match official WARP headers for client version and OS metadata.
- Parse SSE base64 protobuf response events.
- Keep fallback tool field numbers aligned with `task.proto`.

## Recommended Next Step

Replace `realRequestTemplate` and hand-written protobuf patching with generated
protobuf types from `warp-proto-apis`. That is the largest stability win, but it
should be done as a separate change because it introduces generated source or a
new code-generation workflow.
