-- +goose Up
CREATE TABLE IF NOT EXISTS images (
    id UUID PRIMARY KEY,
    status TEXT NOT NULL,
    original_path TEXT NOT NULL,
    processed_path TEXT,
    thumbnail_path TEXT,
    watermarked_path TEXT
);