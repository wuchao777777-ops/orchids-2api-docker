package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/goccy/go-json"
)

type puterPublicModelsResponse struct {
	Models []string `json:"models"`
}

type puterPublicModelDetailsResponse struct {
	Models []puterPublicModelDetails `json:"models"`
}

type puterPublicModelDetails struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	PuterID  string `json:"puterId"`
}

type puterPublicModelChoice struct {
	ID   string
	Name string
}

const (
	puterPublicModelsURL       = "https://puter.com/puterai/chat/models"
	puterPublicModelDetailsURL = "https://puter.com/puterai/chat/models/details"
)

var puterChatCompletionProviders = map[string]struct{}{
	"anthropic":  {},
	"deepseek":   {},
	"mistralai":  {},
	"openrouter": {},
	"x-ai":       {},
}

var puterProviderAliases = map[string]string{
	"claude":     "anthropic",
	"deepseek":   "deepseek",
	"gemini":     "google",
	"google":     "google",
	"mistral":    "mistralai",
	"openai":     "openai",
	"openrouter": "openrouter",
	"xai":        "x-ai",
	"x-ai":       "x-ai",
}

func fetchPuterPublicModelChoices(ctx context.Context, proxyFunc func(*http.Request) (*url.URL, error)) ([]puterPublicModelChoice, error) {
	if models, err := fetchPuterPublicModelDetails(ctx, proxyFunc); err == nil && len(models) > 0 {
		return models, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, puterPublicModelsURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/144.0.0.0 Safari/537.36")

	client := &http.Client{
		Timeout: 12 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
		},
	}
	if proxyFunc != nil {
		client.Transport = &http.Transport{Proxy: proxyFunc}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("puter models fetch failed: %d", resp.StatusCode)
	}

	var payload puterPublicModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return normalizePuterPublicModels(payload.Models), nil
}

func fetchPuterPublicModelDetails(ctx context.Context, proxyFunc func(*http.Request) (*url.URL, error)) ([]puterPublicModelChoice, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, puterPublicModelDetailsURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/144.0.0.0 Safari/537.36")

	client := &http.Client{
		Timeout: 12 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
		},
	}
	if proxyFunc != nil {
		client.Transport = &http.Transport{Proxy: proxyFunc}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("puter model details fetch failed: %d", resp.StatusCode)
	}

	var payload puterPublicModelDetailsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return normalizePuterPublicModelDetails(payload.Models), nil
}

func normalizePuterPublicModels(rawModels []string) []puterPublicModelChoice {
	if len(rawModels) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(rawModels))
	out := make([]puterPublicModelChoice, 0, len(rawModels))
	for _, raw := range rawModels {
		id, ok := normalizePuterModelID(raw)
		if !ok || id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, puterPublicModelChoice{ID: id, Name: id})
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

func normalizePuterPublicModelDetails(rawModels []puterPublicModelDetails) []puterPublicModelChoice {
	if len(rawModels) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(rawModels))
	out := make([]puterPublicModelChoice, 0, len(rawModels))
	for _, raw := range rawModels {
		id, name, ok := normalizePuterModelDetails(raw)
		if !ok || id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, puterPublicModelChoice{ID: id, Name: name})
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

func normalizePuterModelID(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}

	prefix, remainder, ok := strings.Cut(raw, ":")
	if !ok {
		return "", false
	}
	prefix = canonicalPuterProvider(prefix)
	if _, allowed := puterChatCompletionProviders[prefix]; !allowed {
		return "", false
	}

	_, modelID, ok := strings.Cut(strings.TrimSpace(remainder), "/")
	if !ok {
		return "", false
	}
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return "", false
	}
	return modelID, true
}

func normalizePuterModelDetails(raw puterPublicModelDetails) (string, string, bool) {
	provider := canonicalPuterProvider(raw.Provider)
	if provider == "" {
		provider = providerFromPuterID(raw.PuterID)
	}
	if _, allowed := puterChatCompletionProviders[provider]; !allowed {
		return "", "", false
	}

	modelID := strings.TrimSpace(raw.ID)
	if modelID == "" {
		derivedID, ok := normalizePuterModelID(raw.PuterID)
		if !ok {
			return "", "", false
		}
		modelID = derivedID
	}

	name := strings.TrimSpace(raw.Name)
	if name == "" {
		name = modelID
	}
	return modelID, name, true
}

func providerFromPuterID(puterID string) string {
	prefix, _, ok := strings.Cut(strings.TrimSpace(puterID), ":")
	if !ok {
		return ""
	}
	return canonicalPuterProvider(prefix)
}

func canonicalPuterProvider(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return ""
	}
	if mapped, ok := puterProviderAliases[provider]; ok {
		return mapped
	}
	return provider
}
