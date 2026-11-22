package repository

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"parser/internal/models"
	"parser/internal/parser"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	insertBatchSize   = 500
	embeddingSize     = 1024
	defaultCandidates = 100
)

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

	for _, query := range []string{
		`CREATE EXTENSION IF NOT EXISTS pg_trgm`,
		`CREATE EXTENSION IF NOT EXISTS vector`,
		`ALTER TABLE product ADD COLUMN IF NOT EXISTS embedding vector(1024)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_product_link_unique ON product(link)`,
		`CREATE INDEX IF NOT EXISTS idx_product_name_trgm ON product USING GIN (product_name gin_trgm_ops)`,
		`CREATE INDEX IF NOT EXISTS idx_product_description_trgm ON product USING GIN (product_description gin_trgm_ops)`,
		`CREATE INDEX IF NOT EXISTS idx_product_info_trgm ON product USING GIN (info gin_trgm_ops)`,
		`CREATE INDEX IF NOT EXISTS idx_product_embedding_hnsw ON product USING hnsw (embedding vector_cosine_ops) WHERE embedding IS NOT NULL`,
	} {
		if _, err := conn.Exec(ctx, query); err != nil {
			conn.Close()
			return nil, fmt.Errorf("initialize database schema: %w", err)
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
			product_description = EXCLUDED.product_description,
			embedding = CASE
				WHEN product.product_name IS DISTINCT FROM EXCLUDED.product_name
				  OR product.category IS DISTINCT FROM EXCLUDED.category
				  OR product.info IS DISTINCT FROM EXCLUDED.info
				  OR product.product_description IS DISTINCT FROM EXCLUDED.product_description
				THEN NULL
				ELSE product.embedding
			END
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

// SaveEmbeddings persists vectors for products identified by their stable links.
func (db *postgresDB) SaveEmbeddings(products []parser.Product, embeddings [][]float32) error {
	if len(products) != len(embeddings) {
		return fmt.Errorf("products/embeddings length mismatch: %d != %d", len(products), len(embeddings))
	}
	if len(products) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	tx, err := db.conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin embedding transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	for i, product := range products {
		if len(embeddings[i]) != embeddingSize {
			return fmt.Errorf("product %q has embedding with %d dimensions, want %d", product.Link, len(embeddings[i]), embeddingSize)
		}
		if product.Link == "" {
			return fmt.Errorf("product at index %d has an empty link", i)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE product SET embedding = $1::vector WHERE link = $2`,
			vectorLiteral(embeddings[i]), product.Link,
		); err != nil {
			return fmt.Errorf("save embedding for %s: %w", product.Link, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit embedding transaction: %w", err)
	}
	return nil
}

// SearchSimilar returns nearest products while applying structured filters in SQL.
func (db *postgresDB) SearchSimilar(embedding []float32, filter models.Filters, limit int) ([]parser.Product, error) {
	if len(embedding) != embeddingSize {
		return nil, fmt.Errorf("query embedding has %d dimensions, want %d", len(embedding), embeddingSize)
	}
	if limit <= 0 {
		limit = defaultCandidates
	}

	query := `
SELECT category, product_name, price, info, product_description, link
FROM product
WHERE embedding IS NOT NULL
`
	args := []any{vectorLiteral(embedding)}

	categories := make([]string, 0, len(filter.Categories))
	for _, category := range filter.Categories {
		if category = strings.TrimSpace(category); category != "" {
			categories = append(categories, category)
		}
	}
	if len(categories) > 0 {
		query += " AND ("
		for i, category := range categories {
			if i > 0 {
				query += " OR "
			}
			query += fmt.Sprintf("category ILIKE $%d", len(args)+1)
			args = append(args, "%"+category+"%")
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

	query += fmt.Sprintf(" ORDER BY embedding <=> $1::vector LIMIT $%d", len(args)+1)
	args = append(args, limit)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rows, err := db.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("semantic product search: %w", err)
	}
	defer rows.Close()

	products := make([]parser.Product, 0, limit)
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
			return nil, fmt.Errorf("scan semantic product: %w", err)
		}
		products = append(products, product)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate semantic products: %w", err)
	}
	return products, nil
}

func vectorLiteral(values []float32) string {
	var builder strings.Builder
	builder.Grow(len(values) * 12)
	builder.WriteByte('[')
	for i, value := range values {
		if i > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(strconv.FormatFloat(float64(value), 'f', -1, 32))
	}
	builder.WriteByte(']')
	return builder.String()
}

func (db *postgresDB) GetProducts(filter models.Filters) ([]parser.Product, error) {
	query := `
SELECT category, product_name, price, info, product_description, link
FROM product
WHERE 1=1
`
	args := make([]any, 0)
	keywordGroups := make([][]string, 0, len(filter.Keywords))
	for _, group := range filter.Keywords {
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
		addedCategory := false
		for _, category := range filter.Categories {
			category = strings.TrimSpace(category)
			if category == "" {
				continue
			}
			if addedCategory {
				query += " OR "
			}
			addedCategory = true
			query += fmt.Sprintf("category ILIKE $%d", len(args)+1)
			args = append(args, "%"+category+"%")
		}
		if addedCategory {
			query += ")"
		} else {
			query = strings.TrimSuffix(query, " AND (")
		}
	}

	for _, group := range keywordGroups {
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

	scoreTerms := make([]string, 0)
	for _, group := range keywordGroups {
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
