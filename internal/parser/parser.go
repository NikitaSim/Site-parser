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
	Category    string
	Title       string
	Price       *int
	Info        string
	Description string
	Link        string
	Image       string
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

// func (p *siteParser) GetDescription(url string) (string, error) {

// 	fmt.Println("Open:", url)

// 	doc, err := p.LoadPage(url)
// 	if err != nil {
// 		return "", err
// 	}

// 	fmt.Println("Tabs:", doc.Find(".productTabsContent").Length())
// 	fmt.Println("Active:", doc.Find(".productTabsContent").Length())
// 	fmt.Println("P:", doc.Find(".productTabsContent p").Length())

// 	var builder strings.Builder

// 	doc.Find(".productTabsContent p").Each(func(i int, s *goquery.Selection) {

// 		key := strings.TrimSpace(s.Find("strong").Text())

// 		value := strings.TrimSpace(
// 			s.Clone().
// 				Find("strong").
// 				Remove().
// 				End().
// 				Text(),
// 		)

// 		value = strings.Join(strings.Fields(value), " ")

// 		if key != "" && value != "" {
// 			builder.WriteString(key)
// 			builder.WriteString(": ")
// 			builder.WriteString(value)
// 			builder.WriteString(". ")
// 		}
// 	})

// 	if builder.Len() > 0 {
// 		return builder.String(), nil
// 	}

// 	text := strings.TrimSpace(doc.Find(".productTabsContent p").First().Text())
// 	text = strings.Join(strings.Fields(text), " ")

// 	return text, nil
// }

func (p *siteParser) LoadPage(url string) (*goquery.Document, error) {

	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

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

func (p *siteParser) GetProducts(doc *goquery.Document, category string) []Product {

	var products []Product

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

		description, err := p.GetDescription(link)
		if err != nil {
			description = "error"
		}

		products = append(products, Product{
			Category:    category,
			Title:       title,
			Price:       price,
			Info:        info,
			Description: description,
			Link:        link,
		})

		fmt.Println(description)

	})

	return products
}

func (p *siteParser) Parse(ch chan<- Product, results *models.ParseResults) error {

	doc, err := p.LoadPage(url)
	if err != nil {
		return err
	}

	categories := p.GetCategories(doc)
	var wg sync.WaitGroup
	var mx sync.Mutex

	for _, category := range categories {
		wg.Add(1)
		results.Categories++
		go func(category Category) {
			defer wg.Done()
			var count int

			//fmt.Println(category.Name)

			doc, err := p.LoadPage(category.URL)
			if err != nil {
				fmt.Println(err)
				return
			}

			products := p.GetProducts(doc, category.Name)

			for _, product := range products {
				ch <- product
				count++
			}
			//fmt.Println(count)
			mx.Lock()
			results.Parsed += count
			mx.Unlock()

		}(category)
	}

	wg.Wait()

	return nil
}
