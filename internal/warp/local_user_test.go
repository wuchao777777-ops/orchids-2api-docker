package warp

import (
	"os"
	"strings"
	"testing"
)

func TestReadLocalUserCredentialFromBytes_ExtractsPlaintextNestedToken(t *testing.T) {
	credential, err := ReadLocalUserCredentialFromBytes([]byte(`{"id_token":{"id_token":"runtime-jwt","refresh_token":"token-from-json"},"refresh_token":""}`))
	if err != nil {
		t.Fatalf("ReadLocalUserCredentialFromBytes() error: %v", err)
	}
	if credential.RefreshToken != "token-from-json" {
		t.Fatalf("RefreshToken=%q want token-from-json", credential.RefreshToken)
	}
}

func TestReadLocalUserCredentialFromBytes_ExtractsLikelyRawToken(t *testing.T) {
	credential, err := ReadLocalUserCredentialFromBytes([]byte("raw_refresh_token_value_abcdefghijklmnopqrstuvwxyz1234567890"))
	if err != nil {
		t.Fatalf("ReadLocalUserCredentialFromBytes() error: %v", err)
	}
	if credential.RefreshToken != "raw_refresh_token_value_abcdefghijklmnopqrstuvwxyz1234567890" {
		t.Fatalf("RefreshToken=%q", credential.RefreshToken)
	}
}

func TestReadLocalUserCredentialFromBytes_ExplainsEncryptedUploadLimit(t *testing.T) {
	orig := decryptLocalUserStorageFunc
	decryptLocalUserStorageFunc = func([]byte) (string, error) {
		return "", os.ErrPermission
	}
	t.Cleanup(func() {
		decryptLocalUserStorageFunc = orig
	})

	_, err := ReadLocalUserCredentialFromBytes([]byte("encrypted"))
	if err == nil {
		t.Fatal("ReadLocalUserCredentialFromBytes() error is nil")
	}
	if !strings.Contains(err.Error(), "upload decrypted User JSON") {
		t.Fatalf("error=%q want decrypted User JSON hint", err.Error())
	}
}
