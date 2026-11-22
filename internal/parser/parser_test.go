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
			body := []byte("<script type=\"application/ld+json\">{\"@type\":\"Product\",\"name\":\"Товар\",\"sku\":\"123\",\"description\":\"Описание\",\"offers\":{\"price\":\"" + test.price + "\",\"priceCurrency\":\"RUB\"}}</script>")
			product, err := parseProduct(body, "https://grohe-russia.shop/wc/test.html")
			if (err == nil) != test.valid {
				t.Fatalf("price %s: unexpected error: %v", test.price, err)
			}
			if err == nil {
				if product.Category != "Туалет" || product.Price == nil {
					t.Fatalf("unexpected product: %+v", product)
				}
				if product.Title != "Товар" || product.Description != "" || product.Link == "" {
					t.Fatalf("product fields were not extracted: %+v", product)
				}
			}
		})
	}
}

func TestExtractMissingProductJSONLD(t *testing.T) {
	_, err := parseProduct([]byte("<html><title>GROHE</title></html>"), "")
	if err == nil {
		t.Fatal("expected JSON-LD extraction error")
	}
}

func TestExtractSoft404(t *testing.T) {
	_, err := parseProduct([]byte("<html><title>404 Not Found</title></html>"), "")
	if err != errPageGone {
		t.Fatalf("expected removed page error, got %v", err)
	}
}

func TestParseProductUsesCharacteristics(t *testing.T) {
	body := []byte(`
<script type="application/ld+json">{"@type":"Product","name":"Смеситель","description":"Рекламный текст"}</script>
<div id="box-additional">
  <div class="product-view-attributes">
    <div class="attribute-set__row">
      <dt class="attribute-set__name" title="Механизм управления"><span>Механизм управления</span><div class="attribute-tooltip">Подсказка</div></dt>
      <dd class="attribute-set__value"> керамический
          картридж </dd>
    </div>
    <div class="attribute-set__row">
      <dt class="attribute-set__name"><span>Высота излива:</span><div class="attribute-tooltip">Подсказка</div></dt>
      <dd class="attribute-set__value">207&nbsp;мм</dd>
    </div>
    <div class="attribute-set__row">
      <dt class="attribute-set__name">Цвет</dt>
      <dd class="attribute-set__value"> </dd>
    </div>
  </div>
</div>
<div class="attribute-set__row">
  <dt class="attribute-set__name">Доставка</dt><dd class="attribute-set__value">Бесплатно</dd>
</div>`)
	product, err := parseProduct(body, "https://grohe-russia.shop/kuhnja/test.html")
	if err != nil {
		t.Fatal(err)
	}
	want := "Механизм управления: керамический картридж\nВысота излива: 207 мм"
	if product.Description != want {
		t.Fatalf("description = %q, want %q", product.Description, want)
	}
}