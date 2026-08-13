package grok

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-json"
)

func TestApplyDPoPAuthorization(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	session := dpopSession{accessToken: "access-token", privateKey: key, publicJWK: publicDPoPJWK(&key.PublicKey)}
	req, _ := http.NewRequest(http.MethodPost, "https://console.x.ai/v1/responses?ignored=1", nil)
	if err := applyDPoPAuthorization(req, session); err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "DPoP access-token" {
		t.Fatalf("Authorization=%q", got)
	}
	parts := strings.Split(req.Header.Get("DPoP"), ".")
	if len(parts) != 3 {
		t.Fatalf("proof has %d parts", len(parts))
	}
	decode := func(part string, out interface{}) {
		raw, err := base64.RawURLEncoding.DecodeString(part)
		if err != nil || json.Unmarshal(raw, out) != nil {
			t.Fatalf("invalid JWT section %q", part)
		}
	}
	var header map[string]interface{}
	var claims map[string]interface{}
	decode(parts[0], &header)
	decode(parts[1], &claims)
	if header["alg"] != "ES256" || header["typ"] != "dpop+jwt" {
		t.Fatalf("unexpected header: %#v", header)
	}
	if claims["htm"] != "POST" || claims["htu"] != "https://console.x.ai/v1/responses" {
		t.Fatalf("unexpected claims: %#v", claims)
	}
	digest := sha256.Sum256([]byte("access-token"))
	if claims["ath"] != base64.RawURLEncoding.EncodeToString(digest[:]) {
		t.Fatalf("unexpected ath: %v", claims["ath"])
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		t.Fatalf("invalid ES256 signature length=%d err=%v", len(sig), err)
	}
	hash := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(&key.PublicKey, hash[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("signature verification failed")
	}
}

func TestParseDPoPAccessToken(t *testing.T) {
	payload, _ := json.Marshal(map[string]interface{}{"exp": time.Now().Add(time.Minute).Unix(), "cnf": map[string]interface{}{"jkt": "thumbprint"}})
	token := "x." + base64.RawURLEncoding.EncodeToString(payload) + ".y"
	expires, thumbprint, err := parseDPoPAccessToken(token)
	if err != nil || expires.IsZero() || thumbprint != "thumbprint" {
		t.Fatalf("expires=%v thumbprint=%q err=%v", expires, thumbprint, err)
	}
}
