package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"orchids-api/internal/store"
)

type boltProjectCreator interface {
	CreateEmptyProject(ctx context.Context) (string, error)
}

func normalizeBoltProjectWorkdir(workdir string) string {
	workdir = strings.TrimSpace(workdir)
	if workdir == "" {
		return ""
	}

	if strings.Contains(workdir, "\\") || (len(workdir) >= 2 && workdir[1] == ':') {
		workdir = strings.ReplaceAll(workdir, "\\", "/")
		workdir = filepath.Clean(workdir)
		workdir = strings.ReplaceAll(workdir, "/", "\\")
		return strings.ToLower(workdir)
	}

	workdir = strings.ReplaceAll(workdir, "\\", "/")
	return filepath.Clean(workdir)
}

func boltProjectSessionKey(accountID int64, workdir string) string {
	workdir = normalizeBoltProjectWorkdir(workdir)
	if accountID == 0 || workdir == "" {
		return ""
	}

	sum := sha256.Sum256([]byte(workdir))
	return "bolt-project:" + strconv.FormatInt(accountID, 10) + ":" + hex.EncodeToString(sum[:16])
}

func (h *Handler) resolveBoltProjectID(ctx context.Context, acc *store.Account, client UpstreamClient, workdir string, forceNew bool) (string, error) {
	if acc == nil {
		return "", fmt.Errorf("bolt account is nil")
	}

	projectID := strings.TrimSpace(acc.ProjectID)
	workdir = normalizeBoltProjectWorkdir(workdir)
	if workdir == "" {
		if !forceNew {
			if projectID == "" {
				return "", fmt.Errorf("missing bolt project id")
			}
			return projectID, nil
		}
	}

	cacheKey := boltProjectSessionKey(acc.ID, workdir)
	if !forceNew && cacheKey != "" && h != nil && h.sessionStore != nil {
		if cached, ok := h.sessionStore.GetBoltProjectID(ctx, cacheKey); ok && strings.TrimSpace(cached) != "" {
			h.sessionStore.Touch(ctx, cacheKey)
			return strings.TrimSpace(cached), nil
		}
	}

	creator, ok := client.(boltProjectCreator)
	if !ok {
		if projectID != "" {
			return projectID, nil
		}
		return "", fmt.Errorf("bolt client does not support project creation")
	}

	projectID, err := creator.CreateEmptyProject(ctx)
	if err != nil {
		return "", err
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return "", fmt.Errorf("bolt project creation returned empty project id")
	}

	if cacheKey != "" && h != nil && h.sessionStore != nil {
		h.sessionStore.SetBoltProjectID(ctx, cacheKey, projectID)
		h.sessionStore.Touch(ctx, cacheKey)
	}
	return projectID, nil
}
