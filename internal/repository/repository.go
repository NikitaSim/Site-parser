package repository

import (
	"context"
	"fmt"

	"parser/internal/parser"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository interface {
	SaveProduct(parser.Product) error
	SaveBuffer([]parser.Product) error
	CloseDB()
	ClearData() error
}

type postgresDB struct {
	conn  *pgxpool.Pool
	dbUrl string
}

//const dbConfig = "postgres://user:password@localhost:5433/postgres"

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
