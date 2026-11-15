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

const (
	categoryWorkerCount = 4
	productWorkerCount  = 15
)

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

type category struct {
	name string
	urls []string
}

type productJob struct {
	category string
	url      string
}

func NewParser() *siteParser {
	// Keep the existing HTTP timeout unchanged.
	return &siteParser{client: &http.Client{Timeout: 40 * time.Second}}
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

type productJSONLD struct {
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

func extract(body []byte) (productJSONLD, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return productJSONLD{}, fmt.Errorf("parse product HTML: %w", err)
	}

	var found productJSONLD
	doc.Find(`script[type="application/ld+json"]`).Each(func(_ int, script *goquery.Selection) {
		if found.Name != "" {
			return
		}
		var candidate productJSONLD
		if json.Unmarshal([]byte(strings.TrimSpace(script.Text())), &candidate) == nil &&
			candidate.Type == "Product" && candidate.Name != "" {
			found = candidate
		}
	})
	if found.Name != "" {
		return found, nil
	}
	if strings.EqualFold(strings.TrimSpace(doc.Find("title").First().Text()), "404 Not Found") {
		return productJSONLD{}, errPageGone
	}
	return productJSONLD{}, fmt.Errorf("Product JSON-LD absent")
}

func categoryFromURL(address string) (string, error) {
	u, err := url.Parse(address)
	if err != nil {
		return "", err
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", fmt.Errorf("product URL has no category: %s", address)
	}
	names := map[string]string{
		"vannaya": "Ванная", "dush": "Душ", "kuhnja": "Кухня",
		"kitchen": "Кухня", "wc": "Туалет", "aksessuary": "Аксессуары",
		"nabory": "Наборы", "kollektsii": "Коллекции",
		"bytovaya-tehnika": "Бытовая техника",
	}
	if name, ok := names[parts[0]]; ok {
		return name, nil
	}
	return parts[0], nil
}

func parseProduct(body []byte, address, categoryName string) (Product, error) {
	data, err := extract(body)
	if err != nil {
		return Product{}, err
	}
	item := Product{
		Category: categoryName,
		Title: strings.TrimSpace(data.Name),
		Description: strings.TrimSpace(data.Description),
		Info: strings.TrimSpace("Артикул: " + data.SKU),
		Link: address,
	}
	if data.Offers.Price != "" {
		price, ok := new(big.Rat).SetString(string(data.Offers.Price))
		if !ok || !price.IsInt() || !price.Num().IsInt64() ||
			price.Sign() < 0 || price.Num().Int64() > 2147483647 ||
			data.Offers.Currency != "RUB" {
			return Product{}, fmt.Errorf("unsupported product price %q %q", data.Offers.Price, data.Offers.Currency)
		}
		value := int(price.Num().Int64())
		item.Price = &value
	}
	return item, nil
}

func (p *siteParser) Parse(ch chan<- Product, results *models.ParseResults) error {
	// Keep the existing overall parse timeout unchanged.
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()

	body, err := p.fetch(ctx, "https://grohe-russia.shop/sitemap.xml")
	if err != nil {
		return fmt.Errorf("fetch sitemap: %w", err)
	}
	var sitemap struct {
		URLs []struct { Loc string `xml:"loc"` } `xml:"url"`
	}
	if err := xml.Unmarshal(body, &sitemap); err != nil {
		return fmt.Errorf("decode sitemap: %w", err)
	}

	// Stage 1: collect and group product URLs by category (like the previous
	// parser's category discovery stage). The sitemap remains the source of truth
	// for URLs, avoiding fragile assumptions about client-rendered product cards.
	grouped := make(map[string][]string)
	seen := make(map[string]struct{}, len(sitemap.URLs))
	for _, entry := range sitemap.URLs {
		u, parseErr := url.Parse(entry.Loc)
		if parseErr != nil || u.Scheme != "https" || u.Host != "grohe-russia.shop" ||
			u.RawQuery != "" || !strings.HasSuffix(u.Path, ".html") {
			continue
		}
		if _, exists := seen[entry.Loc]; exists {
			continue
		}
		seen[entry.Loc] = struct{}{}
		name, categoryErr := categoryFromURL(entry.Loc)
		if categoryErr != nil {
			continue
		}
		grouped[name] = append(grouped[name], entry.Loc)
	}
	if len(grouped) == 0 {
		return fmt.Errorf("no product URLs found in sitemap")
	}
	categories := make([]category, 0, len(grouped))
	for name, addresses := range grouped {
		categories = append(categories, category{name: name, urls: addresses})
	}

	// A single limiter is shared by all product workers.
	limiter := time.NewTicker(150 * time.Millisecond)
	defer limiter.Stop()
	categoryJobs := make(chan category)
	productJobs := make(chan productJob, productWorkerCount*2)
	var categoryWG, productWG sync.WaitGroup
	var mu sync.Mutex
	parsed, failed := 0, 0

	// Stage 2: category workers fan out product-page jobs.
	categoryWG.Add(categoryWorkerCount)
	for i := 0; i < categoryWorkerCount; i++ {
		go func() {
			defer categoryWG.Done()
			for cat := range categoryJobs {
				for _, address := range cat.urls {
					select {
					case productJobs <- productJob{category: cat.name, url: address}:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	}
	go func() {
		for _, cat := range categories {
			select {
			case categoryJobs <- cat:
			case <-ctx.Done():
				close(categoryJobs)
				categoryWG.Wait()
				close(productJobs)
				return
			}
		}
		close(categoryJobs)
		categoryWG.Wait()
		close(productJobs)
	}()

	// Stage 3: product workers load and parse each individual product page.
	productWG.Add(productWorkerCount)
	for i := 0; i < productWorkerCount; i++ {
		go func() {
			defer productWG.Done()
			for job := range productJobs {
				select {
				case <-ctx.Done():
					return
				case <-limiter.C:
				}
				page, fetchErr := p.fetch(ctx, job.url)
				var item Product
				if fetchErr == nil {
					item, fetchErr = parseProduct(page, job.url, job.category)
				}
				if fetchErr != nil {
					if errors.Is(fetchErr, errPageGone) {
						continue
					}
					log.Printf("GROHE parse failed %s: %v", job.url, fetchErr)
					mu.Lock()
					failed++
					mu.Unlock()
					continue
				}
				select {
				case ch <- item:
					mu.Lock()
					parsed++
					mu.Unlock()
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	productWG.Wait()

	results.Categories += len(categories)
	results.Parsed += parsed
	results.Errors += failed
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("GROHE import cancelled: %w", err)
	}
	if parsed == 0 {
		return fmt.Errorf("GROHE: no products parsed from %d URLs", len(seen))
	}
	if failed > 0 {
		return fmt.Errorf("GROHE: %d product pages failed out of %d URLs", failed, len(seen))
	}
	return nil
}
