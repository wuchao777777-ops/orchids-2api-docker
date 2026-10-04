// Package responses implements the OpenAI Responses protocol side of the
// gateway: the wire types, the request/response conversions, the SSE codec, the
// sub-resources, compaction, and the bridge that serves a Responses client from
// a Chat Completions channel.
//
// It is deliberately the sibling of internal/adapter, not part of it. The
// adapter translates Anthropic events into OpenAI Chat Completions chunks; this
// package translates the Responses envelope into a Chat Completions request and
// back. Two directions, two packages.
package responses
