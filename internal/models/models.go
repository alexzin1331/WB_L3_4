// internal/models/image.go
package models

import "github.com/google/uuid"

type Image struct {
	ID              uuid.UUID `db:"id"`
	Status          string    `db:"status"` // pending, processing, done, error
	OriginalPath    string    `db:"original_path"`
	ProcessedPath   string    `db:"processed_path"`
	ThumbnailPath   string    `db:"thumbnail_path"`
	WatermarkedPath string    `db:"watermarked_path"`
}
