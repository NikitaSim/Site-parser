package ml

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEmbed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var request embeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if len(request.Texts) != 1 || request.Texts[0] != "test product" {
			t.Errorf("unexpected request: %#v", request)
		}
		vector := make([]float32, embeddingDimensions)
		vector[0] = 0.5
		_ = json.NewEncoder(w).Encode(embeddingResponse{Embeddings: [][]float32{vector}})
	}))
	defer server.Close()

	vectors, err := NewClient(server.URL).Embed(context.Background(), []string{"test product"})
	if err != nil {
		t.Fatalf("Embed returned error: %v", err)
	}
	if len(vectors) != 1 || len(vectors[0]) != embeddingDimensions {
		t.Fatalf("unexpected vectors shape: %d x %d", len(vectors), len(vectors[0]))
	}
	if vectors[0][0] != 0.5 {
		t.Fatalf("unexpected vector value: %v", vectors[0][0])
	}
}

func TestRerank(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/rerank" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var request rerankRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if request.Query != "black shower set" || len(request.Passages) != 2 {
			t.Errorf("unexpected request: %#v", request)
		}
		_ = json.NewEncoder(w).Encode(rerankResponse{Scores: []float64{0.8, 0.2}})
	}))
	defer server.Close()

	scores, err := NewClient(server.URL).Rerank(context.Background(), "black shower set", []string{"product one", "product two"})
	if err != nil {
		t.Fatalf("Rerank returned error: %v", err)
	}
	if len(scores) != 2 || scores[0] != 0.8 || scores[1] != 0.2 {
		t.Fatalf("unexpected scores: %#v", scores)
	}
}

func TestEmbedRejectsWrongDimensions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(embeddingResponse{Embeddings: [][]float32{{0.1, 0.2}}})
	}))
	defer server.Close()

	_, err := NewClient(server.URL).Embed(context.Background(), []string{"test"})
	if err == nil {
		t.Fatal("expected dimension validation error")
	}
}
