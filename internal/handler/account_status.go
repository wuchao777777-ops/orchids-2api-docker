package handler

import (
	"context"
	"log/slog"
	"sync"
	"time"

	apperrors "orchids-api/internal/errors"
	"orchids-api/internal/store"
)

// accountStatusMu 保护并发的 markAccountStatus 调用，
// 避免多个 goroutine 同时修改同一 Account 的 StatusCode/LastAttempt。
var accountStatusMu sync.Mutex

// classifyAccountStatus delegates to the centralized errors package.
func classifyAccountStatus(errStr string) string {
	return apperrors.ClassifyAccountStatus(errStr)
}

func markAccountStatus(ctx context.Context, store *store.Store, acc *store.Account, status string) {
	if acc == nil || store == nil || status == "" {
		return
	}

	accountStatusMu.Lock()
	defer accountStatusMu.Unlock()

	// 避免重复标记同一状态，防止冷却计时器被反复重置
	if acc.StatusCode == status {
		slog.Debug("账号状态已存在，跳过重复标记", "account_id", acc.ID, "status", status)
		return
	}

	now := time.Now()
	acc.StatusCode = status
	acc.LastAttempt = now

	if err := store.UpdateAccount(ctx, acc); err != nil {
		slog.Warn("账号状态更新失败", "account_id", acc.ID, "status", status, "error", err)
		return
	}
	slog.Info("账号状态已标记", "account_id", acc.ID, "status", status)
}
