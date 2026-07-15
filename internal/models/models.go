package models

type ParseResults struct {
	Categories int `json:"categories"`
	Parsed     int `json:"parsed"`
	Inserted   int `json:"inserted"`
	Errors     int `json:"errors"`
}

type Filters struct {
	Category string `json:"category"`
	Name     string `json:"name"`
	PriceMin *int   `json:"price_min"`
	PriceMax *int   `json:"price_max"`

	NeedClarification bool   `json:"needClarification"`
	Message           string `json:"message"`
}
