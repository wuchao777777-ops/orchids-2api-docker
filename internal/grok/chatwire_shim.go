package grok

import (
	"orchids-api/internal/chatwire"
	"orchids-api/internal/store"
)

// The OpenAI Chat Completions wire types now live in internal/chatwire: the
// Responses bridge lowers a request into this shape and posts it to the shared
// chat pipeline, so the types cannot belong to the Grok provider package.
//
// These aliases keep every existing grok call site compiling unchanged. The
// three request-scoped fields that were unexported are exported on
// chatwire.Request as StartedAt, SourceOperation and Account.
type (
	ChatCompletionsRequest = chatwire.Request
	ChatMessage            = chatwire.Message
	ToolDef                = chatwire.ToolDef
	ToolCall               = chatwire.ToolCall
	RateLimitInfo          = chatwire.RateLimitInfo
)

// chatSourceOperationKey stays in grok on purpose: a context key's identity is
// the key's type, so moving the type to another package would silently change
// which value a context lookup finds.
type chatSourceOperationKey struct{}

// Compatibility aliases for the loose parsers grok still calls by their short
// local names.
var (
	parseLooseStringAny       = chatwire.ParseLooseStringAny
	parseLooseBoolAny         = chatwire.ParseLooseBoolAny
	parseLooseBoolAnyForField = chatwire.ParseLooseBoolAnyForField
	parseLooseIntAny          = chatwire.ParseLooseIntAny
	parseLooseFloatAny        = chatwire.ParseLooseFloatAny
	parseStringList           = chatwire.ParseStringList
	validateToolDefinitions   = chatwire.ValidateToolDefinitions
)

// nativeToolTypes is the hosted-tool table. Define it once in chatwire and
// alias it here so the two cannot drift.
var nativeToolTypes = chatwire.NativeToolTypes

// ensure the store import stays meaningful for the alias above.
var _ = store.Account{}
