package store

import (
	"reflect"
	"testing"
)

func TestModelNormalizeRouteAndAccountBinding(t *testing.T) {
	model := &Model{Provider: " BUILD ", Origin: "", Capabilities: []string{"video", "VIDEO", " responses "}, BoundAccountIDs: []int64{9, 2, 9}}
	model.NormalizeRoute()
	if model.Provider != "build" || model.Origin != "manual" {
		t.Fatalf("route = %#v", model)
	}
	if !reflect.DeepEqual(model.Capabilities, []string{"responses", "video"}) || !reflect.DeepEqual(model.BoundAccountIDs, []int64{2, 9}) {
		t.Fatalf("normalized route = %#v", model)
	}
	if !model.SupportsCapability("VIDEO") || model.AllowsAccount(3) || !model.AllowsAccount(9) {
		t.Fatalf("route policy mismatch")
	}
}
