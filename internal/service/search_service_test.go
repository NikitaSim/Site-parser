package service

import (
	"testing"

	"parser/internal/parser"
)

func TestLimitProducts(t *testing.T) {
	products := make([]parser.Product, 15)
	for i := range products {
		products[i].Title = "product"
	}

	got := limitProducts(products, maxSearchResults)
	if len(got) != maxSearchResults {
		t.Fatalf("limitProducts() returned %d products, want %d", len(got), maxSearchResults)
	}
}

func TestLimitProductsDoesNotExpandShortResult(t *testing.T) {
	products := make([]parser.Product, 3)

	got := limitProducts(products, maxSearchResults)
	if len(got) != len(products) {
		t.Fatalf("limitProducts() returned %d products, want %d", len(got), len(products))
	}
}

func TestLimitProductsZeroLimitReturnsEmptySlice(t *testing.T) {
	products := make([]parser.Product, 1)

	got := limitProducts(products, 0)
	if len(got) != 0 {
		t.Fatalf("limitProducts() returned %d products, want 0", len(got))
	}
}
