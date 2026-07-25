package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"parser/internal/models"
)

type ClientService interface {
	Chat(string, string) (string, error)
}

type Client struct {
	url string
}

func NewClient(url string) *Client {
	return &Client{
		url: url,
	}
}

// Скелет запроса в LLM
func (c *Client) Chat(prompt, systemPrompt string) (string, error) {

	reqBody := models.ChatRequest{
		Model:  "qwen3:8b",
		Stream: false,

		Messages: []models.Message{
			{
				Role:    "system",
				Content: systemPrompt,
			},
			{
				Role:    "user",
				Content: prompt,
			},
		},

		Options: &models.ChatOptions{
			Temperature: 0,
			Think:       false,
		},
	}

	req, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("JSON marshal error: %s", err)
	}

	resp, err := http.Post(
		c.url+"/api/chat",
		"application/json",
		bytes.NewReader(req),
	)
	if err != nil {
		return "", fmt.Errorf("AI request error: %s", err)
	}
	defer resp.Body.Close()

	var result models.ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("Decode results error %s", err)
	}

	return result.Message.Content, nil
}
