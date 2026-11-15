package models

// Метрики парсинга
type ParseResults struct {
	Categories int `json:"categories"`
	Parsed     int `json:"parsed"`
	Inserted   int `json:"inserted"`
	Errors     int `json:"errors"`
}

// Фильтры для товаров
type Filters struct {
	Categories []string   `json:"categories"`
	Keywords   [][]string `json:"keywords"`
	PriceMin   *int       `json:"priceMin"`
	PriceMax   *int       `json:"priceMax"`

	NeedClarification bool   `json:"needClarification"`
	Message           string `json:"message"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model    string         `json:"model"`
	Messages []Message      `json:"messages"`
	Stream   bool           `json:"stream"`
	Format   map[string]any `json:"format,omitempty"`
	Options  *ChatOptions   `json:"options,omitempty"`
	Think    bool           `json:"think"`
}

type ChatResponse struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
}

type ChatOptions struct {
	Temperature float32 `json:"temperature,omitempty"`
	NumPredict  int     `json:"num_predict,omitempty"`
}
