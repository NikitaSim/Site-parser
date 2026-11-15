package models

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestChatRequestMarshalsStructuredOutputAndZeroValues(t *testing.T) {
	request := ChatRequest{
		Model: "qwen3:8b",
		Stream: false,
		Format: map[string]any{"type": "object"},
		Options: &ChatOptions{Temperature: 0, NumPredict: 2048},
		Think: false,
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(body)
	for _, expected := range []string{"\"format\":", "\"think\":false", "\"temperature\":0", "\"stream\":false"} {
		if !strings.Contains(encoded, expected) {
			t.Fatalf("expected %s in Ollama request, got %s", expected, encoded)
		}
	}
}
