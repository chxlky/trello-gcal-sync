package integrations

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/avast/retry-go"
	"go.uber.org/zap"
)

type TrelloClient struct {
	Client      *http.Client
	APIKey      string
	APIToken    string
	CallbackURL string
}

func NewTrelloClient(key, token, callbackURL string) *TrelloClient {
	return &TrelloClient{
		Client:      &http.Client{},
		APIKey:      key,
		APIToken:    token,
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

	var webhookID string
	err := retry.Do(
		func() error {
			req, err := http.NewRequest("POST", apiURL, bytes.NewBufferString(formData.Encode()))
			if err != nil {
				return retry.Unrecoverable(fmt.Errorf("failed to create post request: %v", err))
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			resp, err := tc.Client.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				if resp.StatusCode >= 500 {
					bodyBytes, _ := io.ReadAll(resp.Body)
					return fmt.Errorf("trello API returned 5xx status: %s, body: %s", resp.Status, string(bodyBytes))
				}
				bodyBytes, _ := io.ReadAll(resp.Body)
				return retry.Unrecoverable(fmt.Errorf("trello API returned non-retryable status: %s, body: %s", resp.Status, string(bodyBytes)))
			}

			var webhook struct {
				ID string `json:"id"`
			}

			if err := json.NewDecoder(resp.Body).Decode(&webhook); err != nil {
				return retry.Unrecoverable(fmt.Errorf("failed to decode Trello response: %v", err))
			}

			webhookID = webhook.ID
			return nil
		},
		retry.Attempts(3),
		retry.DelayType(retry.BackOffDelay),
		retry.OnRetry(func(n uint, err error) {
			zap.L().Warn("Retrying Trello RegisterWebhook", zap.Uint("attempt", n+1), zap.Error(err))
		}),
	)

	if err != nil {
		return "", fmt.Errorf("unable to register webhook with Trello: %w", err)
	}

	zap.L().Info("Successfully registered webhook", zap.String("webhookID", webhookID), zap.String("boardID", boardId))

	return webhookID, nil
}

func (tc *TrelloClient) DeleteWebhook(webhookID string) error {
	apiURL := fmt.Sprintf("https://api.trello.com/1/webhooks/%s", webhookID)

	formData := url.Values{}
	formData.Set("key", tc.APIKey)
	formData.Set("token", tc.APIToken)

	err := retry.Do(
		func() error {
			req, err := http.NewRequest("DELETE", apiURL+"?"+formData.Encode(), nil)
			if err != nil {
				return retry.Unrecoverable(fmt.Errorf("failed to create delete request: %v", err))
			}

			resp, err := tc.Client.Do(req)
			if err != nil {
				return err // Retry on network errors
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				if resp.StatusCode >= 500 {
					bodyBytes, _ := io.ReadAll(resp.Body)
					return fmt.Errorf("trello API returned 5xx status: %s, body: %s", resp.Status, string(bodyBytes))
				}
				bodyBytes, _ := io.ReadAll(resp.Body)
				return retry.Unrecoverable(fmt.Errorf("trello API returned non-retryable status: %s, body: %s", resp.Status, string(bodyBytes)))
			}
			return nil
		},
		retry.Attempts(3),
		retry.DelayType(retry.BackOffDelay),
		retry.OnRetry(func(n uint, err error) {
			zap.L().Warn("Retrying Trello DeleteWebhook", zap.Uint("attempt", n+1), zap.Error(err))
		}),
	)

	if err != nil {
		return fmt.Errorf("unable to delete webhook with Trello: %w", err)
	}

	zap.L().Info("Successfully deleted webhook", zap.String("webhookID", webhookID))

	return nil
}
