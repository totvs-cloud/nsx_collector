package alerting

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
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
		http:    &http.Client{Timeout: 30 * time.Second},
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

func (sc *SlackClient) UploadImage(threadTS, filename, title string, img []byte) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("channels", sc.channel)
	w.WriteField("title", title)
	w.WriteField("filename", filename)
	if threadTS != "" {
		w.WriteField("thread_ts", threadTS)
	}
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		return err
	}
	if _, err := io.Copy(fw, bytes.NewReader(img)); err != nil {
		return err
	}
	w.Close()

	req, err := http.NewRequest("POST", "https://slack.com/api/files.upload", &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+sc.token)
	req.Header.Set("Content-Type", w.FormDataContentType())

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
		return fmt.Errorf("slack upload: %s", out.Error)
	}
	return nil
}
