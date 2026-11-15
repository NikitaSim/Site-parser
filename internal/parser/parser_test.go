package parser

import "testing"

func TestParseProduct(t *testing.T) {
	tests := []struct {
		name  string
		price string
		valid bool
	}{
		{name: "whole rubles", price: "47500.00", valid: true},
		{name: "zero", price: "0", valid: true},
		{name: "fractional rubles", price: "12.50", valid: false},
		{name: "negative", price: "-1", valid: false},
		{name: "integer overflow", price: "2147483648", valid: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(`<script type="application/ld+json">{"@type":"Product","name":"Товар","sku":"123","description":"Описание","offers":{"price":"` + test.price + `","priceCurrency":"RUB"}}</script>`)
			product, err := parseProduct(body, "https://grohe-russia.shop/wc/test.html")
			if (err == nil) != test.valid {
				t.Fatalf("price %s: unexpected error: %v", test.price, err)
			}
			if err == nil {
				if product.Category != "Туалет" || product.Price == nil {
					t.Fatalf("unexpected product: %+v", product)
				}
				if product.Title != "Товар" || product.Description != "Описание" || product.Link == "" {
					t.Fatalf("product fields were not extracted: %+v", product)
				}
			}
		})
	}
}

func TestExtractMissingProductJSONLD(t *testing.T) {
	_, err := extract([]byte("<html><title>GROHE</title></html>"))
	if err == nil {
		t.Fatal("expected JSON-LD extraction error")
	}
}

func TestExtractSoft404(t *testing.T) {
	_, err := extract([]byte("<html><title>404 Not Found</title></html>"))
	if err != errPageGone {
		t.Fatalf("expected removed page error, got %v", err)
	}
}
