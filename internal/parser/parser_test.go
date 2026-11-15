package parser

import (
	"testing"
)

func TestProduct(t *testing.T) {
	for _, tc := range []struct {
		price string
		valid bool
	}{{"47500.00", true}, {"0", true}, {"12.50", false}, {"-1", false}, {"2147483648", false}} {
		body := []byte(`<script type="application/ld+json">{"@type":"Product","name":"Товар","sku":"123","offers":{"price":"` + tc.price + `","priceCurrency":"RUB"}}</script>`)
		p, e := parseProduct(body, "https://grohe-russia.shop/wc/test.html")
		if (e == nil) != tc.valid {
			t.Fatalf("price %s: %v", tc.price, e)
		}
		if e == nil && (p.Category != "Туалет" || p.Price == nil) {
			t.Fatal(p)
		}
	}
	if _, e := parseProduct([]byte(`<h1>Not product</h1>`), "https://grohe-russia.shop/test.html"); e == nil {
		t.Fatal("expected error")
	}
}

func TestSoft404(t *testing.T) {
	_, err := extract([]byte("<html><title>404 Not Found</title></html>"))
	if err != errPageGone {
		t.Fatalf("expected removed page, got %v", err)
	}
	_, err = extract([]byte("<html><title>GROHE</title></html>"))
	if err == nil || err == errPageGone {
		t.Fatalf("expected extraction error, got %v", err)
	}
}
