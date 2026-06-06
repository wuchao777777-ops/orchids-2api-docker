package warp

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

type ModelChoice struct {
	ID   string
	Name string
}

const getFeatureModelChoicesQuery = `query GetFeatureModelChoices($requestContext: RequestContext!) {
  user(requestContext: $requestContext) {
    __typename
    ... on UserOutput {
      user {
        workspaces {
          featureModelChoice {
            agentMode {
              defaultId
              choices {
                id
                displayName
              }
            }
          }
        }
      }
    }
    ... on UserFacingError {
      error {
        message
      }
    }
  }
}`

func (c *Client) FetchDiscoveredModelChoices(ctx context.Context) ([]ModelChoice, string, error) {
	if c == nil || c.session == nil {
		return nil, "", fmt.Errorf("warp session not initialized")
	}

	client := c.authHTTPClient()
	if err := c.session.ensureToken(ctx, client); err != nil {
		return nil, "", err
	}

	jwt := c.session.currentJWT()
	agentChoices, defaultID, err := fetchFeatureAgentModeModelChoices(ctx, client, jwt)

	if len(agentChoices) > 0 {
		return mergeWarpModelChoices(defaultID, agentChoices), "feature_model_choice_agent_mode", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("warp model discovery failed: %w", err)
	}
	return nil, "", fmt.Errorf("warp model discovery returned no choices")
}

func fetchFeatureAgentModeModelChoices(ctx context.Context, client *http.Client, jwt string) ([]ModelChoice, string, error) {
	payload := map[string]interface{}{
		"query":         getFeatureModelChoicesQuery,
		"operationName": "GetFeatureModelChoices",
		"variables": map[string]interface{}{
			"requestContext": requestContextPayload(),
		},
	}

	var resp struct {
		Data struct {
			User struct {
				Type  string `json:"__typename"`
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
				User struct {
					Workspaces []struct {
						FeatureModelChoice struct {
							AgentMode struct {
								DefaultID string `json:"defaultId"`
								Choices   []struct {
									ID          string `json:"id"`
									DisplayName string `json:"displayName"`
								} `json:"choices"`
							} `json:"agentMode"`
						} `json:"featureModelChoice"`
					} `json:"workspaces"`
				} `json:"user"`
			} `json:"user"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := doGraphQL(ctx, client, warpGraphQLV2URL, jwt, "GetFeatureModelChoices", payload, &resp); err != nil {
		return nil, "", err
	}
	if len(resp.Errors) > 0 {
		return nil, "", fmt.Errorf("warp graphql: %s", resp.Errors[0].Message)
	}
	if !strings.EqualFold(strings.TrimSpace(resp.Data.User.Type), "UserOutput") {
		if msg := strings.TrimSpace(resp.Data.User.Error.Message); msg != "" {
			return nil, "", fmt.Errorf("warp graphql: %s", msg)
		}
		return nil, "", fmt.Errorf("warp graphql returned %q for feature model choices", strings.TrimSpace(resp.Data.User.Type))
	}

	var choices []ModelChoice
	defaultID := ""
	for _, workspace := range resp.Data.User.User.Workspaces {
		agentMode := workspace.FeatureModelChoice.AgentMode
		if defaultID == "" {
			defaultID = canonicalModelID(agentMode.DefaultID)
		}
		for _, choice := range agentMode.Choices {
			if normalized, ok := normalizeWarpModelChoice(choice.ID, choice.DisplayName); ok {
				choices = append(choices, normalized)
			}
		}
	}
	return choices, defaultID, nil
}

func normalizeWarpModelChoice(id, name string) (ModelChoice, bool) {
	id = canonicalModelID(id)
	if id == "" {
		return ModelChoice{}, false
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = id
	}
	return ModelChoice{
		ID:   id,
		Name: name,
	}, true
}

func mergeWarpModelChoices(defaultID string, groups ...[]ModelChoice) []ModelChoice {
	defaultID = canonicalModelID(defaultID)

	out := make([]ModelChoice, 0)
	seen := map[string]struct{}{}
	for _, group := range groups {
		for _, choice := range group {
			normalized, ok := normalizeWarpModelChoice(choice.ID, choice.Name)
			if !ok {
				continue
			}
			if _, exists := seen[normalized.ID]; exists {
				continue
			}
			seen[normalized.ID] = struct{}{}
			out = append(out, normalized)
		}
	}

	if defaultID == "" || len(out) < 2 {
		return out
	}
	for i := 1; i < len(out); i++ {
		if out[i].ID != defaultID {
			continue
		}
		defaultChoice := out[i]
		copy(out[1:i+1], out[0:i])
		out[0] = defaultChoice
		break
	}
	return out
}
