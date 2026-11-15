package service

import (
 "fmt"
 "log"
 "sync"
 "time"

 "parser/internal/models"
 "parser/internal/parser"
 "parser/internal/repository"
)

const (
 importBatchSize = 1000
 writerCount     = 16
)

type parserService struct {
 parser parser.Parser
 repo   repository.Repository
 mu     sync.Mutex
}

func NewParserService(siteParser parser.Parser, repo repository.Repository) ParseService {
 return &parserService{parser: siteParser, repo: repo}
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
  wg       sync.WaitGroup
  mu       sync.Mutex
  inserted int
  saveErr  error
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
     continue // keep draining so the producer never blocks
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
    }
   }
  }()
 }

 batch := make([]parser.Product, 0, importBatchSize)
 for product := range products {
  batch = append(batch, product)
  if len(batch) == importBatchSize {
   batches <- batch
   batch = make([]parser.Product, 0, importBatchSize) // new slice: writers own the old one
  }
 }
 if len(batch) > 0 {
  batches <- batch
 }
 close(batches)
 wg.Wait()
 results.Inserted = inserted

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