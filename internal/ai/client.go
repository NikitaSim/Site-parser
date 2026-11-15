package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"parser/internal/models"
)

type ClientService interface {
	Chat(string, string, map[string]any) (string, error)
}

type Client struct {
	url    string
	client *http.Client
}

func NewClient(url string) *Client {
	return &Client{
		url:    strings.TrimRight(strings.TrimSpace(url), "/"),
		// Keep the existing Ollama HTTP timeout unchanged.
		client: &http.Client{Timeout: 30 * time.Minute},
	}
}

func (c *Client) Chat(prompt, systemPrompt string, format map[string]any) (string, error) {
	if c.url == "" {
		return "", fmt.Errorf("AI_URL is empty")
	}

	requestBody := models.ChatRequest{
		Model:  "qwen3:8b",
		Stream: false,
		Format: format,
		Messages: []models.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: prompt},
		},
		Options: &models.ChatOptions{
			Temperature: 0,
			NumPredict:  2048,
		},
		Think: false,
	}

	payload, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("marshal Ollama request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.url+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create Ollama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("Ollama request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", fmt.Errorf("read Ollama response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("Ollama returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result models.ChatResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("decode Ollama response: %w", err)
	}
	if strings.TrimSpace(result.Message.Content) == "" {
		return "", fmt.Errorf("Ollama returned an empty message")
	}
	return result.Message.Content, nil
}
