package parser

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"

	"parser/internal/models"
)

const parserWorkerCount = 64

type Parser interface {
	Parse(chan<- Product, *models.ParseResults) error
}

type siteParser struct{ client *http.Client }

type Product struct {
	Category    string `json:"category"`
	Title       string `json:"title"`
	Price       *int   `json:"price"`
	Info        string `json:"info"`
	Description string `json:"description"`
	Link        string `json:"link"`
}

func NewParser() *siteParser {
	return &siteParser{client: &http.Client{Timeout: 60 * time.Second}}
}

func (p *siteParser) fetch(ctx context.Context, address string) ([]byte, error) {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.Host != "grohe-russia.shop" {
		return nil, fmt.Errorf("invalid GROHE URL: %s", address)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "SiteParser-Grohe/1.0")

	var response *http.Response
	for attempt := 0; attempt < 3; attempt++ {
		response, err = p.client.Do(req)
		if err != nil {
			return nil, err
		}
		if attempt == 2 || (response.StatusCode != http.StatusBadGateway &&
			response.StatusCode != http.StatusServiceUnavailable &&
			response.StatusCode != http.StatusGatewayTimeout) {
			break
		}
		_ = response.Body.Close()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * time.Second):
		}
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone {
		return nil, errPageGone
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", response.StatusCode, address)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 20<<20))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", address, err)
	}
	return body, nil
}

type product struct {
	Type        string `json:"@type"`
	Name        string `json:"name"`
	SKU         string `json:"sku"`
	Description string `json:"description"`
	Offers      struct {
		Price    json.Number `json:"price"`
		Currency string      `json:"priceCurrency"`
	} `json:"offers"`
}

var errPageGone = errors.New("страница товара удалена")

func extract(body []byte) (product, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return product{}, fmt.Errorf("parse product HTML: %w", err)
	}

	var found product
	doc.Find(`script[type="application/ld+json"]`).Each(func(_ int, script *goquery.Selection) {
		if found.Name != "" {
			return
		}
		raw := strings.TrimSpace(script.Text())
		var candidate product
		if json.Unmarshal([]byte(raw), &candidate) == nil &&
			candidate.Type == "Product" && candidate.Name != "" {
			found = candidate
		}
	})
	if found.Name != "" {
		return found, nil
	}

	if strings.EqualFold(strings.TrimSpace(doc.Find("title").First().Text()), "404 Not Found") {
		return product{}, errPageGone
	}
	return product{}, fmt.Errorf("Product JSON-LD absent")
}

func (p *siteParser) Parse(ch chan<- Product, results *models.ParseResults) error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()

	body, err := p.fetch(ctx, "https://grohe-russia.shop/sitemap.xml")
	if err != nil {
		return fmt.Errorf("fetch sitemap: %w", err)
	}

	var sitemap struct {
		URLs []struct {
			Loc string `xml:"loc"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal(body, &sitemap); err != nil {
		return fmt.Errorf("decode sitemap: %w", err)
	}

	addresses := make([]string, 0, len(sitemap.URLs))
	seen := make(map[string]struct{}, len(sitemap.URLs))
	for _, entry := range sitemap.URLs {
		u, err := url.Parse(entry.Loc)
		if err != nil || u.Scheme != "https" || u.Host != "grohe-russia.shop" ||
			u.RawQuery != "" || !strings.HasSuffix(u.Path, ".html") {
			continue
		}
		if _, exists := seen[entry.Loc]; exists {
			continue
		}
		seen[entry.Loc] = struct{}{}
		addresses = append(addresses, entry.Loc)
	}
	if len(addresses) == 0 {
		return fmt.Errorf("no product URLs found in sitemap")
	}

	jobs := make(chan string, parserWorkerCount*2)
	var wg sync.WaitGroup
	var mu sync.Mutex
	parsed, failed := 0, 0
	categories := make(map[string]struct{})
	// One shared ticker limits the total request rate across all workers.

	wg.Add(parserWorkerCount)
	for i := 0; i < parserWorkerCount; i++ {
		go func() {
			defer wg.Done()
			for address := range jobs {
				if ctx.Err() != nil {
					return
				}

				page, fetchErr := p.fetch(ctx, address)
				var item Product
				if fetchErr == nil {
					item, fetchErr = parseProduct(page, address)
				}
				if fetchErr != nil {
					if errors.Is(fetchErr, errPageGone) {
						log.Printf("GROHE page skipped (removed): %s", address)
						continue
					}
					log.Printf("GROHE parse failed %s: %v", address, fetchErr)
					mu.Lock()
					failed++
					results.Errors++
					mu.Unlock()
					continue
				}

				select {
				case ch <- item:
					mu.Lock()
					parsed++
					categories[item.Category] = struct{}{}
					mu.Unlock()
				case <-ctx.Done():
					return
				}
			}
		}()
	}

sendJobs:
	for _, address := range addresses {
		select {
		case jobs <- address:
		case <-ctx.Done():
			break sendJobs
		}
	}
	close(jobs)
	wg.Wait()

	results.Parsed += parsed
	results.Categories += len(categories)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("GROHE import cancelled: %w", err)
	}
	if parsed == 0 {
		return fmt.Errorf("GROHE: no products parsed from %d URLs", len(addresses))
	}
	if failed > 0 {
		return fmt.Errorf("GROHE: %d pages failed out of %d URLs", failed, len(addresses))
	}
	return nil
}

func parseProduct(body []byte, address string) (Product, error) {
	data, err := extract(body)
	if err != nil {
		return Product{}, err
	}

	item := Product{
		Title:       strings.TrimSpace(data.Name),
		Description: strings.TrimSpace(data.Description),
		Info:        strings.TrimSpace("Артикул: " + data.SKU),
		Link:        address,
	}

	if data.Offers.Price != "" {
		price, ok := new(big.Rat).SetString(string(data.Offers.Price))
		if !ok || !price.Num().IsInt64() ||
			price.Sign() < 0 ||
			price.Num().Quo(price.Num(), price.Denom()).Int64() > 2147483647 ||
			data.Offers.Currency != "RUB" {
			return Product{}, fmt.Errorf(
				"unsupported product price %q %q",
				data.Offers.Price,
				data.Offers.Currency,
			)
		}

		// Отбрасываем копейки без округления.
		priceInt := new(big.Int).Quo(price.Num(), price.Denom())
		value := int(priceInt.Int64())
		item.Price = &value
	}

	u, err := url.Parse(address)
	if err != nil {
		return Product{}, err
	}

	slug := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(slug) > 0 {
		item.Category = slug[0]
	}

	categoryNames := map[string]string{
		"vannaya":    "Ванная",
		"dush":       "Душ",
		"kuhnja":     "Кухня",
		"kitchen":    "Кухня",
		"wc":         "Туалет",
		"aksessuary": "Аксессуары",
		"nabory":     "Наборы",
	}

	if name, ok := categoryNames[item.Category]; ok {
		item.Category = name
	}

	return item, nil
}
