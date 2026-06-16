package auth

type StoredOAuthCredential struct {
	Provider        string `json:"provider,omitempty"`
	Access          string `json:"access"`
	Refresh         string `json:"refresh"`
	Expires         int64  `json:"expires"`
	AccountID       string `json:"accountId"`
	Email           string `json:"email,omitempty"`
	ChatGPTPlanType string `json:"chatgptPlanType,omitempty"`
	BaseURL         string `json:"baseUrl,omitempty"`
}

type AuthProfileSaveResult struct {
	ProjectID string `json:"projectId"`
	ProfileID string `json:"profileId"`
	SavedAt   int64  `json:"savedAt"`
}

type AuthIdentity struct {
	AccountID       string `json:"accountId,omitempty"`
	Email           string `json:"email,omitempty"`
	ChatGPTPlanType string `json:"chatgptPlanType,omitempty"`
	ProfileName     string `json:"profileName,omitempty"`
	ExpiresAt       int64  `json:"expiresAt,omitempty"`
}
