package alerting

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type SlackClient struct {
	token   string
	channel string
	http    *http.Client
}

type slackResp struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func NewSlackClient(token, channel string) *SlackClient {
	return &SlackClient{
		token:   token,
		channel: channel,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (sc *SlackClient) Post(text string) error {
	payload := map[string]any{
		"channel":      sc.channel,
		"text":         text,
		"unfurl_links": false,
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", "https://slack.com/api/chat.postMessage", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+sc.token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := sc.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var out slackResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	if !out.OK {
		return fmt.Errorf("slack: %s", out.Error)
	}
	return nil
}
