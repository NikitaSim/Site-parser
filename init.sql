CREATE TABLE IF NOT EXISTS product(
    id BIGSERIAL PRIMARY KEY,
    product_name TEXT, 
    category TEXT NOT NULL,
    price INTEGER,
    info TEXT,
    product_description TEXT,
    link TEXT
);

CREATE INDEX idx_product_category ON product(category);