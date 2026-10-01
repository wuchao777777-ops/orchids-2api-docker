package qoder

import (
	"encoding/json"
	"orchids-api/internal/testutil"
	"strings"
	"testing"
)

// The body Qoder actually returned for production account 22.
const productionBusyBody = `{"code":"10605","message":"{\"isQueued\":true,\"modelKey\":\"qfmodel\",\"queueCount\":0,\"queueType\":\"p3\",\"retryAfterSeconds\":30,\"serviceAvailable\":false,\"waitTime\":30}"}`

// TestEnvelopeCodeReadsADoubleEncodedBody pins the shape that broke production:
// the failure body arrives as a JSON string, so the envelope sits one level
// deeper. Missing it made a 10605 queue refusal look like a credential
// rejection, and every account in turn was parked as a rate limit.
func TestEnvelopeCodeReadsADoubleEncodedBody(t *testing.T) {
	doubleEncoded, err := json.Marshal(productionBusyBody)
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]string{
		"plain object":       productionBusyBody,
		"JSON string body":   string(doubleEncoded),
		"string within body": `{"code":"10605","message":"{\"isQueued\":true}"}`,
	} {
		t.Run(name, func(t *testing.T) {
			testutil.Equal(t, envelopeCode([]byte(raw)), busyCode)
		})
	}
}

// TestStreamReportsAQueueRefusalAsBusy is the end-to-end form of the same bug:
// the SSE frame must reach the busy verdict, not the credential one.
func TestStreamReportsAQueueRefusalAsBusy(t *testing.T) {
	for name, body := range map[string]string{
		"plain body":     productionBusyBody,
		"double-encoded": mustMarshalString(t, productionBusyBody),
	} {
		t.Run(name, func(t *testing.T) {
			frame, err := json.Marshal(map[string]any{
				"headers":         map[string][]string{},
				"body":            body,
				"statusCodeValue": 401,
				"statusCode":      "401",
			})
			if err != nil {
				t.Fatal(err)
			}
			_, streamErr := consumeStreamObserved(strings.NewReader("data: "+string(frame)+"\n\n"), true, nil, nil)
			if streamErr == nil {
				t.Fatal("expected an error")
			}
			testutil.CheckContain(t, streamErr.Error(), "gateway is busy")
			testutil.CheckNotContain(t, streamErr.Error(), "rejected the credential")
		})
	}
}

func mustMarshalString(t *testing.T, value string) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestSharedQueueRefusalIsShapeIndependent pins that the queue payload is
// recognised even when the envelope hides the business code. Production reached
// this state: the code reader could not unwrap the envelope, the refusal was
// reported as a credential rejection, and the retry lost the upstream's wait.
func TestSharedQueueRefusalIsShapeIndependent(t *testing.T) {
	for name, values := range map[string][]string{
		"bare code":             {"{\"code\":\"10605\"}"},
		"queue flag only":       {`{"isQueued":true}`},
		"service unavailable":   {`{"serviceAvailable":false}`},
		"nested inside message": {`{"code":"10605","message":"{\"isQueued\":true,\"serviceAvailable\":false,\"retryAfterSeconds\":30}"}`},
	} {
		t.Run(name, func(t *testing.T) {
			if !sharedQueueRefusal(values...) {
				t.Errorf("%v was not recognised as a shared queue refusal", values)
			}
		})
	}
	for name, values := range map[string][]string{
		"credential rejection": {"qoder upstream rejected the credential: session expired"},
		"agent limit":          {`{"agentLimitResetTime":1790538433100}`},
	} {
		t.Run(name, func(t *testing.T) {
			if sharedQueueRefusal(values...) {
				t.Errorf("%v was wrongly treated as a queue refusal", values)
			}
		})
	}
}
