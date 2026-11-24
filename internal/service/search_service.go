package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"parser/internal/ai"
	"parser/internal/models"
	"parser/internal/parser"
	"parser/internal/repository"
)

const (
	rerankBatchSize = 10
	rerankWorkers   = 2
	semanticLimit   = 100
	maxSearchResults = 10
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

type SemanticClient interface {
	Embed(context.Context, []string) ([][]float32, error)
	Rerank(context.Context, string, []string) ([]float64, error)
}

type semanticRepository interface {
	SearchSimilar([]float32, models.Filters, int) ([]parser.Product, error)
}

type searchService struct {
	repo     repository.Repository
	aiChat   ai.ClientService
	mlClient SemanticClient
}

func NewSearchService(repo repository.Repository, aiChat ai.ClientService) SearchService {
	return &searchService{repo: repo, aiChat: aiChat}
}

func NewSearchServiceWithML(repo repository.Repository, aiChat ai.ClientService, mlClient SemanticClient) SearchService {
	return &searchService{repo: repo, aiChat: aiChat, mlClient: mlClient}
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

	if s.mlClient != nil {
		if products, ok := s.semanticSearch(message, filter); ok {
			return limitProducts(products, maxSearchResults), nil
		}
	}

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

	return limitProducts(s.rerank(products, message), maxSearchResults), nil
}

func (s *searchService) semanticSearch(query string, filter models.Filters) ([]parser.Product, bool) {
	vectorRepo, ok := s.repo.(semanticRepository)
	if !ok {
		return nil, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	queryVectors, err := s.mlClient.Embed(ctx, []string{query})
	if err != nil || len(queryVectors) != 1 {
		log.Printf("semantic query embedding failed: %v", err)
		return nil, false
	}

	semanticProducts, err := vectorRepo.SearchSimilar(queryVectors[0], filter, semanticLimit)
	if err != nil {
		log.Printf("semantic database search failed: %v", err)
		return nil, false
	}

	var lexicalProducts []parser.Product
	if len(filter.Keywords) > 0 {
		var lexicalErr error
		lexicalProducts, lexicalErr = s.repo.GetProducts(filter)
		if lexicalErr != nil {
			log.Printf("lexical candidate search failed: %v", lexicalErr)
		}
	}

	candidates := make([]parser.Product, 0, semanticLimit)
	seen := make(map[string]struct{}, semanticLimit)
	add := func(products []parser.Product) {
		for _, product := range products {
			if product.Link == "" {
				continue
			}
			if _, exists := seen[product.Link]; exists {
				continue
			}
			seen[product.Link] = struct{}{}
			candidates = append(candidates, product)
			if len(candidates) == semanticLimit {
				return
			}
		}
	}

	// Reserve some candidate slots for lexical matches so exact article numbers
	// and product names are not crowded out by the semantic nearest neighbors.
	semanticHead := len(semanticProducts)
	if semanticHead > semanticLimit*4/5 {
		semanticHead = semanticLimit * 4 / 5
	}
	add(semanticProducts[:semanticHead])
	add(lexicalProducts)
	if len(candidates) < semanticLimit {
		add(semanticProducts[semanticHead:])
	}
	if len(candidates) == 0 {
		return nil, false
	}

	passages := make([]string, len(candidates))
	for i, product := range candidates {
		passages[i] = strings.Join([]string{
			product.Title,
			product.Category,
			product.Info,
			product.Description,
		}, "\n")
	}

	scores, err := s.mlClient.Rerank(ctx, query, passages)
	if err != nil {
		log.Printf("ML reranking failed: %v", err)
		return nil, false
	}

	order := make([]int, len(candidates))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		return scores[order[i]] > scores[order[j]]
	})

	result := make([]parser.Product, 0, len(candidates))
	for _, index := range order {
		result = append(result, candidates[index])
	}
	return result, true
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
	return result
}


// limitProducts enforces the public search response limit after ranking.
func limitProducts(products []parser.Product, limit int) []parser.Product {
	if limit <= 0 {
		return []parser.Product{}
	}
	if len(products) <= limit {
		return products
	}
	return products[:limit]
}
