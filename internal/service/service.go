package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"parser/internal/ai"
	"parser/internal/models"
	"parser/internal/parser"
	"parser/internal/repository"
	"sync"
)

const workerCount = 3

var ErrNeedClarification = errors.New("need clarification")

type AICompareRequest struct {
	Query    string           `json:"user_query"`
	Products []parser.Product `json:"products"`
	Level    int              `json:"level"`
}

type AICompareResponse struct {
	Level   int              `json:"Level"`
	Results []parser.Product `json:"results"`
}

type Service interface {
	Parse() (*models.ParseResults, error)
	Chat(string) ([]parser.Product, error)
}

type parserService struct {
	parser parser.Parser
	repo   repository.Repository
	aiChat ai.ClientService
}

func NewService(parser parser.Parser, repo repository.Repository, ai ai.ClientService) *parserService {
	return &parserService{
		parser: parser,
		repo:   repo,
		aiChat: ai,
	}
}

func (s *parserService) Chat(message string) ([]parser.Product, error) {

	aiResp, err := s.aiChat.Chat(message, ai.SystemPrompt)
	if err != nil {
		return nil, fmt.Errorf("AI response error: %s", err)
	}

	//fmt.Println("Ответ ИИ", aiResp)
	var filter models.Filters
	if err := json.Unmarshal([]byte(aiResp), &filter); err != nil {
		return nil, fmt.Errorf("JSON unmarshal error: %s", err)
	}

	if filter.NeedClarification {
		return nil, ErrNeedClarification
	}
	filter.Message = message
	fmt.Println(filter)

	products, err := s.repo.GetProducts(filter)

	if len(products) == 0 {

		filter.Categories = nil

		products, err = s.repo.GetProducts(filter)
	}

	if len(products) == 0 {

		filter.PriceMin = nil
		filter.PriceMax = nil

		products, err = s.repo.GetProducts(filter)
	}

	if len(products) == 0 {

		filter.Keywords = nil

		products, err = s.repo.GetProducts(filter)
	}
	if err != nil {
		return nil, fmt.Errorf("SQL error: %s", err)
	}

	filterProduct, err := s.DescriptionFilter(products, message)
	if err != nil {
		return nil, fmt.Errorf("Description filter error: %s", err)
	}
	fmt.Println("Продукты под фильтры:")
	for _, pr := range filterProduct {
		fmt.Println(pr)
	}

	return filterProduct, nil
}

func (s *parserService) DescriptionFilter(products []parser.Product, query string) ([]parser.Product, error) {
	ch := make(chan *AICompareRequest, 5)
	result := make(chan []parser.Product, 20)

	var wg sync.WaitGroup
	level := 1

	for len(products) > 15 {
		wg.Add(1)

		ch <- &AICompareRequest{
			Products: products[:15],
			Level:    level,
			Query:    query,
		}

		products = products[15:]
	}

	if len(products) > 0 {
		wg.Add(1)

		ch <- &AICompareRequest{
			Products: products,
			Level:    level,
			Query:    query,
		}
	}

	// ---------- workers ----------
	for i := 0; i < 4; i++ {
		go func() {
			for productsBatch := range ch {

				body, err := json.Marshal(productsBatch)
				if err != nil {
					wg.Done()
					continue
				}

				resp, err := s.aiChat.Chat(string(body), ai.RerankPrompt)
				if err != nil {
					wg.Done()
					continue
				}
				fmt.Println("test_1: ", resp)

				resp = NormalizeJSON(resp)

				var products []parser.Product

				if err := json.Unmarshal([]byte(resp), &products); err != nil {
					fmt.Printf("unmarshal error: %v\nresponse:\n%s\n", err, resp)
					wg.Done()
					continue
				}
				fmt.Println("Tets_2: ", products)
				result <- products

				// var aiResp AICompareResponse

				// if err := json.Unmarshal([]byte(resp), &aiResp); err != nil {
				// 	fmt.Println(err)
				// 	wg.Done()
				// 	continue
				// }

				// fmt.Println("Tets_2: ", products)

				// result <- aiResp.Results
			}
		}()
	}

	// ---------- закрытие ----------
	go func() {
		wg.Wait()

		close(ch)
		close(result)
	}()

	final := make([]parser.Product, 0)

	for prod := range result {

		if len(prod) > 5 {

			mid := len(prod) / 2

			wg.Add(2)
			wg.Done()

			ch <- &AICompareRequest{
				Products: prod[:mid],
				Query:    query,
				Level:    level + 1,
			}

			ch <- &AICompareRequest{
				Products: prod[mid:],
				Query:    query,
				Level:    level + 1,
			}

			continue
		}

		wg.Done()

		final = append(final, prod...)
	}

	return final, nil
}

func NormalizeJSON(resp string) string {
	resp = strings.TrimSpace(resp)

	// Убираем markdown-блоки
	resp = strings.ReplaceAll(resp, "```json", "")
	resp = strings.ReplaceAll(resp, "```JSON", "")
	resp = strings.ReplaceAll(resp, "```", "")
	resp = strings.TrimSpace(resp)

	// Ищем начало JSON
	startArr := strings.Index(resp, "[")
	startObj := strings.Index(resp, "{")

	start := -1
	switch {
	case startArr == -1:
		start = startObj
	case startObj == -1:
		start = startArr
	case startArr < startObj:
		start = startArr
	default:
		start = startObj
	}

	if start == -1 {
		return resp
	}

	// Ищем конец JSON
	endArr := strings.LastIndex(resp, "]")
	endObj := strings.LastIndex(resp, "}")

	end := -1
	switch {
	case endArr == -1:
		end = endObj
	case endObj == -1:
		end = endArr
	case endArr > endObj:
		end = endArr
	default:
		end = endObj
	}

	if end == -1 || end < start {
		return resp[start:]
	}

	return strings.TrimSpace(resp[start : end+1])
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
