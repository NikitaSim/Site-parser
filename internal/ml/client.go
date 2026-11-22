package ml

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const embeddingDimensions = 1024

type Client struct {
	baseURL string
	http    *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		http:    &http.Client{Timeout: 10 * time.Minute},
	}
}

type embeddingRequest struct {
	Texts []string `json:"texts"`
}

type embeddingResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

type rerankRequest struct {
	Query    string   `json:"query"`
	Passages []string `json:"passages"`
}

type rerankResponse struct {
	Scores []float64 `json:"scores"`
}

func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	var result embeddingResponse
	if err := c.post(ctx, "/v1/embeddings", embeddingRequest{Texts: texts}, &result); err != nil {
		return nil, err
	}
	if len(result.Embeddings) != len(texts) {
		return nil, fmt.Errorf("ML service returned %d embeddings for %d texts", len(result.Embeddings), len(texts))
	}
	for i, vector := range result.Embeddings {
		if len(vector) != embeddingDimensions {
			return nil, fmt.Errorf("ML service returned embedding %d with %d dimensions, want %d", i, len(vector), embeddingDimensions)
		}
	}
	return result.Embeddings, nil
}

func (c *Client) Rerank(ctx context.Context, query string, passages []string) ([]float64, error) {
	if len(passages) == 0 {
		return nil, nil
	}
	var result rerankResponse
	if err := c.post(ctx, "/v1/rerank", rerankRequest{
		Query: query, Passages: passages,
	}, &result); err != nil {
		return nil, err
	}
	if len(result.Scores) != len(passages) {
		return nil, fmt.Errorf("ML service returned %d scores for %d passages", len(result.Scores), len(passages))
	}
	return result.Scores, nil
}

func (c *Client) post(ctx context.Context, path string, payload, target any) error {
	if c.baseURL == "" {
		return fmt.Errorf("ML_URL is empty")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal ML request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create ML request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("ML request %s: %w", path, err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return fmt.Errorf("read ML response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ML service returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	if err := json.Unmarshal(responseBody, target); err != nil {
		return fmt.Errorf("decode ML response: %w", err)
	}
	return nil
}
