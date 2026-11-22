CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS product(
    id BIGSERIAL PRIMARY KEY,
    product_name TEXT,
    category TEXT NOT NULL,
    price INTEGER,
    info TEXT,
    product_description TEXT,
    link TEXT,
    embedding vector(1024)
);

ALTER TABLE product ADD COLUMN IF NOT EXISTS embedding vector(1024);

CREATE INDEX IF NOT EXISTS idx_product_category ON product(category);
CREATE INDEX IF NOT EXISTS idx_product_price ON product(price);
CREATE UNIQUE INDEX IF NOT EXISTS idx_product_link_unique ON product(link);
CREATE INDEX IF NOT EXISTS idx_product_name_trgm
    ON product USING GIN (product_name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_product_description_trgm
    ON product USING GIN (product_description gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_product_info_trgm
    ON product USING GIN (info gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_product_embedding_hnsw
    ON product USING hnsw (embedding vector_cosine_ops)
    WHERE embedding IS NOT NULL;
