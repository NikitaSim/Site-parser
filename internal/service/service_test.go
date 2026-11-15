package service

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"parser/internal/ai"
	"parser/internal/models"
	"parser/internal/parser"
)

type fakeRepository struct {
	mu         sync.Mutex
	batchSizes []int
	products   []parser.Product
	getErr     error
}

func (r *fakeRepository) SaveProduct(p parser.Product) error {
	return r.SaveBuffer([]parser.Product{p})
}

func (r *fakeRepository) SaveBuffer(products []parser.Product) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batchSizes = append(r.batchSizes, len(products))
	r.products = append(r.products, products...)
	return nil
}

func (r *fakeRepository) GetProducts(models.Filters) ([]parser.Product, error) {
	return r.products, r.getErr
}

func (r *fakeRepository) CloseDB() {}

type fakeParser struct {
	count int
	err   error
}

func (p fakeParser) Parse(out chan<- parser.Product, results *models.ParseResults) error {
	for i := 0; i < p.count; i++ {
		out <- parser.Product{
			Category: "Душ",
			Title:    fmt.Sprintf("Product %d", i),
			Link:     fmt.Sprintf("https://grohe-russia.shop/dush/%d.html", i),
		}
	}
	results.Parsed = p.count
	results.Categories = 1
	return p.err
}

type fakeAI struct {
	response string
}

func (a fakeAI) Chat(string, string) (string, error) {
	return a.response, nil
}

func TestParserServiceUsesBoundedBatches(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewParserService(fakeParser{count: importBatchSize + 1}, repo)

	results, err := svc.Parse()
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if results.Parsed != importBatchSize+1 || results.Inserted != importBatchSize+1 {
		t.Fatalf("unexpected parse results: %+v", results)
	}
	if len(repo.batchSizes) != 2 || repo.batchSizes[0] != importBatchSize || repo.batchSizes[1] != 1 {
		t.Fatalf("unexpected batch sizes: %v", repo.batchSizes)
	}
}

func TestParserServiceKeepsExistingDataOnParserError(t *testing.T) {
	repo := &fakeRepository{}
	svc := NewParserService(fakeParser{count: 2, err: errors.New("site unavailable")}, repo)

	results, err := svc.Parse()
	if err == nil {
		t.Fatal("expected parser error")
	}
	if results.Inserted != 2 || len(repo.products) != 2 {
		t.Fatalf("successfully parsed products should still be upserted: results=%+v saved=%d", results, len(repo.products))
	}
}

func TestRerankBatchMapsOnlyKnownLinksToOriginalProducts(t *testing.T) {
	products := []parser.Product{
		{Title: "First", Link: "https://grohe-russia.shop/1.html"},
		{Title: "Second", Link: "https://grohe-russia.shop/2.html"},
	}
	aiClient := fakeAI{response: `{"links":["https://grohe-russia.shop/2.html","unknown","https://grohe-russia.shop/2.html","https://grohe-russia.shop/1.html"]}`}
	svc := &searchService{aiChat: aiClient}

	got := svc.rerankBatch(products, "душевой шланг")
	if len(got) != 2 || got[0].Title != "Second" || got[1].Title != "First" {
		t.Fatalf("unexpected reranking result: %+v", got)
	}
}

func TestRerankBatchAcceptsEmptySelection(t *testing.T) {
	svc := &searchService{aiChat: fakeAI{response: `{"links":[]}`}}
	got := svc.rerankBatch([]parser.Product{{Title: "Candidate", Link: "link"}}, "unrelated query")
	if len(got) != 0 {
		t.Fatalf("expected empty result, got %+v", got)
	}
}

func TestNormalizeJSON(t *testing.T) {
	got := NormalizeJSON("Here is the result:\n```json\n{\"links\":[]}\n```")
	if got != `{"links":[]}` {
		t.Fatalf("unexpected normalized JSON: %q", got)
	}
}

// Keep the compile-time interface assertion close to the test doubles.
var _ ai.ClientService = fakeAI{}
