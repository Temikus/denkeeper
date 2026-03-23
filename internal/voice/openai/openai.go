package openai

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
)

const defaultBaseURL = "https://api.openai.com/v1"

// Client implements both voice.STTProvider and voice.TTSProvider using
// OpenAI's Whisper (transcription) and TTS APIs.
type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

func New(apiKey string) *Client {
	return &Client{
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		http:    http.DefaultClient,
	}
}

// NewWithHTTPClient creates a client with a custom HTTP client and base URL (for testing).
func NewWithHTTPClient(apiKey, baseURL string, httpClient *http.Client) *Client {
	return &Client{
		apiKey:  apiKey,
		baseURL: baseURL,
		http:    httpClient,
	}
}

// Transcribe sends audio to OpenAI's Whisper API and returns the transcribed text.
// The format parameter should be the audio file extension (e.g. "ogg", "mp3", "wav").
func (c *Client) Transcribe(ctx context.Context, audio []byte, format string) (string, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)

	if err := w.WriteField("model", "whisper-1"); err != nil {
		return "", fmt.Errorf("writing model field: %w", err)
	}
	if err := w.WriteField("response_format", "text"); err != nil {
		return "", fmt.Errorf("writing response_format field: %w", err)
	}

	part, err := w.CreateFormFile("file", "audio."+format)
	if err != nil {
		return "", fmt.Errorf("creating form file: %w", err)
	}
	if _, err := part.Write(audio); err != nil {
		return "", fmt.Errorf("writing audio data: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("closing multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/audio/transcriptions", &body)
	if err != nil {
		return "", fmt.Errorf("creating transcription request: %w", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("sending transcription request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading transcription response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("transcription API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return string(respBody), nil
}

// Synthesize converts text to speech using OpenAI's TTS API.
// Returns OGG/Opus audio bytes suitable for Telegram voice messages.
func (c *Client) Synthesize(ctx context.Context, text string, voice string) ([]byte, error) {
	payload := fmt.Sprintf(`{"model":"tts-1","input":%q,"voice":%q,"response_format":"opus"}`, text, voice)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/audio/speech", bytes.NewBufferString(payload))
	if err != nil {
		return nil, fmt.Errorf("creating TTS request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending TTS request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading TTS response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("TTS API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}
