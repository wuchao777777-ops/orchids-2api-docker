package store

import (
	"orchids-api/internal/testutil"
	"reflect"
	"testing"
)

func TestModelNormalizeRouteAndAccountBinding(t *testing.T) {
	model := &Model{Provider: " BUILD ", Origin: "", Capabilities: []string{"video", "VIDEO", " responses "}, BoundAccountIDs: []int64{9, 2, 9}}
	model.NormalizeRoute()
	testutil.Equal(t, model.Provider, "build")
	testutil.Equal(t, model.Origin, "manual")
	testutil.Falsef(t, !reflect.DeepEqual(model.Capabilities, []string{"responses", "video"}) || !reflect.DeepEqual(model.BoundAccountIDs, []int64{2, 9}), "normalized route = %#v", model)
	testutil.False(t, !model.SupportsCapability("VIDEO") || model.AllowsAccount(3) || !model.AllowsAccount(9), "route policy mismatch")
}
