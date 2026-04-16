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
	TS    string `json:"ts,omitempty"`
	Error string `json:"error,omitempty"`
}

func NewSlackClient(token, channel string) *SlackClient {
	return &SlackClient{
		token:   token,
		channel: channel,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

func (sc *SlackClient) Post(text string) (string, error) {
	payload := map[string]any{
		"channel":      sc.channel,
		"text":         text,
		"unfurl_links": false,
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", "https://slack.com/api/chat.postMessage", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+sc.token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := sc.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var out slackResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if !out.OK {
		return "", fmt.Errorf("slack: %s", out.Error)
	}
	return out.TS, nil
}

// UploadImage uploads a file to Slack using files.getUploadURLExternal + files.completeUploadExternal
func (sc *SlackClient) UploadImage(threadTS, filename, title string, img []byte) error {
	// Step 1: Get upload URL
	uploadURL, fileID, err := sc.getUploadURL(filename, len(img))
	if err != nil {
		return fmt.Errorf("getUploadURL: %w", err)
	}

	// Step 2: Upload file to the URL
	if err := sc.uploadToURL(uploadURL, img); err != nil {
		return fmt.Errorf("uploadToURL: %w", err)
	}

	// Step 3: Complete upload
	if err := sc.completeUpload(fileID, title, threadTS); err != nil {
		return fmt.Errorf("completeUpload: %w", err)
	}

	return nil
}

type uploadURLResp struct {
	OK        bool   `json:"ok"`
	UploadURL string `json:"upload_url"`
	FileID    string `json:"file_id"`
	Error     string `json:"error,omitempty"`
}

func (sc *SlackClient) getUploadURL(filename string, length int) (string, string, error) {
	url := fmt.Sprintf("https://slack.com/api/files.getUploadURLExternal?filename=%s&length=%d", filename, length)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+sc.token)

	resp, err := sc.http.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	var out uploadURLResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", "", err
	}
	if !out.OK {
		return "", "", fmt.Errorf("slack: %s", out.Error)
	}
	return out.UploadURL, out.FileID, nil
}

func (sc *SlackClient) uploadToURL(uploadURL string, data []byte) error {
	req, err := http.NewRequest("POST", uploadURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := sc.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("upload status: %d", resp.StatusCode)
	}
	return nil
}

func (sc *SlackClient) completeUpload(fileID, title, threadTS string) error {
	file := map[string]string{
		"id":    fileID,
		"title": title,
	}
	payload := map[string]any{
		"files":      []any{file},
		"channel_id": sc.channel,
	}
	if threadTS != "" {
		payload["thread_ts"] = threadTS
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", "https://slack.com/api/files.completeUploadExternal", bytes.NewReader(body))
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
