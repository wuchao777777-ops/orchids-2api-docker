package qoder

import (
	"orchids-api/internal/testutil"
	"testing"
)

// TestAliyunUserTypeFallsBackToADocumentedClass pins the account class in the
// chat body: the upstream sorts a request into a queue by it, so an unknown
// class must still send a class it recognises rather than an empty field.
func TestAliyunUserTypeFallsBackToADocumentedClass(t *testing.T) {
	if got := aliyunUserTypeOr(""); got != defaultAliyunUserType || got == "" {
		t.Fatalf("aliyunUserTypeOr(\"\") = %q, want a documented default", got)
	}
	testutil.Equal(t, aliyunUserTypeOr("personal_professional_trial"), "personal_professional_trial")
	testutil.Equal(t, aliyunUserTypeOr("  "), defaultAliyunUserType)

	client := NewFromAccount(nil, nil)
	testutil.Equal(t, client.aliyunUserType(), "")
}
