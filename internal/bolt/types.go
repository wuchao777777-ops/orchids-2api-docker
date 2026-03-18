package bolt

import "github.com/goccy/go-json"

type Request struct {
	ID                   string          `json:"id"`
	Messages             []Message       `json:"messages"`
	IsFirstPrompt        bool            `json:"isFirstPrompt"`
	FeaturePreviews      FeaturePreviews `json:"featurePreviews"`
	ErrorReasoning       *string         `json:"errorReasoning"`
	PromptMode           string          `json:"promptMode"`
	SelectedModel        string          `json:"selectedModel"`
	EffortLevel          string          `json:"effortLevel"`
	ProjectID            string          `json:"projectId"`
	StripeStatus         string          `json:"stripeStatus"`
	UsesInspectedElement bool            `json:"usesInspectedElement"`
	SupportIntegrations  bool            `json:"supportIntegrations"`
	RunningCommands      []interface{}   `json:"runningCommands"`
	ProjectFiles         ProjectFiles    `json:"projectFiles"`
	GlobalSystemPrompt   string          `json:"globalSystemPrompt"`
	ProjectPrompt        string          `json:"projectPrompt"`
	Dependencies         []interface{}   `json:"dependencies"`
	HostingProvider      string          `json:"hostingProvider"`
	Problems             string          `json:"problems"`
}

type FeaturePreviews struct {
	Reasoning bool `json:"reasoning"`
	Diffs     bool `json:"diffs"`
}

type ProjectFiles struct {
	Visible []interface{} `json:"visible"`
	Hidden  []interface{} `json:"hidden"`
}

type Message struct {
	ID          string       `json:"id"`
	Role        string       `json:"role"`
	Content     string       `json:"content"`
	RawContent  string       `json:"rawContent,omitempty"`
	Cache       bool         `json:"cache"`
	Parts       []Part       `json:"parts"`
	Annotations []Annotation `json:"annotations,omitempty"`
}

type Part struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type Annotation struct {
	Type          string `json:"type"`
	UserMessageID string `json:"userMessageId"`
}

type EndEvent struct {
	FinishReason string    `json:"finishReason"`
	IsContinued  bool      `json:"isContinued,omitempty"`
	Usage        BoltUsage `json:"usage"`
}

type BoltUsage struct {
	PromptTokens     int `json:"promptTokens"`
	CompletionTokens int `json:"completionTokens"`
}

type ToolCall struct {
	Function   string          `json:"function"`
	Parameters json.RawMessage `json:"parameters"`
}

type RootData struct {
	User  *RootUser `json:"user"`
	Token string    `json:"token"`
}

type RateLimits struct {
	BillingPeriod  *BillingPeriod  `json:"billingPeriod"`
	MaxPerDay      float64         `json:"maxPerDay"`
	MaxPerMonth    float64         `json:"maxPerMonth"`
	RegularTokens  *TokenBalance   `json:"regularTokens"`
	RewardTokens   *TokenBalance   `json:"rewardTokens"`
	Overflow       *TokenBalance   `json:"overflow"`
	Purchased      *TokenBalance   `json:"purchased"`
	ReferralTokens *ReferralTokens `json:"referralTokens"`
	SpecialTokens  *TokenBalance   `json:"specialTokens"`
	TotalThisMonth float64         `json:"totalThisMonth"`
	TotalToday     float64         `json:"totalToday"`
}

type BillingPeriod struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

type TokenBalance struct {
	Available float64 `json:"available"`
	Used      float64 `json:"used"`
}

type ReferralTokens struct {
	Free *TokenBalance `json:"free"`
	Paid *TokenBalance `json:"paid"`
}

type RootUser struct {
	ID                          string            `json:"id"`
	Email                       string            `json:"email"`
	Name                        string            `json:"name"`
	Username                    string            `json:"username"`
	ActiveOrganizationID        int64             `json:"activeOrganizationId"`
	TotalBoltTokenPurchases     float64           `json:"totalBoltTokenPurchases"`
	Membership                  *Membership       `json:"membership"`
	Organizations               []Organization    `json:"organizations"`
	TokenAllocations            []TokenAllocation `json:"tokenAllocations"`
	ExpirableBoltTokenPurchases []TokenAllocation `json:"expirableBoltTokenPurchases"`
}

type Membership struct {
	Tier         interface{}   `json:"tier"`
	Paid         bool          `json:"paid"`
	SubscribedAt string        `json:"subscribedAt"`
	Subscription *Subscription `json:"subscription"`
}

type Organization struct {
	ID       int64                `json:"id"`
	Role     string               `json:"role"`
	Slug     string               `json:"slug"`
	Provider string               `json:"provider"`
	Billing  *OrganizationBilling `json:"billing"`
}

type OrganizationBilling struct {
	Tier         interface{}   `json:"tier"`
	Paid         bool          `json:"paid"`
	Custom       bool          `json:"custom"`
	Subscription *Subscription `json:"subscription"`
}

type Subscription struct {
	Status   string  `json:"status"`
	Interval string  `json:"interval"`
	PlanCost float64 `json:"planCost"`
	Quantity int     `json:"quantity"`
}

type TokenAllocation struct {
	Kind      string  `json:"kind"`
	Tokens    float64 `json:"tokens"`
	Remaining float64 `json:"remaining"`
	Amount    float64 `json:"amount"`
	Balance   float64 `json:"balance"`
}
