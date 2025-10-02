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

	storage := &Storage{pool: pool, db: db}

	// Check and update schema if needed
	if err := storage.ensureSchemaCompatibility(); err != nil {
		db.Close()
		return nil, fmt.Errorf("%s: %v", op, err)
	}

	return storage, nil
}

func (s *Storage) Close() {
	s.db.Close()
	s.pool.Close()
}

func (s *Storage) ensureSchemaCompatibility() error {
	const op = "storage.ensureSchemaCompatibility"

	// Check if new columns exist
	var count int
	err := s.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM information_schema.columns 
		 WHERE table_name = 'images' AND column_name IN ('resize_status', 'thumbnail_status', 'watermark_status')`).Scan(&count)

	if err != nil {
		return fmt.Errorf("%s: failed to check schema: %v", op, err)
	}

	// If we don't have all 3 columns, add them
	if count < 3 {
		_, err = s.pool.Exec(context.Background(), `
			DO $$ 
			BEGIN
				IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'images' AND column_name = 'resize_status') THEN
					ALTER TABLE images ADD COLUMN resize_status TEXT DEFAULT 'pending';
				END IF;
				
				IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'images' AND column_name = 'thumbnail_status') THEN
					ALTER TABLE images ADD COLUMN thumbnail_status TEXT DEFAULT 'pending';
				END IF;
				
				IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'images' AND column_name = 'watermark_status') THEN
					ALTER TABLE images ADD COLUMN watermark_status TEXT DEFAULT 'pending';
				END IF;
			END $$;
		`)
		if err != nil {
			return fmt.Errorf("%s: failed to add status columns: %v", op, err)
		}
	}

	return nil
}

func (s *Storage) SaveImage(img *models.Image) error {
	const op = "storage.SaveImage"

	// Try to insert with new schema first
	_, err := s.pool.Exec(context.Background(),
		`INSERT INTO images (id, status, original_path, processed_path, thumbnail_path, watermarked_path, resize_status, thumbnail_status, watermark_status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		img.ID, img.Status, img.OriginalPath, img.ProcessedPath, img.ThumbnailPath, img.WatermarkedPath,
		img.ResizeStatus, img.ThumbnailStatus, img.WatermarkStatus)

	if err != nil {
		// If new schema fails, try old schema (backward compatibility)
		_, fallbackErr := s.pool.Exec(context.Background(),
			`INSERT INTO images (id, status, original_path, processed_path, thumbnail_path, watermarked_path)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			img.ID, img.Status, img.OriginalPath, img.ProcessedPath, img.ThumbnailPath, img.WatermarkedPath)
		if fallbackErr != nil {
			return fmt.Errorf("%s: new schema failed: %v, old schema failed: %v", op, err, fallbackErr)
		}
	}
	return nil
}

func (s *Storage) GetImage(id uuid.UUID) (*models.Image, error) {
	const op = "storage.GetImage"
	var img models.Image
	err := s.pool.QueryRow(context.Background(),
		`SELECT id, status, original_path, processed_path, thumbnail_path, watermarked_path, 
		 COALESCE(resize_status, 'pending') as resize_status, 
		 COALESCE(thumbnail_status, 'pending') as thumbnail_status, 
		 COALESCE(watermark_status, 'pending') as watermark_status 
		 FROM images WHERE id = $1`,
		id).Scan(&img.ID, &img.Status, &img.OriginalPath, &img.ProcessedPath, &img.ThumbnailPath, &img.WatermarkedPath,
		&img.ResizeStatus, &img.ThumbnailStatus, &img.WatermarkStatus)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", op, err)
	}
	return &img, nil
}

func (s *Storage) UpdateImage(img *models.Image) error {
	const op = "storage.UpdateImage"

	// Try to update with new schema first
	_, err := s.pool.Exec(context.Background(),
		`UPDATE images SET status = $2, processed_path = $3, thumbnail_path = $4, watermarked_path = $5,
		 resize_status = $6, thumbnail_status = $7, watermark_status = $8 WHERE id = $1`,
		img.ID, img.Status, img.ProcessedPath, img.ThumbnailPath, img.WatermarkedPath,
		img.ResizeStatus, img.ThumbnailStatus, img.WatermarkStatus)

	if err != nil {
		// If new schema fails, try old schema (backward compatibility)
		_, fallbackErr := s.pool.Exec(context.Background(),
			`UPDATE images SET status = $2, processed_path = $3, thumbnail_path = $4, watermarked_path = $5 WHERE id = $1`,
			img.ID, img.Status, img.ProcessedPath, img.ThumbnailPath, img.WatermarkedPath)
		if fallbackErr != nil {
			return fmt.Errorf("%s: new schema failed: %v, old schema failed: %v", op, err, fallbackErr)
		}
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
