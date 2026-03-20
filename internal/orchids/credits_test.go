package orchids

import (
	"net/http"
	"testing"
)

func TestParseRSCCredits_UserProfileResponse(t *testing.T) {
	t.Parallel()

	body := `0:{"a":"$@1","f":"","b":"choPxsUJba0sBZaoLUW1Q","q":"","i":false}
1:{"id":7151762,"userId":"user_3AHq0Zdu5m1LetDBxJwCZ9F43s4","email":"degohjsodin852@einzieg.site","plan":"FREE","credits":0,"lastFreeCreditsRedeem":null,"oneTimeCredits":null,"freeCreditsBlocked":true}
`

	info, err := parseRSCCredits(body)
	if err != nil {
		t.Fatalf("parseRSCCredits() error = %v", err)
	}
	if info.Plan != "FREE" {
		t.Fatalf("Plan=%q want FREE", info.Plan)
	}
	if info.Credits != 0 {
		t.Fatalf("Credits=%v want 0", info.Credits)
	}
}

func TestIsOrchidsActionNotFound(t *testing.T) {
	t.Parallel()

	resp := &http.Response{
		Header: http.Header{
			"X-Nextjs-Action-Not-Found": []string{"1"},
		},
	}
	if !isOrchidsActionNotFound(resp, "") {
		t.Fatal("expected x-nextjs-action-not-found header to be detected")
	}
	if !isOrchidsActionNotFound(nil, "Server action not found.") {
		t.Fatal("expected body text to be detected")
	}
	if isOrchidsActionNotFound(nil, "something else") {
		t.Fatal("unexpected action-not-found detection")
	}
}
