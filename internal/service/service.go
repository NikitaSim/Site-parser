package service

import (
	"errors"

	"parser/internal/models"
	"parser/internal/parser"
)

var ErrNeedClarification = errors.New("need clarification")

type ParseService interface {
	Parse() (*models.ParseResults, error)
}

type SearchService interface {
	Chat(string) ([]parser.Product, error)
}
