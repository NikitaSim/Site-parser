package service

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	"parser/internal/ai"
	"parser/internal/models"
	"parser/internal/parser"
	"parser/internal/repository"
)

const (
	rerankBatchSize = 10
	rerankWorkers   = 2
)

type rerankRequest struct {
	Query    string           `json:"query"`
	Products []parser.Product `json:"products"`
}

type rerankResponse struct {
	Links []string `json:"links"`
}

type rerankTask struct {
	index    int
	products []parser.Product
}

type rerankResult struct {
	index    int
	products []parser.Product
}

type searchService struct {
	repo   repository.Repository
	aiChat ai.ClientService
}

func NewSearchService(repo repository.Repository, aiChat ai.ClientService) SearchService {
	return &searchService{repo: repo, aiChat: aiChat}
}

func (s *searchService) Chat(message string) ([]parser.Product, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil, fmt.Errorf("empty user query")
	}

	response, err := s.aiChat.Chat(message, ai.SystemPrompt, ai.FilterSchema)
	if err != nil {
		return nil, fmt.Errorf("extract search filters: %w", err)
	}

	var filter models.Filters
	if err := json.Unmarshal([]byte(NormalizeJSON(response)), &filter); err != nil {
		return nil, fmt.Errorf("decode search filters: %w; response: %q", err, response)
	}
	if filter.NeedClarification {
		return nil, ErrNeedClarification
	}
	if filter.PriceMin != nil && *filter.PriceMin < 0 {
		return nil, fmt.Errorf("invalid minimum price: value cannot be negative")
	}
	if filter.PriceMax != nil && *filter.PriceMax < 0 {
		return nil, fmt.Errorf("invalid maximum price: value cannot be negative")
	}
	if filter.PriceMin != nil && filter.PriceMax != nil && *filter.PriceMin > *filter.PriceMax {
		return nil, fmt.Errorf("invalid price range: minimum exceeds maximum")
	}
	log.Println(filter)
	// Keep explicit category and price constraints. If the strict text search
	// returns nothing, progressively relax only keyword groups.
	products, err := s.repo.GetProducts(filter)
	if err != nil {
		return nil, fmt.Errorf("search products: %w", err)
	}

	for groups := len(filter.Keywords) - 1; len(products) == 0 && groups >= 0; groups-- {
		relaxed := filter
		relaxed.Keywords = filter.Keywords[:groups]
		products, err = s.repo.GetProducts(relaxed)
		if err != nil {
			return nil, fmt.Errorf("search products with relaxed keywords: %w", err)
		}
	}
	if len(products) == 0 {
		return []parser.Product{}, nil
	}

	return s.rerank(products, message), nil
}

func (s *searchService) rerank(products []parser.Product, query string) []parser.Product {
	tasks := make([]rerankTask, 0, (len(products)+rerankBatchSize-1)/rerankBatchSize)
	for start := 0; start < len(products); start += rerankBatchSize {
		end := start + rerankBatchSize
		if end > len(products) {
			end = len(products)
		}
		tasks = append(tasks, rerankTask{index: len(tasks), products: products[start:end]})
	}

	jobs := make(chan rerankTask, len(tasks))
	results := make(chan rerankResult, len(tasks))
	for _, task := range tasks {
		jobs <- task
	}
	close(jobs)

	workers := rerankWorkers
	if len(tasks) < workers {
		workers = len(tasks)
	}

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for task := range jobs {
				selected := s.rerankBatch(task.products, query)
				results <- rerankResult{index: task.index, products: selected}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	ordered := make([][]parser.Product, len(tasks))
	for result := range results {
		ordered[result.index] = result.products
	}

	final := make([]parser.Product, 0, len(products))
	for _, batch := range ordered {
		final = append(final, batch...)
	}
	return final
}

func (s *searchService) rerankBatch(products []parser.Product, query string) []parser.Product {
	request := rerankRequest{Query: query, Products: products}
	body, err := json.Marshal(request)
	if err != nil {
		return products
	}

	response, err := s.aiChat.Chat(string(body), ai.RerankPrompt, ai.RerankSchema)
	if err != nil {
		// Preserve the SQL result if the model is unavailable.
		return products
	}

	var selected rerankResponse
	if err := json.Unmarshal([]byte(NormalizeJSON(response)), &selected); err != nil {
		return products
	}

	byLink := make(map[string]parser.Product, len(products))
	for _, product := range products {
		byLink[product.Link] = product
	}
	result := make([]parser.Product, 0, len(selected.Links))
	seen := make(map[string]struct{}, len(selected.Links))
	for _, link := range selected.Links {
		product, ok := byLink[link]
		if !ok {
			continue
		}
		if _, exists := seen[link]; exists {
			continue
		}
		seen[link] = struct{}{}
		result = append(result, product)
	}
	// An explicit empty links array means that the model found no relevant
	// product. Return it as-is; only malformed/failed responses fall back.
	return result
}
