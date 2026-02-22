package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
)

type Word struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

type TranscriptionResponse struct {
	Text  string `json:"text"`
	Words []Word `json:"words"`
}

type Client struct {
	BaseURL  string
	APIKey   string
	Language string
	HTTP     *http.Client
}

func NewClient(baseURL, apiKey, language string) *Client {
	return &Client{
		BaseURL:  baseURL,
		APIKey:   apiKey,
		Language: language,
		HTTP:     &http.Client{},
	}
}

func (c *Client) Transcribe(wavData []byte) (*TranscriptionResponse, error) {
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
	
	if c.Language != "" {
		_ = writer.WriteField("language", c.Language)
	}

	err = writer.Close()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", c.BaseURL+"/audio/transcriptions", body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

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
