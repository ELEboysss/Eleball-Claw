package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestAudioFormat(t *testing.T) {
	svc := NewSttService("baidu", "app-id", "api-key", "secret-key", "", 0, 0, zap.NewNop())

	tests := []struct {
		name     string
		audio    []byte
		expected string
	}{
		{
			name:     "m4a ftyp box",
			audio:    []byte{0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p', 'm', 'p', '4', '2'},
			expected: "m4a",
		},
		{
			name:     "wav riff",
			audio:    []byte{'R', 'I', 'F', 'F', 0x00, 0x00, 0x00, 0x00, 'W', 'A', 'V', 'E'},
			expected: "wav",
		},
		{
			name:     "amr header",
			audio:    []byte{'#', '!', 'A', 'M', 'R', 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
			expected: "amr",
		},
		{
			name:     "unknown fallback to pcm",
			audio:    []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C},
			expected: "pcm",
		},
		{
			name:     "too short fallback to pcm",
			audio:    []byte{0x01, 0x02},
			expected: "pcm",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			format := svc.audioFormat(tt.audio)
			assert.Equal(t, tt.expected, format)
		})
	}
}

func TestSttService_IsEnabled(t *testing.T) {
	t.Run("enabled", func(t *testing.T) {
		svc := NewSttService("baidu", "app-id", "api-key", "secret-key", "", 0, 0, zap.NewNop())
		assert.True(t, svc.IsEnabled())
	})

	t.Run("disabled without credentials", func(t *testing.T) {
		svc := NewSttService("baidu", "", "", "", "", 0, 0, zap.NewNop())
		assert.False(t, svc.IsEnabled())
	})

	t.Run("unsupported provider", func(t *testing.T) {
		svc := NewSttService("xunfei", "app-id", "api-key", "secret-key", "", 0, 0, zap.NewNop())
		assert.False(t, svc.IsEnabled())
	})
}
