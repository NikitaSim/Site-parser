package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"parser/internal/models"
	"parser/internal/parser"

	"github.com/jackc/pgx/v5/pgxpool"
)

const insertBatchSize = 500

type Repository interface {
	SaveProduct(parser.Product) error
	SaveBuffer([]parser.Product) error
	GetProducts(models.Filters) ([]parser.Product, error)
	CloseDB()
}

type postgresDB struct {
	conn *pgxpool.Pool
}

func NewRepo(dbURL string) (*postgresDB, error) {
	cfg, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	cfg.MaxConns = 8
	cfg.MinConns = 1
	conn, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var pingErr error
	for {
		pingErr = conn.Ping(ctx)
		if pingErr == nil {
			break
		}
		select {
		case <-ctx.Done():
			conn.Close()
			return nil, fmt.Errorf("ping database: %w", pingErr)
		case <-time.After(time.Second):
		}
	}

	// Product links are the stable identity for the GROHE catalogue.
	if _, err := conn.Exec(ctx, `
		DELETE FROM product older
		USING product newer
		WHERE older.link = newer.link
		  AND older.link IS NOT NULL
		  AND older.id < newer.id
	`); err != nil {
		conn.Close()
		return nil, fmt.Errorf("remove duplicate product links: %w", err)
	}
	if _, err := conn.Exec(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_product_link_unique ON product(link)`); err != nil {
		conn.Close()
		return nil, fmt.Errorf("create unique product link index: %w", err)
	}
	if _, err := conn.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS pg_trgm`); err != nil {
		conn.Close()
		return nil, fmt.Errorf("enable pg_trgm: %w", err)
	}
	for _, indexQuery := range []string{
		`CREATE INDEX IF NOT EXISTS idx_product_name_trgm ON product USING GIN (product_name gin_trgm_ops)`,
		`CREATE INDEX IF NOT EXISTS idx_product_description_trgm ON product USING GIN (product_description gin_trgm_ops)`,
		`CREATE INDEX IF NOT EXISTS idx_product_info_trgm ON product USING GIN (info gin_trgm_ops)`,
	} {
		if _, err := conn.Exec(ctx, indexQuery); err != nil {
			conn.Close()
			return nil, fmt.Errorf("create trigram index: %w", err)
		}
	}

	return &postgresDB{conn: conn}, nil
}

func (db *postgresDB) CloseDB() {
	db.conn.Close()
}

func (db *postgresDB) SaveProduct(product parser.Product) error {
	return db.SaveBuffer([]parser.Product{product})
}

// SaveBuffer writes bounded multi-row INSERT batches. One importer goroutine
// batches records; each SQL statement contains at most 500 rows / 3000 args.
func (db *postgresDB) SaveBuffer(products []parser.Product) error {
	if len(products) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	for start := 0; start < len(products); start += insertBatchSize {
		end := start + insertBatchSize
		if end > len(products) {
			end = len(products)
		}
		batch := products[start:end]

		var query strings.Builder
		query.WriteString(`INSERT INTO product
			(product_name, category, price, info, product_description, link)
			VALUES `)
		args := make([]any, 0, len(batch)*6)

		for i, product := range batch {
			if i > 0 {
				query.WriteByte(',')
			}
			offset := i*6 + 1
			fmt.Fprintf(&query, "($%d,$%d,$%d,$%d,$%d,$%d)",
				offset, offset+1, offset+2, offset+3, offset+4, offset+5)
			args = append(args,
				product.Title,
				product.Category,
				product.Price,
				product.Info,
				product.Description,
				product.Link,
			)
		}

		query.WriteString(` ON CONFLICT (link) DO UPDATE SET
			product_name = EXCLUDED.product_name,
			category = EXCLUDED.category,
			price = EXCLUDED.price,
			info = EXCLUDED.info,
			product_description = EXCLUDED.product_description
			WHERE product.product_name IS DISTINCT FROM EXCLUDED.product_name
			   OR product.category IS DISTINCT FROM EXCLUDED.category
			   OR product.price IS DISTINCT FROM EXCLUDED.price
			   OR product.info IS DISTINCT FROM EXCLUDED.info
			   OR product.product_description IS DISTINCT FROM EXCLUDED.product_description`)

		if _, err := db.conn.Exec(ctx, query.String(), args...); err != nil {
			return fmt.Errorf("upsert product batch [%d:%d]: %w", start, end, err)
		}
	}
	return nil
}

func (db *postgresDB) GetProducts(filter models.Filters) ([]parser.Product, error) {
	query := `
SELECT category, product_name, price, info, product_description, link
FROM product
WHERE 1=1
`
	args := make([]any, 0)
	keywordGroups := make([][]string, 0, len(filter.Keywords))
	for _, group := range keywordGroups {
		clean := make([]string, 0, len(group))
		for _, word := range group {
			word = strings.TrimSpace(word)
			if word != "" {
				clean = append(clean, word)
			}
		}
		if len(clean) > 0 {
			keywordGroups = append(keywordGroups, clean)
		}
	}

	if len(filter.Categories) > 0 {
		query += " AND ("
		for i, category := range filter.Categories {
			if i > 0 {
				query += " OR "
			}
			query += fmt.Sprintf("category ILIKE $%d", len(args)+1)
			args = append(args, "%"+category+"%")
		}
		query += ")"
	}

	for _, group := range keywordGroups {
		if len(group) == 0 {
			continue
		}
		query += " AND ("
		for i, word := range group {
			if i > 0 {
				query += " OR "
			}
			placeholder := len(args) + 1
			query += fmt.Sprintf("(product_name ILIKE $%d OR product_description ILIKE $%d OR info ILIKE $%d)",
				placeholder, placeholder, placeholder)
			args = append(args, "%"+word+"%")
		}
		query += ")"
	}

	if filter.PriceMin != nil {
		query += fmt.Sprintf(" AND price >= $%d", len(args)+1)
		args = append(args, *filter.PriceMin)
	}
	if filter.PriceMax != nil {
		query += fmt.Sprintf(" AND price <= $%d", len(args)+1)
		args = append(args, *filter.PriceMax)
	}

	// Use trigram similarity to put the most promising candidates first.
	// ILIKE remains the filter; similarity is used for ranking the survivors.
	scoreTerms := make([]string, 0)
	for _, group := range filter.Keywords {
		for _, word := range group {
			placeholder := len(args) + 1
			scoreTerms = append(scoreTerms, fmt.Sprintf(
				"GREATEST(similarity(COALESCE(product_name, ''), $%d), similarity(COALESCE(product_description, ''), $%d), similarity(COALESCE(info, ''), $%d))",
				placeholder, placeholder, placeholder,
			))
			args = append(args, word)
		}
	}
	if len(scoreTerms) > 0 {
		query += " ORDER BY (" + strings.Join(scoreTerms, " + ") + ") DESC, product_name"
	} else {
		query += " ORDER BY product_name"
	}
	query += " LIMIT 60"

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rows, err := db.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query products: %w", err)
	}
	defer rows.Close()

	products := make([]parser.Product, 0)
	for rows.Next() {
		var product parser.Product
		if err := rows.Scan(
			&product.Category,
			&product.Title,
			&product.Price,
			&product.Info,
			&product.Description,
			&product.Link,
		); err != nil {
			return nil, fmt.Errorf("scan product row: %w", err)
		}
		products = append(products, product)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate product rows: %w", err)
	}
	return products, nil
}
