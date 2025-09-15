package models

type TrelloWebhookPayload struct {
    Action struct {
        Data struct {
            Card struct {
                ID          string `json:"id"`
                Name        string `json:"name"`
                Due         string `json:"due"`
                ShortLink   string `json:"shortLink"`
            } `json:"card"`
        } `json:"data"`
        Type string `json:"type"` // e.g., "updateCard"
    } `json:"action"`
}