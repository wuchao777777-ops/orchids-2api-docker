package channel

import (
	"orchids-api/internal/testutil"
	"testing"
)

func TestRegistryInvariantsAndPathParsing(t *testing.T) {
	seenID, seenLabel, seenPrefix := map[ID]bool{}, map[string]bool{}, map[string]bool{}
	defaults := 0
	for _, definition := range All() {
		testutil.Falsef(t, seenID[definition.ID] || seenLabel[definition.Label] || seenPrefix[definition.APIPrefix], "duplicate definition: %+v", definition)
		seenID[definition.ID], seenLabel[definition.Label], seenPrefix[definition.APIPrefix] = true, true, true
		if definition.Default {
			defaults++
		}
		id, ok := Parse(definition.Label)
		testutil.Falsef(t, !ok || id != definition.ID, "label parse failed: %+v", definition)
		id, ok = FromPath(definition.APIPrefix + "/models")
		testutil.Falsef(t, !ok || id != definition.ID, "path parse failed: %+v", definition)
		id, model, ok := TrimModelPath(definition.APIPrefix + "/models/example")
		testutil.Falsef(t, !ok || id != definition.ID || model != "example", "model path parse failed: %+v", definition)
	}
	testutil.Equal(t, defaults, 1)
}

// TestRegistryIsWorkBuddyDefaultAfterChannelRemoval pins the provider set once
// the fifth channel was retired: WorkBuddy carries the default flag and the
// remaining four channels keep their order, so any accidental re-addition or
// reordering of the registry fails here.
func TestRegistryIsWorkBuddyDefaultAfterChannelRemoval(t *testing.T) {
	got := Default()
	testutil.Falsef(t, got.ID != WorkBuddy, "Default() = %q, want %q", got.ID, WorkBuddy)
	want := []ID{WorkBuddy, Qoder, Cline, Grok}
	all := All()
	testutil.Equal(t, len(all), len(want))
	for i, id := range want {
		testutil.Equal(t, all[i].ID, id)
	}
	testutil.Equal(t, len(GenericPrefixes()), 3)
	testutil.Equal(t, len(AllPrefixes()), len(want))
}
