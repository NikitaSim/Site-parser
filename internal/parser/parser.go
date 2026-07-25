package parser

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"parser/internal/models"

	"github.com/PuerkitoBio/goquery"
)

type Parser interface {
	Parse(chan<- Product, *models.ParseResults) error
}

type siteParser struct{}

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

const url = "https://nprommet.ru/"

func NewParser() *siteParser {
	return &siteParser{}
}

func (p *siteParser) GetDescription(url string) (string, error) {

	doc, err := p.LoadPage(url)
	if err != nil {
		return "", err
	}

	text := strings.TrimSpace(doc.Find(".productTabsContent").First().Text())

	text = strings.Join(strings.Fields(text), " ")

	return text, nil
}

// Загрузка страницы сайта
func (p *siteParser) LoadPage(url string) (*goquery.Document, error) {

	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	//fmt.Println("СТАТУС САЙТА", resp.StatusCode, url)

	return goquery.NewDocumentFromReader(resp.Body)
}

func (p *siteParser) GetCategories(doc *goquery.Document) []Category {

	var categories []Category

	doc.Find("#catalogMenu a").Each(func(i int, s *goquery.Selection) {

		name := strings.TrimSpace(s.Text())
		href, _ := s.Attr("href")

		categories = append(categories, Category{
			Name: name,
			URL:  href,
		})

	})

	return categories
}

func (p *siteParser) GetProducts(doc *goquery.Document, category string, products chan<- Product, count *int) {

	doc.Find(".product").Each(func(i int, s *goquery.Selection) {

		title := strings.TrimSpace(s.Find(".title").Text())

		priceText := strings.TrimSpace(s.Find(".price").Text())
		priceText = strings.ReplaceAll(priceText, "₽", "")
		priceText = strings.ReplaceAll(priceText, " ", "")

		info := strings.TrimSpace(s.Find(".info").Text())

		var price *int
		if priceText != "" {
			p, err := strconv.Atoi(priceText)
			if err == nil {
				price = &p
			}
		}

		link, _ := s.Find(".title").Attr("href")

		products <- Product{
			Category: category,
			Title:    title,
			Price:    price,
			Info:     info,
			Link:     link,
		}
		(*count)++
	})

}

func (p *siteParser) Parse(ch chan<- Product, results *models.ParseResults) error {

	doc, err := p.LoadPage(url)
	if err != nil {
		results.Errors++
		return err
	}

	categories := p.GetCategories(doc)
	var wg sync.WaitGroup
	var mx sync.Mutex

	product := make(chan Product, 32)
	var workerWG sync.WaitGroup

	for i := 0; i < 15; i++ { // Количество воркеров для получения описания товара
		workerWG.Add(1)
		go func() {
			defer workerWG.Done()
			for prod := range product {

				description, err := p.GetDescription(prod.Link)
				if err != nil {
					description = ""
				}
				prod.Description = description
				ch <- prod

				//fmt.Println(description)
			}
		}()
	}

	job := make(chan Category)
	go func() {
		defer close(job)
		for _, category := range categories {
			results.Categories++
			job <- category
		}
	}()

	for i := 0; i < 4; i++ { // Воркеры для получения товара по каждой категории
		wg.Add(1)
		go func() {
			defer wg.Done()

			for category := range job {
				var count int
				//fmt.Println(category.Name)

				doc, err := p.LoadPage(category.URL)
				if err != nil {
					fmt.Println(err)
					results.Errors++
					continue
				}

				p.GetProducts(doc, category.Name, product, &count)

				mx.Lock()
				results.Parsed += count
				mx.Unlock()
			}
		}()

	}

	go func() {
		wg.Wait()
		close(product)
	}()
	workerWG.Wait()

	return nil
}
