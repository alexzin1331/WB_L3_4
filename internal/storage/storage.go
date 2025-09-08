// internal/storage/storage.go
package storage

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"WB_L3_4/internal/models"
)

type Storage struct {
	pool *pgxpool.Pool
	db   *sql.DB // For migrations
}

func NewStorage(dsn string) (*Storage, error) {
	const op = "storage.NewStorage"

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", op, err)
	}

	db := stdlib.OpenDBFromPool(pool)
	if err := runMigrations(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("%s: %v", op, err)
	}

	return &Storage{pool: pool, db: db}, nil
}

func (s *Storage) Close() {
	s.db.Close()
	s.pool.Close()
}

func (s *Storage) SaveImage(img *models.Image) error {
	const op = "storage.SaveImage"
	_, err := s.pool.Exec(context.Background(),
		`INSERT INTO images (id, status, original_path, processed_path, thumbnail_path, watermarked_path)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		img.ID, img.Status, img.OriginalPath, img.ProcessedPath, img.ThumbnailPath, img.WatermarkedPath)
	if err != nil {
		return fmt.Errorf("%s: %v", op, err)
	}
	return nil
}

func (s *Storage) GetImage(id uuid.UUID) (*models.Image, error) {
	const op = "storage.GetImage"
	var img models.Image
	err := s.pool.QueryRow(context.Background(),
		`SELECT id, status, original_path, processed_path, thumbnail_path, watermarked_path FROM images WHERE id = $1`,
		id).Scan(&img.ID, &img.Status, &img.OriginalPath, &img.ProcessedPath, &img.ThumbnailPath, &img.WatermarkedPath)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", op, err)
	}
	return &img, nil
}

func (s *Storage) UpdateImage(img *models.Image) error {
	const op = "storage.UpdateImage"
	_, err := s.pool.Exec(context.Background(),
		`UPDATE images SET status = $2, processed_path = $3, thumbnail_path = $4, watermarked_path = $5 WHERE id = $1`,
		img.ID, img.Status, img.ProcessedPath, img.ThumbnailPath, img.WatermarkedPath)
	if err != nil {
		return fmt.Errorf("%s: %v", op, err)
	}
	return nil
}

func (s *Storage) DeleteImage(id uuid.UUID) error {
	const op = "storage.DeleteImage"
	_, err := s.pool.Exec(context.Background(), `DELETE FROM images WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("%s: %v", op, err)
	}
	return nil
}
