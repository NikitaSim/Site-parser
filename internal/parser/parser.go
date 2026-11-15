package parser

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"parser/internal/models"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
)

type Parser interface {
	Parse(chan<- Product, *models.ParseResults) error
}

type siteParser struct{ client *http.Client }

type Category struct {
	Name string
	URL  string
}

type Product struct {
	Category    string `json:"category"`
	Title       string `json:"title"` // Название товара
	Price       *int   `json:"price"`
	Info        string `json:"info"`        // Краткое описание с карточки
	Description string `json:"description"` // Полное описание товара
	Link        string `json:"link"`
}

func NewParser() *siteParser { return &siteParser{client: &http.Client{Timeout: 40 * time.Second}} }
func (p *siteParser) fetch(ctx context.Context, address string) ([]byte, error) {
	u, e := url.Parse(address)
	if e != nil || u.Scheme != "https" || u.Host != "grohe-russia.shop" {
		return nil, fmt.Errorf("invalid URL: %s", address)
	}
	req, e := http.NewRequestWithContext(ctx, "GET", address, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", "SiteParser-Grohe/1.0")
	r, e := p.client.Do(req)
	if e != nil {
		return nil, e
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d: %s", r.StatusCode, address)
	}
	return io.ReadAll(io.LimitReader(r.Body, 20<<20))
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

func extract(body []byte) (product, error) {
	d, e := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if e != nil {
		return product{}, e
	}
	var p product
	d.Find("script[type='application/ld+json']").Each(func(_ int, s *goquery.Selection) {
		var candidate product
		if json.Unmarshal([]byte(s.Text()), &candidate) == nil && candidate.Type == "Product" {
			p = candidate
		}
	})
	if p.Name == "" {
		return p, fmt.Errorf("Product JSON-LD absent")
	}
	return p, nil
}

func (p *siteParser) Parse(ch chan<- Product, results *models.ParseResults) error {
	body, err := p.fetch(context.Background(), "https://grohe-russia.shop/sitemap.xml")
	if err != nil {
		return err
	}
	var sitemap struct {
		URLs []struct {
			Loc string `xml:"loc"`
		} `xml:"url"`
	}
	if err = xml.Unmarshal(body, &sitemap); err != nil {
		return err
	}
	addresses := []string{}
	seen := map[string]bool{}
	for _, entry := range sitemap.URLs {
		u, e := url.Parse(entry.Loc)
		if e == nil && u.Host == "grohe-russia.shop" && u.Scheme == "https" && u.RawQuery == "" && strings.HasSuffix(u.Path, ".html") && !seen[entry.Loc] {
			seen[entry.Loc] = true
			addresses = append(addresses, entry.Loc)
		}
	}
	if len(addresses) == 0 {
		return fmt.Errorf("no product URLs in sitemap")
	}
	jobs := make(chan string)
	var wg sync.WaitGroup
	var mx sync.Mutex
	parsed, failed := 0, 0
	categories := map[string]bool{}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for address := range jobs {
				<-ticker.C
				b, e := p.fetch(context.Background(), address)
				var item Product
				if e == nil {
					item, e = parseProduct(b, address)
				}
				if e != nil {
					log.Printf("GROHE %s: %v", address, e)
					mx.Lock()
					failed++
					mx.Unlock()
					continue
				}
				ch <- item
				mx.Lock()
				parsed++
				categories[item.Category] = true
				mx.Unlock()
			}
		}()
	}
	for _, address := range addresses {
		jobs <- address
	}
	close(jobs)
	wg.Wait()
	results.Parsed += parsed
	results.Categories += len(categories)
	// Errors is owned by the service, avoiding concurrent metric writes.
	if failed > 0 {
		return fmt.Errorf("GROHE: %d failed pages of %d", failed, len(addresses))
	}
	return nil
}
func parseProduct(body []byte, address string) (Product, error) {
	p, e := extract(body)
	if e != nil {
		return Product{}, e
	}
	item := Product{Title: p.Name, Description: p.Description, Info: "Артикул: " + p.SKU, Link: address}
	if p.Offers.Price != "" {
		r, ok := new(big.Rat).SetString(string(p.Offers.Price))
		if !ok || !r.IsInt() || !r.Num().IsInt64() || r.Sign() < 0 || r.Num().Int64() > 2147483647 || p.Offers.Currency != "RUB" {
			return item, fmt.Errorf("unsupported price")
		}
		v := int(r.Num().Int64())
		item.Price = &v
	}
	u, e := url.Parse(address)
	if e != nil {
		return item, e
	}
	item.Category = strings.Split(strings.Trim(u.Path, "/"), "/")[0]
	names := map[string]string{"vannaya": "Ванная", "dush": "Душ", "kuhnja": "Кухня", "kitchen": "Кухня", "wc": "Туалет", "aksessuary": "Аксессуары", "nabory": "Наборы"}
	if name, ok := names[item.Category]; ok {
		item.Category = name
	}
	return item, nil
}
