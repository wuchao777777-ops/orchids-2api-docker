package store

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

type boltModelChoice struct {
	ID   string
	Name string
}

var (
	boltAbsoluteAssetPattern   = regexp.MustCompile(`/assets/[A-Za-z0-9._-]+\.js`)
	boltRelativeAssetPattern   = regexp.MustCompile(`(?:\./)+[A-Za-z0-9._/-]+\.js`)
	boltModelLabelPattern      = regexp.MustCompile(`"(claude-[^"]+)":\{[^}]*?label:"([^"]+)"`)
	boltClaudeCodeListPattern  = regexp.MustCompile(`new Map\(\[\[[A-Za-z$_][\w$]*\.ClaudeCode,\[(.*?)\]\]\]\)`)
	boltModelRefPattern        = regexp.MustCompile(`\["([^"]+)"\]`)
	fetchBoltModelChoices      = fetchBoltModelChoicesFromBundle
	boltModelDiscoveryFallback = []boltModelChoice{
		{ID: "claude-haiku-4-5-20251001", Name: "Haiku 4.5"},
		{ID: "claude-sonnet-4-5-20250929", Name: "Sonnet 4.5"},
		{ID: "claude-sonnet-4-6", Name: "Sonnet 4.6"},
		{ID: "claude-opus-4-5-20251101", Name: "Opus 4.5"},
		{ID: "claude-opus-4-6", Name: "Opus 4.6"},
	}
	boltModelDiscoveryCache = struct {
		mu      sync.Mutex
		items   []boltModelChoice
		expires time.Time
	}{}
)

const (
	boltAppURL            = "https://bolt.new/"
	boltUserAgent         = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.0.0 Safari/537.36"
	boltModelCacheTTL     = 30 * time.Minute
	boltModelFetchTimeout = 12 * time.Second
)

func fetchBoltModelChoicesFromBundle(ctx context.Context) ([]boltModelChoice, error) {
	boltModelDiscoveryCache.mu.Lock()
	if time.Now().Before(boltModelDiscoveryCache.expires) && len(boltModelDiscoveryCache.items) > 0 {
		items := slices.Clone(boltModelDiscoveryCache.items)
		boltModelDiscoveryCache.mu.Unlock()
		return items, nil
	}
	boltModelDiscoveryCache.mu.Unlock()

	fetchCtx, cancel := context.WithTimeout(ctx, boltModelFetchTimeout)
	defer cancel()

	html, err := fetchBoltText(fetchCtx, boltAppURL)
	if err != nil {
		cacheBoltModelChoices(boltModelDiscoveryFallback)
		return slices.Clone(boltModelDiscoveryFallback), err
	}

	queue := extractBoltAssetURLs(html, boltAppURL)
	if len(queue) == 0 {
		cacheBoltModelChoices(boltModelDiscoveryFallback)
		return slices.Clone(boltModelDiscoveryFallback), fmt.Errorf("no bolt asset urls found")
	}

	seen := make(map[string]struct{}, len(queue))
	for len(queue) > 0 {
		assetURL := queue[0]
		queue = queue[1:]
		if _, ok := seen[assetURL]; ok {
			continue
		}
		seen[assetURL] = struct{}{}

		js, err := fetchBoltText(fetchCtx, assetURL)
		if err != nil {
			continue
		}
		if models, ok := parseBoltBundleModelChoices(js); ok {
			cacheBoltModelChoices(models)
			return slices.Clone(models), nil
		}

		for _, nested := range extractBoltAssetURLs(js, assetURL) {
			if _, ok := seen[nested]; ok {
				continue
			}
			queue = append(queue, nested)
		}
	}

	cacheBoltModelChoices(boltModelDiscoveryFallback)
	return slices.Clone(boltModelDiscoveryFallback), fmt.Errorf("no bolt model list found in bundle assets")
}

func cacheBoltModelChoices(items []boltModelChoice) {
	boltModelDiscoveryCache.mu.Lock()
	boltModelDiscoveryCache.items = slices.Clone(items)
	boltModelDiscoveryCache.expires = time.Now().Add(boltModelCacheTTL)
	boltModelDiscoveryCache.mu.Unlock()
}

func fetchBoltText(ctx context.Context, targetURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", boltUserAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("bolt fetch failed: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func extractBoltAssetURLs(text string, baseURL string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 16)

	for _, match := range boltAbsoluteAssetPattern.FindAllString(text, -1) {
		resolved := resolveBoltAssetURL(baseURL, match)
		if resolved == "" {
			continue
		}
		if _, ok := seen[resolved]; ok {
			continue
		}
		seen[resolved] = struct{}{}
		out = append(out, resolved)
	}

	for _, match := range boltRelativeAssetPattern.FindAllString(text, -1) {
		resolved := resolveBoltAssetURL(baseURL, match)
		if resolved == "" {
			continue
		}
		if _, ok := seen[resolved]; ok {
			continue
		}
		seen[resolved] = struct{}{}
		out = append(out, resolved)
	}
	return out
}

func resolveBoltAssetURL(baseURL, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	target, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	return base.ResolveReference(target).String()
}

func parseBoltBundleModelChoices(js string) ([]boltModelChoice, bool) {
	labelMatches := boltModelLabelPattern.FindAllStringSubmatch(js, -1)
	if len(labelMatches) == 0 {
		return nil, false
	}

	labels := make(map[string]string, len(labelMatches))
	for _, match := range labelMatches {
		if len(match) < 3 {
			continue
		}
		modelID := strings.TrimSpace(match[1])
		label := strings.TrimSpace(match[2])
		if modelID == "" || label == "" {
			continue
		}
		labels[modelID] = label
	}

	listMatch := boltClaudeCodeListPattern.FindStringSubmatch(js)
	if len(listMatch) < 2 {
		return nil, false
	}

	refMatches := boltModelRefPattern.FindAllStringSubmatch(listMatch[1], -1)
	if len(refMatches) == 0 {
		return nil, false
	}

	out := make([]boltModelChoice, 0, len(refMatches))
	seen := map[string]struct{}{}
	for _, match := range refMatches {
		if len(match) < 2 {
			continue
		}
		modelID := strings.TrimSpace(match[1])
		if modelID == "" {
			continue
		}
		if _, ok := seen[modelID]; ok {
			continue
		}
		label := labels[modelID]
		if label == "" {
			continue
		}
		seen[modelID] = struct{}{}
		out = append(out, boltModelChoice{ID: modelID, Name: label})
	}

	return out, len(out) > 0
}

func BuildBoltSeedModels(ctx context.Context) []Model {
	choices, err := fetchBoltModelChoices(ctx)
	if err != nil {
		slog.Warn("Bolt 模型同步: 官网 bundle 解析失败，回退到内置列表", "error", err)
	}
	return buildBoltModelsFromChoices(choices)
}

func buildBoltBootstrapModels() []Model {
	return buildBoltModelsFromChoices(boltModelDiscoveryFallback)
}

func buildBoltModelsFromChoices(choices []boltModelChoice) []Model {
	defaultModelID := chooseBoltDefaultModelID(choices)
	models := make([]Model, 0, len(choices))
	for i, choice := range choices {
		models = append(models, Model{
			ID:        strconv.Itoa(120 + i),
			Channel:   "Bolt",
			ModelID:   choice.ID,
			Name:      "Claude " + choice.Name + " (Bolt)",
			Status:    ModelStatusAvailable,
			IsDefault: choice.ID == defaultModelID,
			SortOrder: i,
		})
	}
	return models
}

func chooseBoltDefaultModelID(choices []boltModelChoice) string {
	preferred := []string{
		"claude-sonnet-4-5-20250929",
		"claude-sonnet-4-6",
		"claude-haiku-4-5-20251001",
	}
	for _, candidate := range preferred {
		for _, choice := range choices {
			if choice.ID == candidate {
				return candidate
			}
		}
	}
	if len(choices) > 0 {
		return choices[0].ID
	}
	return ""
}
