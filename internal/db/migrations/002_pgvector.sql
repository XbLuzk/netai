-- +goose Up
CREATE EXTENSION IF NOT EXISTS vector;

ALTER TABLE functions ADD COLUMN embedding vector(1536);

CREATE INDEX functions_embedding_idx ON functions USING hnsw (embedding vector_cosine_ops);

-- +goose Down
DROP INDEX IF EXISTS functions_embedding_idx;
ALTER TABLE functions DROP COLUMN IF EXISTS embedding;
DROP EXTENSION IF EXISTS vector;
