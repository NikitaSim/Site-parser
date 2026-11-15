package service

import (
	"fmt"

	"parser/internal/models"
	"parser/internal/parser"
	"parser/internal/repository"
	"sync"
)

const importBatchSize = 1000

type parserService struct {
	parser parser.Parser
	repo   repository.Repository
	mu     sync.Mutex
}

func NewParserService(siteParser parser.Parser, repo repository.Repository) ParseService {
	return &parserService{parser: siteParser, repo: repo}
}

// Parse uses one database writer and bounded batches. The old catalogue is
// not truncated before parsing: a failed import must not destroy good data.
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

	batch := make([]parser.Product, 0, importBatchSize)
	var inserted int
	var saveErr error

	flush := func() {
		if len(batch) == 0 || saveErr != nil {
			return
		}
		if err := s.repo.SaveBuffer(batch); err != nil {
			saveErr = err
			return
		}
		inserted += len(batch)
		batch = batch[:0]
	}

	for product := range products {
		if saveErr != nil {
			// Keep draining the parser channel so producer goroutines can exit.
			continue
		}
		batch = append(batch, product)
		if len(batch) == importBatchSize {
			flush()
		}
	}
	flush()
	results.Inserted = inserted

	if err := <-parseErr; err != nil {
		results.Errors++
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
