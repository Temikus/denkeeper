package voice

import "context"

// STTProvider transcribes audio to text.
type STTProvider interface {
	Transcribe(ctx context.Context, audio []byte, format string) (string, error)
}

// TTSProvider synthesizes text to audio.
type TTSProvider interface {
	Synthesize(ctx context.Context, text string, voice string) ([]byte, error)
}
