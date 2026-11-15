CREATE TABLE IF NOT EXISTS product(
    id BIGSERIAL PRIMARY KEY,
    product_name TEXT,
    category TEXT NOT NULL,
    price INTEGER,
    info TEXT,
    product_description TEXT,
    link TEXT
);

CREATE INDEX IF NOT EXISTS idx_product_category ON product(category);
CREATE INDEX IF NOT EXISTS idx_product_name ON product(product_name);
CREATE INDEX IF NOT EXISTS idx_product_price ON product(price);
CREATE UNIQUE INDEX IF NOT EXISTS idx_product_link_unique ON product(link);
