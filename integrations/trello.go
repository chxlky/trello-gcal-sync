package integrations

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
)

type TrelloClient struct {
	Client *http.Client
	APIKey string
	APIToken string
	CallbackURL string
}

func NewTrelloClient(key, token, callbackURL string) *TrelloClient {
	return &TrelloClient{
		Client: &http.Client{},
		APIKey: key,
		APIToken: token,
		CallbackURL: callbackURL,
	}
}

func (tc *TrelloClient) RegisterWebhook(boardId string) (string, error) {
	apiURL := "https://api.trello.com/1/webhooks/"

	formData := url.Values{}
	formData.Set("key", tc.APIKey)
	formData.Set("token", tc.APIToken)
	formData.Set("callbackURL", tc.CallbackURL)
	formData.Set("idModel", boardId)
	formData.Set("description", "Webhook for Trello-GCal Sync")

	req, err := http.NewRequest("POST", apiURL, bytes.NewBufferString(formData.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to create post request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := tc.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send post request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("trello API returned non-200 status: %s, body: %s", resp.Status, string(bodyBytes))
	}

	var webhook struct {
		ID string `json:"id"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&webhook); err != nil {
		return "", fmt.Errorf("failed to decode Trello response: %v", err)
	}

	log.Printf("Successfully registered webhook with ID: %s for board ID: %s\n", webhook.ID, boardId)

	return webhook.ID, nil
}

func (tc *TrelloClient) DeleteWebhook(webhookID string) error {
	apiURL := fmt.Sprintf("https://api.trello.com/1/webhooks/%s", webhookID)

	formData := url.Values{}
	formData.Set("key", tc.APIKey)
	formData.Set("token", tc.APIToken)

	req, err := http.NewRequest("DELETE", apiURL, bytes.NewBufferString(formData.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create delete request: %v", err)
	}
	
	resp, err := tc.Client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send delete request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("trello API returned non-200 status: %s, body: %s", resp.Status, string(bodyBytes))
	}

	log.Printf("Successfully deleted webhook with ID: %s\n", webhookID)
	
	return nil
}