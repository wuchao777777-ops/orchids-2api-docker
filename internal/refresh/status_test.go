package refresh

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"orchids-api/internal/config"
	"orchids-api/internal/store"
	"orchids-api/internal/testutil"
)

func TestRefreshQoderQuotaClearsFalseAgentExhaustion(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/quota/usage":
			_, _ = w.Write([]byte(`{"userType":"personal_pro","userQuota":{"total":300,"used":82,"remaining":218,"unit":"credits"}}`))
		case "/api/v2/user/plan":
			_, _ = w.Write([]byte(`{"user_type":"personal_pro","plan_tier_name":"Pro","is_paid_plan":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	mini := miniredis.RunT(t)
	s, err := store.New(store.Options{RedisAddr: mini.Addr(), RedisPrefix: "qoder-auto-quota:"})
	testutil.NoError(t, err, "store.New() error=%v")
	t.Cleanup(func() { _ = s.Close() })

	acc := &store.Account{
		AccountType: "qoder", Enabled: true, StatusCode: "402",
		StatusMessage: "agent limit", LastAttempt: time.Now().Add(-time.Hour),
		QoderAccessToken: "access", QoderRefreshToken: "refresh",
		QoderExpiresAt: time.Now().Add(6 * time.Hour), QoderMachineID: "machine",
	}
	testutil.NoError(t, s.CreateAccount(context.Background(), acc), "CreateAccount() error=%v")
	cfg := &config.Config{QoderOpenAPIBaseURL: upstream.URL, QoderOAuthBaseURL: upstream.URL, QoderInferenceURL: upstream.URL}
	refreshQoderQuota(context.Background(), cfg, s, acc)

	after, err := s.GetAccount(context.Background(), acc.ID)
	testutil.NoError(t, err, "GetAccount() error=%v")
	testutil.Falsef(t, after.StatusCode != "" || after.QoderQuota.Exhausted, "false exhaustion survived authoritative sync: status=%q quota=%+v", after.StatusCode, after.QoderQuota)
	testutil.Equal(t, after.QoderQuota.Remaining, 218)
	testutil.Equal(t, after.UsageCurrent, 218)
}

func TestGrokCLIBillingNeedsSyncUsesSlowCadence(t *testing.T) {
	now := time.Now()
	testutil.False(t, grokCLIBillingNeedsSync(&store.Account{GrokBilling: store.GrokBillingSnapshot{SyncedAt: now.Add(-time.Minute)}}, now), "fresh CLI billing was polled on credential tick")
	testutil.False(t, !grokCLIBillingNeedsSync(&store.Account{GrokBilling: store.GrokBillingSnapshot{SyncedAt: now.Add(-time.Hour)}}, now), "stale CLI billing was not polled")
}

// TestPlanGrokRefreshCycle_SkipsAccountHoldingALease covers the write-back
// guard: an account already refreshing is merged, never scheduled twice.
