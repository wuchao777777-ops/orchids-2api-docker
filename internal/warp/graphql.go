package warp

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/goccy/go-json"
)

const graphqlURL = "https://app.warp.dev/graphql/v2"

// RequestLimitInfo holds the user's request limit and usage information.
type RequestLimitInfo struct {
	IsUnlimited                  bool   `json:"isUnlimited"`
	NextRefreshTime              string `json:"nextRefreshTime"`
	RequestLimit                 int    `json:"requestLimit"`
	RequestsUsedSinceLastRefresh int    `json:"requestsUsedSinceLastRefresh"`
	RequestLimitRefreshDuration  string `json:"requestLimitRefreshDuration"`
}

// BonusGrant holds bonus credit grant information.
type BonusGrant struct {
	RequestCreditsGranted   int    `json:"requestCreditsGranted"`
	RequestCreditsRemaining int    `json:"requestCreditsRemaining"`
	Expiration              string `json:"expiration"`
	Reason                  string `json:"reason"`
}

// UsageMetadata holds credit/request multiplier info for a model choice.
type UsageMetadata struct {
	CreditMultiplier  float64 `json:"creditMultiplier"`
	RequestMultiplier float64 `json:"requestMultiplier"`
}

// ModelSpec holds cost/quality/speed ratings for a model choice.
// Values may be strings ("low","high") or numbers from the API.
type ModelSpec struct {
	Cost    interface{} `json:"cost"`
	Quality interface{} `json:"quality"`
	Speed   interface{} `json:"speed"`
}

// ModelChoice represents a single model option within a feature category.
type ModelChoice struct {
	DisplayName     string        `json:"displayName"`
	BaseModelName   string        `json:"baseModelName"`
	ID              string        `json:"id"`
	ReasoningLevel  string        `json:"reasoningLevel"`
	UsageMetadata   UsageMetadata `json:"usageMetadata"`
	Description     string        `json:"description"`
	DisableReason   string        `json:"disableReason"`
	VisionSupported bool          `json:"visionSupported"`
	Spec            ModelSpec     `json:"spec"`
	Provider        string        `json:"provider"`
}

// FeatureModelCategory holds the default and available choices for a feature.
type FeatureModelCategory struct {
	DefaultID string        `json:"defaultId"`
	Choices   []ModelChoice `json:"choices"`
}

// FeatureModelChoices holds model choices for all feature categories.
type FeatureModelChoices struct {
	AgentMode *FeatureModelCategory `json:"agentMode"`
	Planning  *FeatureModelCategory `json:"planning"`
	Coding    *FeatureModelCategory `json:"coding"`
	CliAgent  *FeatureModelCategory `json:"cliAgent"`
}

// graphqlRequest is the generic GraphQL request envelope.
type graphqlRequest struct {
	Query     string      `json:"query"`
	Variables interface{} `json:"variables"`
}

// requestContext matches the Warp GraphQL RequestContext input type.
type requestContext struct {
	ClientContext clientContext `json:"clientContext"`
	OSContext     osContext     `json:"osContext"`
}

type clientContext struct {
	Version string `json:"version"`
}

type osContext struct {
	Category           string  `json:"category"`
	LinuxKernelVersion *string `json:"linuxKernelVersion"`
	Name               string  `json:"name"`
	Version            string  `json:"version"`
}

func defaultRequestContext() requestContext {
	return requestContext{
		ClientContext: clientContext{Version: clientVersion},
		OSContext: osContext{
			Category:           osCategory,
			LinuxKernelVersion: nil,
			Name:               osName,
			Version:            osVersion,
		},
	}
}

const getRequestLimitInfoQuery = `query GetRequestLimitInfo($requestContext: RequestContext!) {
  user(requestContext: $requestContext) {
    __typename
    ... on UserOutput {
      user {
        requestLimitInfo {
          isUnlimited
          nextRefreshTime
          requestLimit
          requestsUsedSinceLastRefresh
          requestLimitRefreshDuration
        }
        bonusGrants {
          requestCreditsGranted
          requestCreditsRemaining
          expiration
          reason
        }
      }
    }
    ... on UserFacingError {
      error { __typename message }
    }
  }
}`

const getFeatureModelChoicesQuery = `query GetFeatureModelChoices($requestContext: RequestContext!) {
  user(requestContext: $requestContext) {
    __typename
    ... on UserOutput {
      user {
        workspaces {
          featureModelChoice {
            agentMode { defaultId, choices { displayName, baseModelName, id, reasoningLevel, usageMetadata { creditMultiplier, requestMultiplier }, description, disableReason, visionSupported, spec { cost, quality, speed }, provider } }
            planning { defaultId, choices { displayName, baseModelName, id, reasoningLevel, usageMetadata { creditMultiplier, requestMultiplier }, description, disableReason, visionSupported, spec { cost, quality, speed }, provider } }
            coding { defaultId, choices { displayName, baseModelName, id, reasoningLevel, usageMetadata { creditMultiplier, requestMultiplier }, description, disableReason, visionSupported, spec { cost, quality, speed }, provider } }
            cliAgent { defaultId, choices { displayName, baseModelName, id, reasoningLevel, usageMetadata { creditMultiplier, requestMultiplier }, description, disableReason, visionSupported, spec { cost, quality, speed }, provider } }
          }
        }
      }
    }
    ... on UserFacingError {
      error { __typename message }
    }
  }
}`

type graphqlResponse struct {
	Data struct {
		User struct {
			Typename string `json:"__typename"`
			User     struct {
				RequestLimitInfo *RequestLimitInfo `json:"requestLimitInfo"`
				BonusGrants      []BonusGrant      `json:"bonusGrants"`
				Workspaces       []struct {
					FeatureModelChoice struct {
						AgentMode *FeatureModelCategory `json:"agentMode"`
						Planning  *FeatureModelCategory `json:"planning"`
						Coding    *FeatureModelCategory `json:"coding"`
						CliAgent  *FeatureModelCategory `json:"cliAgent"`
					} `json:"featureModelChoice"`
				} `json:"workspaces"`
			} `json:"user"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"user"`
	} `json:"data"`
	Errors []interface{} `json:"errors"`
}

// FetchRequestLimitInfo queries the Warp GraphQL API for the user's request
// limit info and bonus grants.
func FetchRequestLimitInfo(ctx context.Context, client *http.Client, jwt string) (*RequestLimitInfo, []BonusGrant, error) {
	body := graphqlRequest{
		Query: getRequestLimitInfoQuery,
		Variables: map[string]interface{}{
			"requestContext": defaultRequestContext(),
		},
	}

	var resp graphqlResponse
	if err := doGraphQL(ctx, client, jwt, "GetRequestLimitInfo", body, &resp); err != nil {
		return nil, nil, err
	}

	if resp.Data.User.Typename == "UserFacingError" {
		msg := "unknown error"
		if resp.Data.User.Error != nil && resp.Data.User.Error.Message != "" {
			msg = resp.Data.User.Error.Message
		}
		return nil, nil, fmt.Errorf("warp graphql: %s", msg)
	}

	limitInfo := resp.Data.User.User.RequestLimitInfo
	if limitInfo == nil {
		limitInfo = &RequestLimitInfo{}
	}
	return limitInfo, resp.Data.User.User.BonusGrants, nil
}

// FetchFeatureModelChoices queries the Warp GraphQL API for available model
// choices across feature categories.
func FetchFeatureModelChoices(ctx context.Context, client *http.Client, jwt string) (*FeatureModelChoices, error) {
	body := graphqlRequest{
		Query: getFeatureModelChoicesQuery,
		Variables: map[string]interface{}{
			"requestContext": defaultRequestContext(),
		},
	}

	var resp graphqlResponse
	if err := doGraphQL(ctx, client, jwt, "GetFeatureModelChoices", body, &resp); err != nil {
		return nil, err
	}

	if resp.Data.User.Typename == "UserFacingError" {
		msg := "unknown error"
		if resp.Data.User.Error != nil && resp.Data.User.Error.Message != "" {
			msg = resp.Data.User.Error.Message
		}
		return nil, fmt.Errorf("warp graphql: UserFacingError: %s", msg)
	}

	if len(resp.Data.User.User.Workspaces) == 0 {
		return nil, fmt.Errorf("warp graphql: no workspaces found")
	}

	fmc := resp.Data.User.User.Workspaces[0].FeatureModelChoice
	result := &FeatureModelChoices{
		AgentMode: fmc.AgentMode,
		Planning:  fmc.Planning,
		Coding:    fmc.Coding,
		CliAgent:  fmc.CliAgent,
	}

	return result, nil
}

// doGraphQL sends a GraphQL request to the Warp API and unmarshals the
// JSON response into the target struct.
func doGraphQL(ctx context.Context, client *http.Client, jwt, operationName string, body graphqlRequest, target interface{}) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("warp graphql: marshal request: %w", err)
	}

	reqURL := graphqlURL + "?op=" + operationName

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("warp graphql: create request: %w", err)
	}

	req.Header.Set("authorization", "Bearer "+jwt)
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-warp-client-id", clientID)
	req.Header.Set("x-warp-client-version", clientVersion)
	req.Header.Set("x-warp-os-category", osCategory)
	req.Header.Set("x-warp-os-name", osName)
	req.Header.Set("x-warp-os-version", osVersion)
	req.Header.Set("accept", "*/*")
	req.Header.Set("accept-encoding", "gzip")

	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("warp graphql %s: %w", operationName, err)
	}
	defer resp.Body.Close()

	var reader io.ReadCloser = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		reader, err = gzip.NewReader(resp.Body)
		if err != nil {
			return fmt.Errorf("warp graphql: gzip decode: %w", err)
		}
		defer reader.Close()
	}

	respBody, err := io.ReadAll(io.LimitReader(reader, 2<<20)) // 2 MB max
	if err != nil {
		return fmt.Errorf("warp graphql %s: read body: %w", operationName, err)
	}

	if resp.StatusCode != http.StatusOK {
		slog.Warn("warp graphql request failed", "op", operationName, "status", resp.StatusCode, "body", string(respBody))
		return fmt.Errorf("warp graphql %s: HTTP %d: %s", operationName, resp.StatusCode, string(respBody))
	}

	if err := json.Unmarshal(respBody, target); err != nil {
		return fmt.Errorf("warp graphql %s: unmarshal response: %w", operationName, err)
	}

	// Check if target is graphqlResponse to handle generic errors
	if gr, ok := target.(*graphqlResponse); ok {
		if len(gr.Errors) > 0 {
			b, _ := json.Marshal(gr.Errors)
			return fmt.Errorf("warp graphql %s: errors: %s", operationName, string(b))
		}
	}

	return nil
}

// GetRequestLimitInfo fetches the user's request limit info using the
// client's session JWT, ensuring the token is refreshed.
func (c *Client) GetRequestLimitInfo(ctx context.Context) (*RequestLimitInfo, []BonusGrant, error) {
	if c.session == nil {
		return nil, nil, fmt.Errorf("warp session not initialized")
	}
	cid := clientID
	if c.account != nil {
		cid = fmt.Sprintf("warp-%d", c.account.ID)
	}
	if err := c.session.ensureToken(ctx, c.httpClient, cid); err != nil {
		return nil, nil, fmt.Errorf("warp graphql: ensureToken: %w", err)
	}
	jwt := c.session.currentJWT()
	if jwt == "" {
		return nil, nil, fmt.Errorf("warp graphql: jwt missing")
	}
	return FetchRequestLimitInfo(ctx, c.httpClient, jwt)
}

// GetFeatureModelChoices fetches available model choices using the client's
// session JWT, ensuring the token is refreshed.
func (c *Client) GetFeatureModelChoices(ctx context.Context) (*FeatureModelChoices, error) {
	if c.session == nil {
		return nil, fmt.Errorf("warp session not initialized")
	}
	cid := clientID
	if c.account != nil {
		cid = fmt.Sprintf("warp-%d", c.account.ID)
	}
	if err := c.session.ensureToken(ctx, c.httpClient, cid); err != nil {
		return nil, fmt.Errorf("warp graphql: ensureToken: %w", err)
	}
	jwt := c.session.currentJWT()
	if jwt == "" {
		return nil, fmt.Errorf("warp graphql: jwt missing")
	}
	return FetchFeatureModelChoices(ctx, c.httpClient, jwt)
}
