package service

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"parser/internal/models"
	"parser/internal/parser"
	"parser/internal/repository"
)

const (
	importBatchSize    = 1000
	writerCount        = 16
	embeddingBatchSize = 64
)

type EmbeddingClient interface {
	Embed(context.Context, []string) ([][]float32, error)
}

type embeddingRepository interface {
	SaveEmbeddings([]parser.Product, [][]float32) error
}

type parserService struct {
	parser   parser.Parser
	repo     repository.Repository
	embedder EmbeddingClient
	mu       sync.Mutex
	embedMu  sync.Mutex
}

func NewParserService(siteParser parser.Parser, repo repository.Repository) ParseService {
	return NewParserServiceWithEmbeddings(siteParser, repo, nil)
}

func NewParserServiceWithEmbeddings(siteParser parser.Parser, repo repository.Repository, embedder EmbeddingClient) ParseService {
	return &parserService{parser: siteParser, repo: repo, embedder: embedder}
}

// Parse reads products from the parser and hands full batches to several
// database writers, so parsing never waits for a single slow INSERT.
// The old catalogue is not truncated: a failed import must not destroy good data.
func (s *parserService) Parse() (*models.ParseResults, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	results := &models.ParseResults{}
	products := make(chan parser.Product, 512)
	parseErr := make(chan error, 1)

	go func() {
		parseErr <- s.parser.Parse(products, results)
		close(products)
	}()

	batches := make(chan []parser.Product, writerCount*2)
	var (
		wg                sync.WaitGroup
		mu                sync.Mutex
		inserted          int
		embeddingFailures int
		saveErr           error
	)

	wg.Add(writerCount)
	for i := 0; i < writerCount; i++ {
		go func() {
			defer wg.Done()
			for batch := range batches {
				mu.Lock()
				failed := saveErr != nil
				mu.Unlock()
				if failed {
					continue
				}

				start := time.Now()
				err := s.repo.SaveBuffer(batch)

				mu.Lock()
				if err != nil {
					if saveErr == nil {
						saveErr = err
					}
				} else {
					inserted += len(batch)
				}
				mu.Unlock()

				if err == nil {
					log.Printf("saved %d rows in %s", len(batch), time.Since(start))
					if s.embedder != nil {
						if err := s.indexBatch(batch); err != nil {
							mu.Lock()
							embeddingFailures++
							mu.Unlock()
							log.Printf("embedding batch failed: %v", err)
						}
					}
				}
			}
		}()
	}

	batch := make([]parser.Product, 0, importBatchSize)
	for product := range products {
		batch = append(batch, product)
		if len(batch) == importBatchSize {
			batches <- batch
			batch = make([]parser.Product, 0, importBatchSize)
		}
	}
	if len(batch) > 0 {
		batches <- batch
	}
	close(batches)
	wg.Wait()
	results.Inserted = inserted
	results.Errors += embeddingFailures

	if err := <-parseErr; err != nil {
		if results.Errors == 0 {
			results.Errors++
		}
		if saveErr != nil {
			return results, fmt.Errorf("save catalogue batch: %w (parser also failed: %v)", saveErr, err)
		}
		return results, fmt.Errorf("parse catalogue: %w", err)
	}
	if saveErr != nil {
		results.Errors++
		return results, fmt.Errorf("save catalogue batch: %w", saveErr)
	}
	return results, nil
}

func (s *parserService) indexBatch(products []parser.Product) error {
	repo, ok := s.repo.(embeddingRepository)
	if !ok {
		return fmt.Errorf("repository does not support embedding persistence")
	}

	for start := 0; start < len(products); start += embeddingBatchSize {
		end := start + embeddingBatchSize
		if end > len(products) {
			end = len(products)
		}
		batch := products[start:end]
		texts := make([]string, 0, len(batch))
		for _, product := range batch {
			text := strings.Join([]string{
				product.Title,
				product.Category,
				product.Info,
				product.Description,
			}, "\n")
			runes := []rune(strings.TrimSpace(text))
			if len(runes) > 20000 {
				text = string(runes[:20000])
			} else {
				text = string(runes)
			}
			texts = append(texts, text)
		}

		// The model is shared by all database writer workers. Serialize inference
		// requests to avoid loading several large batches into memory at once.
		s.embedMu.Lock()
		vectors, err := s.embedder.Embed(context.Background(), texts)
		s.embedMu.Unlock()
		if err != nil {
			return fmt.Errorf("generate embeddings for batch [%d:%d]: %w", start, end, err)
		}
		if err := repo.SaveEmbeddings(batch, vectors); err != nil {
			return fmt.Errorf("persist embeddings for batch [%d:%d]: %w", start, end, err)
		}
	}
	return nil
}
