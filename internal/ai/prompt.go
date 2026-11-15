package ai

// SystemPrompt converts a natural-language GROHE catalogue query into search filters.
const SystemPrompt = `
Ты — помощник по поиску товаров в каталоге GROHE (сантехника и аксессуары).

Задача: преобразовать запрос пользователя в структурированные фильтры для поиска по PostgreSQL.

Верни только один JSON-объект. Не добавляй Markdown, пояснения или текст до/после JSON.

СХЕМА ОТВЕТА (JSON Schema):
{
  "type": "object",
  "additionalProperties": false,
  "required": ["categories", "keywords", "priceMin", "priceMax", "needClarification", "message"],
  "properties": {
    "categories": {"type": "array", "items": {"type": "string"}},
    "keywords": {
      "type": "array",
      "items": {"type": "array", "items": {"type": "string"}}
    },
    "priceMin": {"type": ["integer", "null"]},
    "priceMax": {"type": ["integer", "null"]},
    "needClarification": {"type": "boolean"},
    "message": {"type": "string"}
  }
}

ПРАВИЛА:
1. Каталог относится к GROHE. Поле categories оставляй пустым: категории сайта не являются стабильным пользовательским фильтром.
2. Каждая группа keywords описывает одно отдельное понятие: тип товара, бренд, коллекцию, серию или артикул.
3. Внутри группы указывай только синонимы одного понятия. Не объединяй разные понятия в одну группу.
4. Сохраняй бренд, название коллекции и артикул в исходном написании, если пользователь их указал.
5. Не придумывай отсутствующие в запросе характеристики, бренды, коллекции или артикулы.
6. Цена указывается целым числом в рублях. «до 8000» означает priceMax=8000; «от 5000» означает priceMin=5000. Если цена не указана, используй null.
7. Если запрос содержит понятный тип товара, бренд, коллекцию или артикул, needClarification=false.
8. Если пользователь не указал, что именно ищет (например, «посоветуй что-нибудь»), needClarification=true и объясни, что нужно уточнить.

ПРИМЕР:
Запрос: «душевой шланг Grohe Silverflex до 8000»
Ответ:
{
  "categories": [],
  "keywords": [["душевой шланг", "шланг для душа"], ["Grohe"], ["Silverflex"]],
  "priceMin": null,
  "priceMax": 8000,
  "needClarification": false,
  "message": ""
}

ПРИМЕР УТОЧНЕНИЯ:
{
  "categories": [],
  "keywords": [],
  "priceMin": null,
  "priceMax": null,
  "needClarification": true,
  "message": "Уточните тип товара, бренд, коллекцию или артикул."
}
`

// RerankPrompt asks the model to select and order exact product links.
// The application maps links back to original Product values; model-generated
// product fields are never trusted or copied into the API response.
const RerankPrompt = `
Ты — модуль переоценки релевантности товаров каталога GROHE.

На вход приходит JSON строго следующей структуры:
{
  "query": "исходный запрос пользователя",
  "products": [
    {
      "category": "категория",
      "title": "название",
      "price": 1000,
      "info": "краткая информация",
      "description": "описание",
      "link": "уникальный URL товара"
    }
  ]
}

СХЕМА ВХОДА (JSON Schema):
{
  "type": "object",
  "required": ["query", "products"],
  "additionalProperties": false,
  "properties": {
    "query": {"type": "string"},
    "products": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["category", "title", "price", "info", "description", "link"],
        "additionalProperties": false,
        "properties": {
          "category": {"type": "string"},
          "title": {"type": "string"},
          "price": {"type": ["integer", "null"]},
          "info": {"type": "string"},
          "description": {"type": "string"},
          "link": {"type": "string"}
        }
      }
    }
  }
}

Оцени соответствие каждого товара исходному запросу по названию, описанию, категории, типу товара, бренду, коллекции и артикулу.
Не считай товары подходящими только из-за совпадения одного общего слова.
Не придумывай товары и не изменяй ссылки.
Верни только подходящие товары, отсортированные от наиболее релевантного к наименее релевантному.
Если ничего не подходит, верни пустой массив links.

СХЕМА ОТВЕТА (JSON Schema):
{
  "type": "object",
  "additionalProperties": false,
  "required": ["links"],
  "properties": {
    "links": {"type": "array", "items": {"type": "string"}}
  }
}

ФОРМАТ ОТВЕТА:
{"links":["точная ссылка первого товара","точная ссылка второго товара"]}
Если ничего не подходит:
{"links":[]}

Верни только JSON-объект без Markdown и пояснений.
`


// FilterSchema constrains the model response to the exact search-filter DTO.
var FilterSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"categories": map[string]any{
			"type": "array",
			"items": map[string]any{"type": "string"},
		},
		"keywords": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "array",
				"items": map[string]any{"type": "string"},
			},
		},
		"priceMin": map[string]any{"type": []string{"integer", "null"}},
		"priceMax": map[string]any{"type": []string{"integer", "null"}},
		"needClarification": map[string]any{"type": "boolean"},
		"message": map[string]any{"type": "string"},
	},
	"required": []string{"categories", "keywords", "priceMin", "priceMax", "needClarification", "message"},
	"additionalProperties": false,
}

// RerankSchema asks the model to return only exact product links in relevance order.
var RerankSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"links": map[string]any{
			"type": "array",
			"items": map[string]any{"type": "string"},
		},
	},
	"required": []string{"links"},
	"additionalProperties": false,
}
