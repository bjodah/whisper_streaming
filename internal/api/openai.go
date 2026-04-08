package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"time"
)

type Word struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

type Segment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

type TranscriptionResponse struct {
	Text     string    `json:"text"`
	Words    []Word    `json:"words"`
	Segments []Segment `json:"segments"`
}

type Client struct {
	BaseURL     string
	APIKey      string
	Language    string
	HTTPTimeout time.Duration
	HTTP        *http.Client
}

func NewClient(baseURL, apiKey, language string, httpTimeout time.Duration) *Client {
	if httpTimeout <= 0 {
		httpTimeout = 30 * time.Second
	}

	return &Client{
		BaseURL:     baseURL,
		APIKey:      apiKey,
		Language:    language,
		HTTPTimeout: httpTimeout,
		HTTP:        &http.Client{Timeout: httpTimeout},
	}
}

func (c *Client) Transcribe(ctx context.Context, wavData []byte, prompt string) (*TranscriptionResponse, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Create file part
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="file"; filename="audio.wav"`)
	h.Set("Content-Type", "audio/wav")
	part, err := writer.CreatePart(h)
	if err != nil {
		return nil, err
	}
	_, err = part.Write(wavData)
	if err != nil {
		return nil, err
	}

	_ = writer.WriteField("model", "whisper-1")
	_ = writer.WriteField("response_format", "verbose_json")
	_ = writer.WriteField("timestamp_granularities[]", "word")
	_ = writer.WriteField("timestamp_granularities[]", "segment")

	if c.Language != "" {
		_ = writer.WriteField("language", c.Language)
	}
	if prompt != "" {
		_ = writer.WriteField("prompt", prompt)
	}

	err = writer.Close()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/audio/transcriptions", body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error: status=%d body=%s", resp.StatusCode, string(respBody))
	}

	var transResp TranscriptionResponse
	if err := json.NewDecoder(resp.Body).Decode(&transResp); err != nil {
		return nil, err
	}

	return &transResp, nil
}

func IsTimeout(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	var netErr interface{ Timeout() bool }
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	return false
}
