package service

import (
	"fmt"
	"parser/internal/models"
	"parser/internal/parser"
	"parser/internal/repository"
	"sync"
)

const workerCount = 3

type Service interface {
	Parse() (*models.ParseResults, error)
}

type parserService struct {
	parser parser.Parser
	repo   repository.Repository
}

func NewService(parser parser.Parser, repo repository.Repository) *parserService {
	return &parserService{
		parser: parser,
		repo:   repo,
	}
}

func (s *parserService) batchWorker(in <-chan parser.Product, results *models.ParseResults, mx *sync.Mutex) error {

	buffer := make([]parser.Product, 0, 100)

	for product := range in {
		buffer = append(buffer, product)
		if len(buffer) >= 100 {
			if err := s.repo.SaveBuffer(buffer); err != nil {
				results.Errors++
				return err
			}
			mx.Lock()
			results.Inserted += len(buffer)
			mx.Unlock()
			buffer = buffer[:0]
		}
	}

	if len(buffer) > 0 {
		if err := s.repo.SaveBuffer(buffer); err != nil {
			results.Errors++
			return err
		}
		mx.Lock()
		results.Inserted += len(buffer)
		mx.Unlock()
	}
	return nil
}

func (s *parserService) Parse() (*models.ParseResults, error) {

	ch := make(chan parser.Product, 32)
	errCh := make(chan error, workerCount+1)
	var mx sync.Mutex

	results := &models.ParseResults{}

	go func() {
		errCh <- s.parser.Parse(ch, results)
		close(ch)
	}()

	var wg sync.WaitGroup
	wg.Add(workerCount)
	if err := s.repo.ClearData(); err != nil {
		return nil, fmt.Errorf("Clear DB error: %s", err)
	}
	for i := 0; i < workerCount; i++ {
		go func(ch <-chan parser.Product) {
			defer wg.Done()
			if err := s.batchWorker(ch, results, &mx); err != nil {
				errCh <- err
			}
		}(ch)
	}

	wg.Wait()
	close(errCh)

	if err := <-errCh; err != nil {
		return nil, err
	}

	return results, nil
}
