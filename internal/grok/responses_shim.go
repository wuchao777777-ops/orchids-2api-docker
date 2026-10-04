package grok

import (
	"io"

	"orchids-api/internal/responses"
)

// Compatibility shims onto internal/responses.
//
// The Responses protocol machinery — the SSE codec, the terminal-status
// semantics, the schema-driven argument normalization and the tool alias
// rewrite — is shared with the non-Grok channels, so it now lives in
// internal/responses. These aliases keep every existing grok call site on its
// short local name; the migration to the package-qualified names happens as
// each file is touched for its own reasons, not as one mechanical rename.

// Type aliases: grok code declares variables of these types, so the identity
// must be shared rather than merely structurally identical.
type (
	ResponsesCreateRequest      = responses.CreateRequest
	compatibleSSEEvent          = responses.SSEEvent
	buildToolAliasIdentity      = responses.ToolAliasIdentity
	buildToolNormalizationState = responses.ToolNormalizationState
)

// Constant aliases.
const (
	upstreamMaxEventBytes         = responses.MaxEventBytes
	maxNormalizedNumberBytes      = responses.MaxNormalizedNumberBytes
	buildCompatibilityWarningsKey = responses.CompatibilityWarningsKey
)

// Function aliases.
var (
	newBuildToolNormalizationState = responses.NewToolNormalizationState
	buildToolAlias                 = responses.BuildToolAlias
	normalizeBuildTool             = responses.NormalizeTool
	normalizeBuildToolChoice       = responses.NormalizeToolChoice
	normalizeBuildFunctionRoot     = responses.NormalizeFunctionRoot

	takeBuildCompatibilityWarnings = responses.TakeCompatibilityWarnings
	collectBuildToolAliases        = responses.CollectToolAliases
	interfaceMaps                  = responses.InterfaceMaps
	normalizeBridgedTools          = normalizeBridgedToolsLocal

	restoreBridgeToolIdentity         = responses.RestoreBridgeToolIdentity
	aliasParameterSchema              = responses.AliasParameterSchema
	normalizeFunctionArguments        = responses.NormalizeFunctionArguments
	normalizeAliasedFunctionArguments = responses.NormalizeAliasedFunctionArguments
	responseFailure                   = responses.Failure
	responseTerminalFinish            = responses.TerminalFinish
	consoleExtractRefusal             = responses.ExtractRefusal
	responseStatusFromFinish          = responses.StatusFromFinish
	responseAnnotations               = responses.Annotations
	readResponseSSEBytes              = responses.ReadSSEBytes
	consumeCompatibleSSE              = responses.ConsumeSSE
	mapsEqualJSON                     = responses.MapsEqualJSON
)

// isPrivateBuildControlEvent stays in grok: it names a Build-private event,
// not a Responses protocol concept.
func isPrivateBuildControlEvent(kind string) bool { return responses.IsPrivateBuildControlEvent(kind) }

// readResponseSSE retains the string callback used by text-oriented callers.
func readResponseSSE(reader io.Reader, consume func(string, string) error) error {
	return responses.ReadSSE(reader, consume)
}

// normalizeBridgedToolsLocal adapts the package-level type alias, which the
// compiler treats as a distinct named type in this position.
func normalizeBridgedToolsLocal(req *ResponsesCreateRequest) (map[string]buildToolAliasIdentity, error) {
	return responses.NormalizeBridgedTools((*responses.CreateRequest)(req))
}

// Aliases for the stored-response helpers that moved to internal/responses.
var (
	responsesOwnerHash      = responses.OwnerHash
	responsesInputItemsJSON = responses.InputItemsJSON
)
