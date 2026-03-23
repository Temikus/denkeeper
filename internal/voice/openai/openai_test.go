package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTranscribe_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/audio/transcriptions") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}

		ct := r.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "multipart/form-data") {
			t.Errorf("expected multipart/form-data, got %s", ct)
		}

		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Fatalf("parsing multipart: %v", err)
		}
		if model := r.FormValue("model"); model != "whisper-1" {
			t.Errorf("expected model whisper-1, got %s", model)
		}
		if rf := r.FormValue("response_format"); rf != "text" {
			t.Errorf("expected response_format text, got %s", rf)
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("getting form file: %v", err)
		}
		defer func() { _ = file.Close() }()
		if header.Filename != "audio.ogg" {
			t.Errorf("expected filename audio.ogg, got %s", header.Filename)
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Hello, world!"))
	}))
	defer server.Close()

	client := NewWithHTTPClient("test-key", server.URL, server.Client())
	text, err := client.Transcribe(context.Background(), []byte("fake-audio"), "ogg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Hello, world!" {
		t.Errorf("expected 'Hello, world!', got %q", text)
	}
}

func TestTranscribe_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad request"}`))
	}))
	defer server.Close()

	client := NewWithHTTPClient("test-key", server.URL, server.Client())
	_, err := client.Transcribe(context.Background(), []byte("fake-audio"), "ogg")
	if err == nil {
		t.Fatal("expected error for non-200 response")
	}
	if !strings.Contains(err.Error(), "status 400") {
		t.Errorf("expected status 400 in error, got: %v", err)
	}
}

func TestSynthesize_Success(t *testing.T) {
	expectedAudio := []byte("fake-opus-audio-data")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/audio/speech") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected application/json, got %s", r.Header.Get("Content-Type"))
		}

		body, _ := io.ReadAll(r.Body)
		bodyStr := string(body)
		if !strings.Contains(bodyStr, `"model":"tts-1"`) {
			t.Errorf("expected model tts-1 in body: %s", bodyStr)
		}
		if !strings.Contains(bodyStr, `"voice":"alloy"`) {
			t.Errorf("expected voice alloy in body: %s", bodyStr)
		}
		if !strings.Contains(bodyStr, `"response_format":"opus"`) {
			t.Errorf("expected response_format opus in body: %s", bodyStr)
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(expectedAudio)
	}))
	defer server.Close()

	client := NewWithHTTPClient("test-key", server.URL, server.Client())
	audio, err := client.Synthesize(context.Background(), "Hello!", "alloy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(audio) != string(expectedAudio) {
		t.Errorf("expected %q, got %q", expectedAudio, audio)
	}
}

func TestSynthesize_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer server.Close()

	client := NewWithHTTPClient("test-key", server.URL, server.Client())
	_, err := client.Synthesize(context.Background(), "Hello!", "alloy")
	if err == nil {
		t.Fatal("expected error for non-200 response")
	}
	if !strings.Contains(err.Error(), "status 429") {
		t.Errorf("expected status 429 in error, got: %v", err)
	}
}
