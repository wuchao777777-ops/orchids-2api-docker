package grok

import (
	"strings"
	"testing"
)

func TestParseSessionIdentityNormal(t *testing.T) {
	body := []byte(`{"status":"ok","session":{"userId":"u_123","email":"user@example.com","organizationId":"org_456"},"user":{"id":"u_123","teamId":"team_789"}}`)
	identity, err := ParseSessionIdentity(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if identity.UserID != "u_123" {
		t.Fatalf("userID = %q", identity.UserID)
	}
	if identity.Email != "user@example.com" {
		t.Fatalf("email = %q", identity.Email)
	}
	// session.organizationId takes precedence over user.teamId in the fallback order.
	if identity.TeamID != "org_456" {
		t.Fatalf("teamID = %q", identity.TeamID)
	}
}

func TestParseSessionIdentityTopLevelFallback(t *testing.T) {
	body := []byte(`{"userId":"u_abc","email":"a@b.c","teamId":"t_1"}`)
	identity, err := ParseSessionIdentity(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if identity.UserID != "u_abc" || identity.Email != "a@b.c" || identity.TeamID != "t_1" {
		t.Fatalf("identity = %+v", identity)
	}
}

func TestParseSessionIdentityUnauthenticated(t *testing.T) {
	if _, err := ParseSessionIdentity([]byte(`{"status":"unauthenticated"}`)); err == nil {
		t.Fatal("expected error for unauthenticated session")
	}
}

func TestParseSessionIdentityBlocked(t *testing.T) {
	if _, err := ParseSessionIdentity([]byte(`{"status":"blocked"}`)); err == nil {
		t.Fatal("expected error for blocked session")
	}
}

func TestParseSessionIdentityMissingIdentity(t *testing.T) {
	if _, err := ParseSessionIdentity([]byte(`{"status":"ok","session":{}}`)); err == nil {
		t.Fatal("expected error when no identity fields present")
	}
}

func TestParseSessionIdentityInvalidJSON(t *testing.T) {
	if _, err := ParseSessionIdentity([]byte(`{`)); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseSessionIdentityTrimmed(t *testing.T) {
	body := []byte(`{"status":"ok","session":{"userId":" u_1 ","organizationId":" org_2 "}}`)
	identity, err := ParseSessionIdentity(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(identity.UserID) != identity.UserID {
		t.Fatalf("userID should be trimmed, got %q", identity.UserID)
	}
	if identity.TeamID != "org_2" {
		t.Fatalf("teamID fallback to organizationId = %q", identity.TeamID)
	}
}
