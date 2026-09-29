package qoder

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"orchids-api/internal/config"
	"orchids-api/internal/upstream"
)

func TestProtocolProfileDefaultsAndOverrides(t *testing.T) {
	for _, name := range []string{"", "reference", "unknown", "skill-cli", " SKILL-CLI "} {
		cfg := &config.Config{QoderProtocolProfile: name}
		c := NewFromAccount(signedTestAccount(), cfg)
		expected := resolveProtocolProfile(cfg)
		if c.clientID != expected.clientID || c.endpoints.inference != expected.inference || c.businessProduct() != expected.businessProduct {
			t.Fatalf("profile %q not applied as a bundle", name)
		}
		if expected.name == ProfileSkillCLI && c.machineToken != c.machineID {
			t.Fatal("skill machine token differs from ID")
		}
		cfg.QoderClientID = "custom-id"
		cfg.QoderClientVersion = "custom-version"
		cfg.QoderInferenceURL = "http://127.0.0.1:19999"
		c = NewFromAccount(signedTestAccount(), cfg)
		if c.clientID != "custom-id" || c.clientVersion != "custom-version" || c.endpoints.inference != cfg.QoderInferenceURL {
			t.Fatal("explicit overrides lost")
		}
		if c.businessProduct() != expected.businessProduct {
			t.Fatal("overrides changed dialect")
		}
	}
	nilClient := NewFromAccount(signedTestAccount(), nil)
	if nilClient.clientID != DefaultClientID || nilClient.endpoints.inference != DefaultInferenceURL || nilClient.businessProduct() != "qoder_work" {
		t.Fatal("default compatibility changed")
	}
}

func TestProtocolRuntimeUsesSelectedIdentityLayout(t *testing.T) {
	for _, name := range []string{ProfileReference, ProfileSkillCLI} {
		t.Run(name, func(t *testing.T) {
			c := NewFromAccount(signedTestAccount(), &config.Config{QoderProtocolProfile: name})
			// Deterministic, nonzero RSA padding; these are synthetic credentials only.
			entropy := bytes.Repeat([]byte{17}, 2048)
			c.entropy = newRecordingSource(entropy)
			creds := c.currentCredentials()
			got, err := c.ensureRuntimeFields(context.Background(), creds)
			if err != nil {
				t.Fatal(err)
			}
			var want RuntimeFields
			if name == ProfileSkillCLI {
				want, err = runtimeFieldsFor(newRecordingSource(entropy), runtimeFieldInput{UID: creds.UID, OrganizationID: creds.OrgID, OrganizationTags: creds.OrgTags, DataPolicyAgreed: c.dataPolicyAgreed()})
			} else {
				want, err = referenceRuntimeFieldsFor(newRecordingSource(entropy), referenceRuntimeFieldInput{Name: creds.Name, Aid: creds.UID, UID: creds.UID, OrganizationID: creds.OrgID, UserType: aliyunUserTypeOr(c.aliyunUserType()), SecurityOAuthToken: creds.AccessToken, RefreshToken: creds.RefreshToken})
			}
			if err != nil || got != want {
				t.Fatalf("wrong %s runtime layout: err=%v", name, err)
			}
			// Even if an account snapshot carries ciphertext from another dialect, a
			// new client derives its own pair instead of importing those stored bytes.
			snapshot := signedTestAccount()
			c.CopyAccountState(snapshot)
			other := ProfileReference
			if name == ProfileReference {
				other = ProfileSkillCLI
			}
			switched := NewFromAccount(snapshot, &config.Config{QoderProtocolProfile: other})
			if switched.runtimeSnapshot().Complete() {
				t.Fatal("profile switch reused persisted runtime")
			}
		})
	}
}

func TestProtocolProfileChatBodyAndHeadersAgree(t *testing.T) {
	for _, name := range []string{ProfileReference, ProfileSkillCLI} {
		t.Run(name, func(t *testing.T) {
			var c *Client
			hits := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				encoded, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				raw, err := decodeBody(encoded)
				if err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				var body chatBody
				if err = json.Unmarshal(raw, &body); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				product := c.businessProduct()
				if body.Business.Product != product || r.Header.Get("Cosy-Business-Product") != product {
					t.Error("body/header dialect mismatch")
				}
				if name == ProfileSkillCLI && r.Header.Get("Cosy-MachineToken") != r.Header.Get("Cosy-MachineId") {
					t.Error("skill machine token not ID")
				}
				fields := c.runtimeSnapshot()
				payload, err := buildCOSYPayload(body.RequestID, fields.EncryptUserInfo, c.clientVersion)
				if err != nil {
					t.Error(err)
				}
				sig := signRequest(payload, fields.Key, r.Header.Get("Cosy-Date"), string(encoded), signPath(r.URL.String()))
				if r.Header.Get("Authorization") != composeBearer(payload, sig) {
					t.Error("RSA ciphertext signing contract changed")
				}
				_, _ = io.WriteString(w, envelope(`{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`)+"event:finish\n\n")
			}))
			defer srv.Close()
			c = NewFromAccount(signedTestAccount(), &config.Config{QoderProtocolProfile: name, QoderInferenceURL: srv.URL})
			if err := c.SendRequestWithPayload(context.Background(), upstream.UpstreamRequest{Model: "Qwen3.7-Max", Prompt: "hello"}, nil, nil); err != nil {
				t.Fatal(err)
			}
			if hits != 1 {
				t.Fatalf("hits=%d", hits)
			}
		})
	}
}

// TestDefaultUpstreamEndpoints pins the international nodes the channel talks
// to. The chat SSE call and the model catalog are two observations of the same
// inference host, so a host change has to move both together.
func TestDefaultUpstreamEndpoints(t *testing.T) {
	t.Parallel()

	c := NewFromAccount(signedTestAccount(), nil)
	want := endpoints{
		oauth:     "https://qoder.com",
		openAPI:   "https://openapi.qoder.sh",
		inference: "https://api2.qoder.sh",
	}
	if c.endpoints != want {
		t.Fatalf("default endpoints = %+v, want %+v", c.endpoints, want)
	}
	if got, wantURL := chatURL(c.endpoints.inference), "https://api2.qoder.sh"+inferPath+inferQuery; got != wantURL {
		t.Fatalf("chatURL() = %q, want %q", got, wantURL)
	}
	if got, wantURL := c.endpoints.inference+modelListRoutes[0].path, "https://api2.qoder.sh/algo/api/v2/model/list"; got != wantURL {
		t.Fatalf("catalog url = %q, want %q", got, wantURL)
	}
	// The skill-cli dialect uses a different client identity but the same node.
	skill := NewFromAccount(signedTestAccount(), &config.Config{QoderProtocolProfile: ProfileSkillCLI})
	if skill.endpoints.inference != "https://api2.qoder.sh" {
		t.Fatalf("skill-cli inference endpoint = %q, want https://api2.qoder.sh", skill.endpoints.inference)
	}
}
