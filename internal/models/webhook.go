package models

type TrelloCardData struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Due       string `json:"due"`
	ShortLink string `json:"shortLink"`
	Closed    bool   `json:"closed"`
}

type TrelloBoardData struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type TrelloWebhookPayload struct {
	Action struct {
		Data struct {
			Card  TrelloCardData  `json:"card"`
			Board TrelloBoardData `json:"board"`
		} `json:"data"`
		Type string `json:"type"` // e.g., "updateCard"
	} `json:"action"`
}
