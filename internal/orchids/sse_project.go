package orchids

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/goccy/go-json"
)

const orchidsProjectCreateURL = "https://www.orchids.app/api/projects/create"

var errOrchidsProjectBootstrapUnavailable = errors.New("orchids createProject unavailable without clerk session cookies")

func (c *Client) createProject(ctx context.Context) (string, error) {
	if c == nil || c.config == nil {
		return "", errOrchidsProjectBootstrapUnavailable
	}

	if strings.TrimSpace(c.config.ClientCookie) == "" && strings.TrimSpace(c.config.SessionCookie) != "" {
		_ = c.bootstrapClientCookieFromSession()
	}

	cookieHeader, ok := orchidsProjectCookieHeader(c.config.ClientCookie, c.config.SessionCookie, c.config.ClientUat, c.config.SessionID)
	if !ok {
		return "", errOrchidsProjectBootstrapUnavailable
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, orchidsProjectCreateURL, bytes.NewReader([]byte("{}")))
	if err != nil {
		return "", err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", orchidsWSOrigin)
	req.Header.Set("Referer", orchidsWSOrigin+"/")
	req.Header.Set("User-Agent", orchidsWSUserAgent)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Cookie", cookieHeader)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read project create response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("project create failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	projectID, err := decodeOrchidsProjectCreateResponse(body)
	if err != nil {
		return "", err
	}
	c.applyProjectID(projectID)
	return projectID, nil
}

func orchidsProjectCookieHeader(clientCookie, sessionCookie, clientUat, sessionID string) (string, bool) {
	clientCookie = strings.TrimSpace(clientCookie)
	sessionCookie = strings.TrimSpace(sessionCookie)
	clientUat = strings.TrimSpace(clientUat)
	sessionID = strings.TrimSpace(sessionID)

	if clientCookie == "" {
		return "", false
	}
	if sessionCookie == "" && clientUat == "" {
		return "", false
	}
	return orchidsClerkCookieHeader(clientCookie, sessionCookie, clientUat, sessionID), true
}

func decodeOrchidsProjectCreateResponse(body []byte) (string, error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return "", errors.New("project create response body is empty")
	}

	var payload interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("failed to decode project create response: %w", err)
	}

	projectID := extractOrchidsProjectID(payload)
	if projectID == "" {
		return "", errors.New("project create response missing project id")
	}
	return projectID, nil
}

func extractOrchidsProjectID(value interface{}) string {
	switch typed := value.(type) {
	case map[string]interface{}:
		for _, key := range []string{"projectId", "project_id"} {
			if projectID, _ := typed[key].(string); strings.TrimSpace(projectID) != "" {
				return strings.TrimSpace(projectID)
			}
		}
		for _, key := range []string{"project", "data", "result", "response"} {
			if child, ok := typed[key]; ok {
				if projectID := extractOrchidsProjectID(child); projectID != "" {
					return projectID
				}
			}
		}
		if projectID, _ := typed["id"].(string); strings.TrimSpace(projectID) != "" {
			return strings.TrimSpace(projectID)
		}
		for _, child := range typed {
			if projectID := extractOrchidsProjectID(child); projectID != "" {
				return projectID
			}
		}
	case []interface{}:
		for _, item := range typed {
			if projectID := extractOrchidsProjectID(item); projectID != "" {
				return projectID
			}
		}
	}
	return ""
}

func (c *Client) applyProjectID(projectID string) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return
	}
	if c.config != nil {
		c.config.ProjectID = projectID
	}
	if c.account != nil {
		c.account.ProjectID = projectID
	}
}
