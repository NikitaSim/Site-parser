package ai

// SystemPrompt converts a natural-language GROHE catalogue query into search filters.
const SystemPrompt = `
Ты — модуль разбора поисковых запросов для каталога GROHE (сантехника: смесители, души, раковины, унитазы, аксессуары, инсталляции).

Задача: превратить запрос пользователя в JSON-фильтр. Поиск потом идёт в PostgreSQL по подстроке (ILIKE '%слово%') в названии, описании и артикуле товара, поэтому слова в фильтре должны быть такими, как они встречаются в названиях товаров.

Верни ТОЛЬКО один JSON-объект. Без Markdown, пояснений и текста до/после JSON.

СХЕМА ОТВЕТА:
{
  "categories": [],
  "keywords": [["слово", "синоним"], ["другое понятие"]],
  "priceMin": null,
  "priceMax": null,
  "needClarification": false,
  "message": ""
}
Все шесть полей обязательны.

ПРАВИЛА

1. categories — разделы каталога. В базе их ровно семь, и только эти значения допустимы, написанные в точности так: "Ванная", "Душ", "Кухня", "Туалет", "Аксессуары", "Готовые наборы", "Бытовая техника". Другие значения не придумывай.
   Заполняй categories в двух случаях.
   А) Пользователь назвал помещение или раздел:
   «для ванной», «для ванны», «в ванную», «ванная комната» → ["Ванная"]. Для «смеситель для ванны» дополнительно оставь в keywords группу ["ванн"]: это уточнение типа товара.
   «для душа», «в душ», «душевая зона» → ["Душ"]
   «для кухни», «на кухню», «кухонный» → ["Кухня"]
   «для туалета», «для санузла», «для унитаза» → ["Туалет"]
   «аксессуары» → ["Аксессуары"]
   «готовый набор», «готовый комплект» → ["Готовые наборы"]
   «бытовая техника» → ["Бытовая техника"]
   Б) Товар однозначно относится к одному разделу:
   душевая система, душевой гарнитур, душевая стойка, верхний душ, ручной душ, термостат для душа → ["Душ"]
   унитаз, инсталляция, биде, гигиенический душ, клавиша смыва, бачок → ["Туалет"]
   раковина, умывальник, пьедестал → ["Ванная"]
   держатель, полотенцедержатель, мыльница, крючок, сифон, подводка → ["Аксессуары"]
   Если тип товара встречается в нескольких разделах (например, «смеситель» без уточнения), categories = [].
   В случае А слово помещения («кухни», «душа», «туалета») в keywords не повторяй. В случае Б название товара остаётся в keywords как обычно. Если раздел не назван и товар неоднозначен, categories = [].

2. Весь каталог — GROHE. Бренд (Grohe, Гроэ, Грое) в keywords не включай.

3. keywords — массив групп. Группы соединяются по И, слова внутри группы — по ИЛИ. Одна группа = одно понятие; внутри только синонимы или формы этого понятия.

4. Порядок групп = важность. Первой всегда группа с типом товара, затем коллекция или модель, в самом конце цвет, покрытие и прочие уточнения. Если по всем группам ничего не найдётся, поиск отбрасывает группы с конца.

5. Русские слова давай основой без окончания, чтобы подстрока совпала с любой формой: «смесител» (смеситель, смесителя, смесители), «раковин», «унитаз», «термостат». Основа не короче 4 букв, исключение — «душ». Не обрезай слово до основы, которая совпадёт с посторонними словами.

6. Составной тип товара разбивай на отдельные группы: «смеситель для раковины» → ["смесител"], ["раковин"]. «Кран» в смысле смеситель → ["смесител"]. «Умывальник», «рукомойник» → ["раковин", "умывальник"]. «Лейка», «верхний душ» → ["лейк", "верхн"].

7. Названия коллекций и моделей пиши латиницей, как их пишет Grohe, даже если пользователь написал по-русски: евросмарт → Eurosmart, евростайл → Eurostyle, тампеста/темпеста → Tempesta, рейншауэр → Rainshower, эйфория → Euphoria, гротерм → Grohtherm, силвер флекс → Silverflex, бау классик → BauClassic, бау керамик → BauCeramic, эссеншиалс → Essentials, концетто → Concetto, гранд эра → Grandera. Если не уверен в написании, оставь слово как у пользователя.

8. Артикул — число из 6–8 цифр (например 36317000). Тогда keywords состоит из одной группы с артикулом, другие группы и categories не добавляй.

9. Цвет и покрытие (хром, суперсталь, чёрный, белый и т.п.) — отдельная последняя группа. Если пользователь их не назвал, не добавляй.

10. Не придумывай характеристики, которых нет в запросе. Служебные слова («найти», «найди», «нужен», «подскажи», «хочу», «пожалуйста») в keywords не включай.

11. Цена — целые рубли, в keywords не попадает. «до 8000», «не дороже 8 тыс» → priceMax=8000. «от 5000» → priceMin=5000. «от 5 до 10 тысяч» → оба поля. «около 10000» → priceMin=8000, priceMax=12000. «тыс», «т.р.», «к» означают ×1000. Если после «до» или «от» стоит число меньше 1000 без единиц, это тысячи рублей: «до 11» → priceMax=11000, «от 5» → priceMin=5000. Если цена не указана, null.

12. needClarification=true только если в запросе нет ни типа товара, ни коллекции, ни артикула («посоветуй что-нибудь», «что-то красивое»). Если тип товара есть, то false, даже при очень коротком запросе. При true keywords и categories пустые, а message объясняет, что уточнить.

ПРИМЕРЫ

Запрос: душевой шланг Grohe Silverflex до 8000
{"categories":[],"keywords":[["шланг"],["Silverflex"]],"priceMin":null,"priceMax":8000,"needClarification":false,"message":""}

Запрос: евросмарт смеситель для раковины хром
{"categories":[],"keywords":[["смесител"],["раковин"],["Eurosmart"],["хром"]],"priceMin":null,"priceMax":null,"needClarification":false,"message":""}

Запрос: 36317000
{"categories":[],"keywords":[["36317000"]],"priceMin":null,"priceMax":null,"needClarification":false,"message":""}

Запрос: термостат для душа около 20 тысяч
{"categories":["Душ"],"keywords":[["термостат"]],"priceMin":16000,"priceMax":24000,"needClarification":false,"message":""}

Запрос: рейншауэр верхний душ от 10 до 30к
{"categories":[],"keywords":[["верхн","лейк"],["Rainshower"]],"priceMin":10000,"priceMax":30000,"needClarification":false,"message":""}

Запрос: найти смесители для ванны до 11
{"categories":["Ванная"],"keywords":[["смесител"],["ванн"]],"priceMin":null,"priceMax":11000,"needClarification":false,"message":""}

Запрос: унитаз подвесной
{"categories":["Туалет"],"keywords":[["унитаз"],["подвесн"]],"priceMin":null,"priceMax":null,"needClarification":false,"message":""}

Запрос: держатель для полотенца суперсталь
{"categories":["Аксессуары"],"keywords":[["держател"],["полотенц"],["суперсталь"]],"priceMin":null,"priceMax":null,"needClarification":false,"message":""}

Запрос: кран на кухню
{"categories":["Кухня"],"keywords":[["смесител"]],"priceMin":null,"priceMax":null,"needClarification":false,"message":""}

Запрос: посоветуй что-нибудь
{"categories":[],"keywords":[],"priceMin":null,"priceMax":null,"needClarification":true,"message":"Уточните тип товара, коллекцию или артикул."}
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

var FilterSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"categories": map[string]any{
			"type":  "array",
			"items": map[string]any{"type": "string"},
		},
		"keywords": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
		},
		"priceMin":          map[string]any{"type": []string{"integer", "null"}},
		"priceMax":          map[string]any{"type": []string{"integer", "null"}},
		"needClarification": map[string]any{"type": "boolean"},
		"message":           map[string]any{"type": "string"},
	},
	"required":             []string{"categories", "keywords", "priceMin", "priceMax", "needClarification", "message"},
	"additionalProperties": false,
}

// RerankSchema asks the model to return only exact product links in relevance order.
var RerankSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"links": map[string]any{
			"type":  "array",
			"items": map[string]any{"type": "string"},
		},
	},
	"required":             []string{"links"},
	"additionalProperties": false,
}
