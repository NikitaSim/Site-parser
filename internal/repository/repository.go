package repository

import (
	"context"
	"fmt"

	"parser/internal/models"
	"parser/internal/parser"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository interface {
	SaveProduct(parser.Product) error
	SaveBuffer([]parser.Product) error
	GetProducts(models.Filters) ([]parser.Product, error)
	CloseDB()
	ClearData() error
}

type postgresDB struct {
	conn  *pgxpool.Pool
	dbUrl string
}

func NewRepo(dbUrl string) (*postgresDB, error) {
	ctx := context.Background()
	conn, err := pgxpool.New(ctx, dbUrl)
	if err != nil {
		return nil, err
	}
	return &postgresDB{
		conn:  conn,
		dbUrl: dbUrl,
	}, nil
}

func (db *postgresDB) CloseDB() {
	db.conn.Close()
}

// сохранения одного товара
func (db *postgresDB) SaveProduct(product parser.Product) error {

	query := `
	INSERT INTO product (product_name, category, price, info, link) 
	VALUES ($1, $2, $3, $4, $5);`

	ctx := context.Background()
	_, err := db.conn.Exec(ctx, query,
		product.Title,
		product.Category,
		product.Price,
		product.Info,
		product.Link,
	)
	if err != nil {
		fmt.Println(product.Title)
		return fmt.Errorf("Insert error: %s", err)
	}

	return nil
}

// Батчевое сохранения товаров
func (db *postgresDB) SaveBuffer(products []parser.Product) error {

	query := "INSERT INTO product (product_name, category, price, info, product_description, link) VALUES \n"
	arg := make([]any, 0)

	for id, product := range products {
		offset := id * 6
		query += fmt.Sprintf("($%d, $%d, $%d, $%d, $%d, $%d),\n",
			offset+1, offset+2, offset+3, offset+4, offset+5, offset+6)
		arg = append(arg,
			product.Title,
			product.Category,
			product.Price,
			product.Info,
			product.Description,
			product.Link,
		)
	}

	query = query[:len(query)-2] + ";"

	ctx := context.Background()
	if _, err := db.conn.Exec(ctx, query, arg...); err != nil {
		return fmt.Errorf("Insert error: %s", err)
	}

	return nil
}

func (db *postgresDB) ClearData() error {

	ctx := context.Background()
	if _, err := db.conn.Exec(ctx, "TRUNCATE TABLE product;"); err != nil {
		return err
	}
	return nil
}

func (db *postgresDB) GetProducts(filter models.Filters) ([]parser.Product, error) {

	query := `
SELECT
	product_name,
	price,
	product_description,
	link
FROM product
WHERE 1=1
`

	args := make([]any, 0)

	// -----------------------------
	// Категории
	// -----------------------------

	if len(filter.Categories) > 0 {

		query += " AND ("

		for i, category := range filter.Categories {

			if i != 0 {
				query += " OR "
			}

			query += fmt.Sprintf("category ILIKE $%d", len(args)+1)
			args = append(args, "%"+category+"%")
		}

		query += ")"
	}

	// -----------------------------
	// Ключевые слова
	// -----------------------------

	for _, group := range filter.Keywords {

		if len(group) == 0 {
			continue
		}
		query += " AND ("

		for i, word := range group {
			if i != 0 {
				query += " OR "
			}
			query += fmt.Sprintf(`
				product_name ILIKE $%d
				OR product_description ILIKE $%d
			`, len(args)+1, len(args)+1)

			args = append(args, "%"+word+"%")
		}
		query += ")"

	}

	// -----------------------------
	// Цена
	// -----------------------------

	if filter.PriceMin != nil {

		query += fmt.Sprintf(
			" AND price >= $%d",
			len(args)+1,
		)

		args = append(args, *filter.PriceMin)
	}

	if filter.PriceMax != nil {

		query += fmt.Sprintf(
			" AND price <= $%d",
			len(args)+1,
		)

		args = append(args, *filter.PriceMax)
	}

	query += " LIMIT 50"

	// fmt.Println(query)
	// fmt.Println(args)

	ctx := context.Background()

	rows, err := db.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var products []parser.Product

	for rows.Next() {

		var product parser.Product

		if err := rows.Scan(
			&product.Title,
			&product.Price,
			&product.Description,
			&product.Link,
		); err != nil {
			continue
		}

		products = append(products, product)
	}

	return products, nil
}
