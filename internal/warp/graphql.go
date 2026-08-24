package warp

import (
	"bytes"
	"context"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/goccy/go-json"
	"golang.org/x/sync/singleflight"
)

var (
	warpRefundGroup     singleflight.Group
	warpRefundedRequest sync.Map
)

type RequestLimitInfo struct {
	IsUnlimited                  bool   `json:"isUnlimited"`
	NextRefreshTime              string `json:"nextRefreshTime"`
	RequestLimit                 int    `json:"requestLimit"`
	RequestsUsedSinceLastRefresh int    `json:"requestsUsedSinceLastRefresh"`
}

type BonusGrant struct {
	RequestCreditsRemaining int `json:"requestCreditsRemaining"`
}

type ConversationUsageInfo struct {
	ConversationID            string  `json:"conversation_id"`
	LastUpdated               string  `json:"last_updated"`
	ContextWindowUsage        float64 `json:"context_window_usage"`
	CreditsSpent              float64 `json:"credits_spent"`
	PlatformCreditsSpent      float64 `json:"platform_credits_spent"`
	TotalProviderCostInCents  float64 `json:"total_provider_cost_in_cents"`
	TotalTokens               int     `json:"total_tokens"`
	SystemPromptTokens        int     `json:"system_prompt_tokens"`
	ToolDefinitionTokens      int     `json:"tool_definition_tokens"`
	ConversationHistoryTokens int     `json:"conversation_history_tokens"`
	LatestInputTokens         int     `json:"latest_input_tokens"`
}

const getRequestLimitInfoQuery = `query GetRequestLimitInfo($requestContext: RequestContext!) {
  user(requestContext: $requestContext) {
    __typename
    ... on UserOutput {
      user {
        workspaces {
		  bonusGrantsInfo {
			grants {
			  requestCreditsRemaining
			}
		  }
        }
        requestLimitInfo {
          isUnlimited
          nextRefreshTime
          requestLimit
          requestsUsedSinceLastRefresh
		}
		bonusGrants {
		  requestCreditsRemaining
        }
      }
    }
    ... on UserFacingError {
      error {
        __typename
        ... on SharedObjectsLimitExceeded {
          limit
          objectType
          message
        }
        ... on PersonalObjectsLimitExceeded {
          limit
          objectType
          message
        }
        ... on AccountDelinquencyError {
          message
        }
      }
    }
  }
}`

const refundCreditsMutation = `mutation ProvideNegativeFeedbackResponseForAiConversation($input: ProvideNegativeFeedbackResponseForAiConversationInput!, $requestContext: RequestContext!) {
  provideNegativeFeedbackResponseForAiConversation(input: $input, requestContext: $requestContext) {
    __typename
    ... on RequestsRefundedOutput {
      requestsRefunded
    }
  }
}`

const getConversationUsageQuery = `query GetConversationUsage($requestContext: RequestContext!, $days: Int, $limit: Int) {
  user(requestContext: $requestContext) {
    __typename
    ... on UserOutput {
      user {
        conversationUsage(days: $days, limit: $limit) {
          conversationId
          lastUpdated
          usageMetadata {
            contextWindowUsage
            creditsSpent
            platformCreditsSpent
            totalProviderCostInCents
            tokenUsage { modelId totalTokens }
            contextWindowSegments { segmentType tokenCount }
          }
        }
      }
    }
  }
}`

func fetchRequestLimitInfo(ctx context.Context, client *http.Client, jwt string) (*RequestLimitInfo, []BonusGrant, error) {
	payload := map[string]interface{}{
		"query":         getRequestLimitInfoQuery,
		"operationName": "GetRequestLimitInfo",
		"variables": map[string]interface{}{
			"requestContext": requestContextPayload(),
		},
	}

	var resp struct {
		Data struct {
			User struct {
				Type string `json:"__typename"`
				User struct {
					Workspaces []struct {
						BonusGrantsInfo struct {
							Grants []BonusGrant `json:"grants"`
						} `json:"bonusGrantsInfo"`
					} `json:"workspaces"`
					RequestLimitInfo struct {
						IsUnlimited                  bool    `json:"isUnlimited"`
						NextRefreshTime              string  `json:"nextRefreshTime"`
						RequestLimit                 float64 `json:"requestLimit"`
						RequestsUsedSinceLastRefresh float64 `json:"requestsUsedSinceLastRefresh"`
					} `json:"requestLimitInfo"`
					BonusGrants []BonusGrant `json:"bonusGrants"`
				} `json:"user"`
			} `json:"user"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := doGraphQL(ctx, client, warpGraphQLV2URL, jwt, "GetRequestLimitInfo", payload, &resp); err != nil {
		return nil, nil, err
	}
	if len(resp.Errors) > 0 {
		return nil, nil, fmt.Errorf("warp graphql: %s", resp.Errors[0].Message)
	}
	if !strings.EqualFold(strings.TrimSpace(resp.Data.User.Type), "UserOutput") {
		return nil, nil, fmt.Errorf("warp graphql returned %q for request limit info", strings.TrimSpace(resp.Data.User.Type))
	}

	info := resp.Data.User.User.RequestLimitInfo
	requestLimit := int(info.RequestLimit)
	used := int(info.RequestsUsedSinceLastRefresh)
	if used < 0 {
		used = 0
	}

	bonuses := resp.Data.User.User.BonusGrants
	if len(bonuses) == 0 {
		for _, workspace := range resp.Data.User.User.Workspaces {
			if len(workspace.BonusGrantsInfo.Grants) == 0 {
				continue
			}
			bonuses = append(bonuses, workspace.BonusGrantsInfo.Grants...)
		}
	}
	return &RequestLimitInfo{
		IsUnlimited:                  info.IsUnlimited,
		NextRefreshTime:              strings.TrimSpace(info.NextRefreshTime),
		RequestLimit:                 requestLimit,
		RequestsUsedSinceLastRefresh: used,
	}, bonuses, nil
}

func refundCredits(ctx context.Context, client *http.Client, jwt, conversationID string, requestIDs []string) error {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return fmt.Errorf("warp conversation id is empty")
	}
	uniqueIDs := make([]string, 0, len(requestIDs))
	seen := make(map[string]struct{}, len(requestIDs))
	for _, requestID := range requestIDs {
		requestID = strings.TrimSpace(requestID)
		if requestID == "" {
			continue
		}
		if _, exists := seen[requestID]; exists {
			continue
		}
		seen[requestID] = struct{}{}
		uniqueIDs = append(uniqueIDs, requestID)
	}
	if len(uniqueIDs) == 0 {
		return fmt.Errorf("warp request ids are empty")
	}

	payload := map[string]interface{}{
		"query":         refundCreditsMutation,
		"operationName": "ProvideNegativeFeedbackResponseForAiConversation",
		"variables": map[string]interface{}{
			"input": map[string]interface{}{
				"conversationId": conversationID,
				"requestIds":     uniqueIDs,
			},
			"requestContext": requestContextPayload(),
		},
	}

	var resp struct {
		Data struct {
			RefundCredits struct {
				Type             string `json:"__typename"`
				RequestsRefunded int    `json:"requestsRefunded"`
			} `json:"provideNegativeFeedbackResponseForAiConversation"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := doGraphQL(ctx, client, warpGraphQLV2URL, jwt, "ProvideNegativeFeedbackResponseForAiConversation", payload, &resp); err != nil {
		return err
	}
	if len(resp.Errors) > 0 {
		return fmt.Errorf("warp refund: %s", resp.Errors[0].Message)
	}
	if resp.Data.RefundCredits.RequestsRefunded < len(uniqueIDs) {
		return fmt.Errorf("warp refund returned %s with %d/%d requests refunded", resp.Data.RefundCredits.Type, resp.Data.RefundCredits.RequestsRefunded, len(uniqueIDs))
	}
	return nil
}

func fetchConversationUsage(ctx context.Context, client *http.Client, jwt, conversationID string) (*ConversationUsageInfo, error) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil, fmt.Errorf("warp conversation id is empty")
	}
	payload := map[string]interface{}{
		"query":         getConversationUsageQuery,
		"operationName": "GetConversationUsage",
		"variables": map[string]interface{}{
			"requestContext": requestContextPayload(),
			"days":           7,
			"limit":          100,
		},
	}
	var response struct {
		Data struct {
			User struct {
				Type string `json:"__typename"`
				User struct {
					ConversationUsage []struct {
						ConversationID string `json:"conversationId"`
						LastUpdated    string `json:"lastUpdated"`
						UsageMetadata  struct {
							ContextWindowUsage       float64 `json:"contextWindowUsage"`
							CreditsSpent             float64 `json:"creditsSpent"`
							PlatformCreditsSpent     float64 `json:"platformCreditsSpent"`
							TotalProviderCostInCents float64 `json:"totalProviderCostInCents"`
							TokenUsage               []struct {
								TotalTokens int `json:"totalTokens"`
							} `json:"tokenUsage"`
							ContextWindowSegments []struct {
								SegmentType string `json:"segmentType"`
								TokenCount  int    `json:"tokenCount"`
							} `json:"contextWindowSegments"`
						} `json:"usageMetadata"`
					} `json:"conversationUsage"`
				} `json:"user"`
			} `json:"user"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := doGraphQL(ctx, client, warpGraphQLV2URL, jwt, "GetConversationUsage", payload, &response); err != nil {
		return nil, err
	}
	if len(response.Errors) > 0 {
		return nil, fmt.Errorf("warp conversation usage: %s", response.Errors[0].Message)
	}
	if !strings.EqualFold(strings.TrimSpace(response.Data.User.Type), "UserOutput") {
		return nil, fmt.Errorf("warp graphql returned %q for conversation usage", response.Data.User.Type)
	}
	for _, item := range response.Data.User.User.ConversationUsage {
		if strings.TrimSpace(item.ConversationID) != conversationID {
			continue
		}
		usage := &ConversationUsageInfo{
			ConversationID:           conversationID,
			LastUpdated:              strings.TrimSpace(item.LastUpdated),
			ContextWindowUsage:       item.UsageMetadata.ContextWindowUsage,
			CreditsSpent:             item.UsageMetadata.CreditsSpent,
			PlatformCreditsSpent:     item.UsageMetadata.PlatformCreditsSpent,
			TotalProviderCostInCents: item.UsageMetadata.TotalProviderCostInCents,
		}
		for _, tokens := range item.UsageMetadata.TokenUsage {
			usage.TotalTokens += tokens.TotalTokens
		}
		for _, segment := range item.UsageMetadata.ContextWindowSegments {
			switch strings.ToUpper(strings.TrimSpace(segment.SegmentType)) {
			case "SYSTEM_PROMPT":
				usage.SystemPromptTokens += segment.TokenCount
			case "TOOL_DEFINITIONS":
				usage.ToolDefinitionTokens += segment.TokenCount
			case "CONVERSATION_HISTORY":
				usage.ConversationHistoryTokens += segment.TokenCount
			case "LATEST_INPUT":
				usage.LatestInputTokens += segment.TokenCount
			}
		}
		return usage, nil
	}
	return nil, fmt.Errorf("warp conversation usage not found for %s", conversationID)
}

func doGraphQL(ctx context.Context, client *http.Client, endpointURL, jwt, operationName string, body interface{}, target interface{}) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("warp graphql marshal request: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	endpoint := strings.TrimSpace(endpointURL)
	if endpoint == "" {
		endpoint = warpGraphQLURL
	}
	if op := strings.TrimSpace(operationName); op != "" && strings.Contains(endpoint, "/graphql/v2") {
		endpoint = endpoint + "?op=" + url.QueryEscape(op)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("warp graphql create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(jwt))
	applyWarpClientHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Encoding", "gzip")

	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("warp graphql request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := readLimitedBody(resp, 2<<20)
	if err != nil {
		return fmt.Errorf("warp graphql read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return &HTTPStatusError{
			Operation:  "graphql request",
			StatusCode: resp.StatusCode,
			ErrorCode:  resp.Header.Get("X-Warp-Error-Code"),
			RetryAfter: parseRetryAfterHeader(resp.Header.Get("Retry-After"), time.Now()),
			Body:       strings.TrimSpace(string(bodyBytes)),
		}
	}
	if err := json.Unmarshal(bodyBytes, target); err != nil {
		contentType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		preview := strings.TrimSpace(string(bodyBytes))
		if len(preview) > 240 {
			preview = preview[:240]
		}
		return fmt.Errorf("warp graphql decode response (content-type %q, body %q): %w", contentType, preview, err)
	}
	return nil
}

func (c *Client) GetRequestLimitInfo(ctx context.Context) (*RequestLimitInfo, []BonusGrant, error) {
	client, err := c.ensureAuthenticated(ctx, false)
	if err != nil {
		return nil, nil, err
	}
	return fetchRequestLimitInfo(ctx, client, c.session.currentJWT())
}

func (c *Client) GetConversationUsage(ctx context.Context, conversationID string) (*ConversationUsageInfo, error) {
	client, err := c.ensureAuthenticated(ctx, false)
	if err != nil {
		return nil, err
	}
	return fetchConversationUsage(ctx, client, c.session.currentJWT(), conversationID)
}

func (c *Client) RefundCredits(ctx context.Context, conversationID, requestID string) error {
	return c.RefundCreditRequests(ctx, conversationID, []string{requestID})
}

func (c *Client) RefundCreditRequests(ctx context.Context, conversationID string, requestIDs []string) error {
	requestIDs = normalizeRequestIDs(requestIDs)
	if len(requestIDs) == 0 {
		return fmt.Errorf("warp refund request ids missing")
	}
	client, err := c.ensureAuthenticated(ctx, false)
	if err != nil {
		return err
	}
	key := strings.TrimSpace(conversationID) + ":" + strings.Join(requestIDs, ",")
	if _, refunded := warpRefundedRequest.Load(key); refunded {
		return nil
	}
	_, err, _ = warpRefundGroup.Do(key, func() (interface{}, error) {
		if _, refunded := warpRefundedRequest.Load(key); refunded {
			return nil, nil
		}
		if refundErr := refundCredits(ctx, client, c.session.currentJWT(), conversationID, requestIDs); refundErr != nil {
			return nil, refundErr
		}
		warpRefundedRequest.Store(key, struct{}{})
		return nil, nil
	})
	return err
}

func normalizeRequestIDs(requestIDs []string) []string {
	seen := make(map[string]struct{}, len(requestIDs))
	normalized := make([]string, 0, len(requestIDs))
	for _, requestID := range requestIDs {
		requestID = strings.TrimSpace(requestID)
		if requestID == "" {
			continue
		}
		if _, ok := seen[requestID]; ok {
			continue
		}
		seen[requestID] = struct{}{}
		normalized = append(normalized, requestID)
	}
	return normalized
}

func requestContextPayload() map[string]interface{} {
	return map[string]interface{}{
		"clientContext": map[string]interface{}{
			"version": clientVersion,
		},
		"osContext": map[string]interface{}{
			"category":           warpOSCategory(),
			"linuxKernelVersion": nil,
			"name":               warpOSCategory(),
			"version":            "",
		},
	}
}
