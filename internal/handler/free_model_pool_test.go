package handler

import (
	"context"
	"testing"

	"orchids-api/internal/store"
	"orchids-api/internal/testutil"
)

func TestSelectAccountRecord_ExhaustedWorkBuddyRequiresConfirmedAdvertisedFreeModel(t *testing.T) {
	h, s, _ := setupModelValidationHandler(t)
	ctx := context.Background()
	acc := &store.Account{
		AccountType: "workbuddy", WorkBuddyRefreshToken: "token", Enabled: true, Weight: 1,
		StatusCode:        store.AccountStatusWorkBuddyQuotaExhausted,
		WorkBuddyModelIDs: []string{`{"id":"hy3"}`, `{"id":"gpt-5.6-sol"}`},
	}
	testutil.NoError(t, s.CreateAccount(ctx, acc))
	if got, err := h.selectAccountRecordWithOptions(ctx, "workbuddy", nil, accountSelectionOptions{ModelID: "hy3"}); err != nil || got.ID != acc.ID {
		t.Fatalf("free selection got=%v err=%v", got, err)
	}
	for _, model := range []string{"gpt-5.6-sol", "hy4-preview-f"} {
		if _, err := h.selectAccountRecordWithOptions(ctx, "workbuddy", nil, accountSelectionOptions{ModelID: model}); err == nil {
			t.Fatalf("model %q unexpectedly selected exhausted WorkBuddy account", model)
		}
	}
}
func TestSelectAccountRecord_ExhaustedQoderRequiresExplicitAccountFreeFactor(t *testing.T) {
	h, s, _ := setupModelValidationHandler(t)
	ctx := context.Background()
	catalog := []string{`{"key":"qfmodel","name":"Qwen3.8-Flash","price_factor":0}`}
	acc := &store.Account{AccountType: "qoder", QoderRefreshToken: "token", Enabled: true, Weight: 1, StatusCode: store.AccountStatusQoderQuotaExhausted, QoderModelIDs: catalog}
	testutil.NoError(t, s.CreateAccount(ctx, acc))
	testutil.NoError(t, s.CreateModel(ctx, &store.Model{Channel: "Qoder", ModelID: "qwen3.8-flash", Name: "qwen3.8-flash", Status: store.ModelStatusAvailable, BillingTier: "free", BillingSource: "qoder_price_factor"}))
	if got, err := h.selectAccountRecordWithOptions(ctx, "qoder", nil, accountSelectionOptions{ModelID: "qwen3.8-flash"}); err != nil || got.ID != acc.ID {
		t.Fatalf("free selection got=%v err=%v", got, err)
	}
}
