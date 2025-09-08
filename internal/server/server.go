package server

import (
	"fmt"
	"image"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"WB_L3_4/internal/models"
	"WB_L3_4/internal/storage"
	"github.com/disintegration/imaging"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

type Server struct {
	cfg      *models.Config
	router   *gin.Engine
	db       *storage.Storage
	producer *kafka.Writer
}

func NewServer(cfg *models.Config, db *storage.Storage, producer *kafka.Writer) *Server {
	r := gin.Default()
	r.Static("/web", "./web")
	r.Static("/files", cfg.StoragePath)

	s := &Server{cfg: cfg, router: r, db: db, producer: producer}

	r.POST("/upload", s.handleUpload)
	r.GET("/image/:id", s.handleGetImage)
	r.DELETE("/image/:id", s.handleDeleteImage)
	r.GET("/", func(c *gin.Context) {
		c.File("./web/index.html")
	})

	return s
}

func (s *Server) Start() error {
	return s.router.Run(s.cfg.ServerAddr)
}

func (s *Server) Stop() {
	// No shutdown needed for gin
}

func (s *Server) handleUpload(c *gin.Context) {
	const op = "server.handleUpload"

	file, err := c.FormFile("image")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	id := uuid.New()
	originalPath := filepath.Join(s.cfg.StoragePath, "original", id.String()+filepath.Ext(file.Filename))
	if err := os.MkdirAll(filepath.Dir(originalPath), 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("%s: %v", op, err)})
		return
	}

	f, err := os.Create(originalPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("%s: %v", op, err)})
		return
	}
	defer f.Close()

	src, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("%s: %v", op, err)})
		return
	}
	defer src.Close()

	if _, err := io.Copy(f, src); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("%s: %v", op, err)})
		return
	}

	img := models.Image{
		ID:           id,
		Status:       "pending",
		OriginalPath: originalPath,
	}
	if err := s.db.SaveImage(&img); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("%s: %v", op, err)})
		return
	}

	// Send to Kafka
	err = s.producer.WriteMessages(c.Request.Context(), kafka.Message{Value: []byte(id.String())})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("%s: %v", op, err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": id.String()})
}

func (s *Server) handleGetImage(c *gin.Context) {
	const op = "server.handleGetImage"
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("%s: %v", op, err)})
		return
	}

	img, err := s.db.GetImage(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("%s: %v", op, err)})
		return
	}

	if img.Status != "done" {
		c.JSON(http.StatusAccepted, gin.H{"status": img.Status})
		return
	}

	// For simplicity, return processed path
	c.File(img.ProcessedPath)
}

func (s *Server) handleDeleteImage(c *gin.Context) {
	const op = "server.handleDeleteImage"
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("%s: %v", op, err)})
		return
	}

	img, err := s.db.GetImage(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("%s: %v", op, err)})
		return
	}

	// Delete files
	os.Remove(img.OriginalPath)
	os.Remove(img.ProcessedPath)
	os.Remove(img.ThumbnailPath)
	os.Remove(img.WatermarkedPath)

	if err := s.db.DeleteImage(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("%s: %v", op, err)})
		return
	}

	c.Status(http.StatusNoContent)
}

func ProcessImage(idStr string, cfg *models.Config) error {
	const op = "server.processImage"
	id, err := uuid.Parse(idStr)
	if err != nil {
		return fmt.Errorf("%s: %v", op, err)
	}

	db, err := storage.NewStorage(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("%s: %v", op, err)
	}
	defer db.Close()

	img, err := db.GetImage(id)
	if err != nil {
		return fmt.Errorf("%s: %v", op, err)
	}

	if img.Status != "pending" {
		return nil // Already processed or error
	}

	img.Status = "processing"
	if err := db.UpdateImage(img); err != nil {
		return fmt.Errorf("%s: %v", op, err)
	}

	src, err := imaging.Open(img.OriginalPath)
	if err != nil {
		img.Status = "error"
		db.UpdateImage(img)
		return fmt.Errorf("%s: %v", op, err)
	}

	// Resize (e.g., to 800x600)
	resized := imaging.Resize(src, 800, 0, imaging.Lanczos)
	resizedPath := filepath.Join(cfg.StoragePath, "processed", id.String()+"_resized.jpg")
	if err := imaging.Save(resized, resizedPath); err != nil {
		img.Status = "error"
		db.UpdateImage(img)
		return fmt.Errorf("%s: %v", op, err)
	}
	img.ProcessedPath = resizedPath

	// Thumbnail (100x100)
	thumb := imaging.Thumbnail(src, 100, 100, imaging.Lanczos)
	thumbPath := filepath.Join(cfg.StoragePath, "processed", id.String()+"_thumb.jpg")
	if err := imaging.Save(thumb, thumbPath); err != nil {
		img.Status = "error"
		db.UpdateImage(img)
		return fmt.Errorf("%s: %v", op, err)
	}
	img.ThumbnailPath = thumbPath

	// Watermark (using a pre-existing watermark image)
	watermark, err := imaging.Open("watermark.png") // Replace with actual watermark image path
	if err != nil {
		img.Status = "error"
		db.UpdateImage(img)
		return fmt.Errorf("%s: %v", op, err)
	}

	// Overlay watermark at position (10, 10) with 50% opacity
	watermarked := imaging.Overlay(src, watermark, image.Point{X: 10, Y: 10}, 0.5)
	watermarkedPath := filepath.Join(cfg.StoragePath, "processed", id.String()+"_watermarked.jpg")
	if err := imaging.Save(watermarked, watermarkedPath); err != nil {
		img.Status = "error"
		db.UpdateImage(img)
		return fmt.Errorf("%s: %v", op, err)
	}
	img.WatermarkedPath = watermarkedPath

	img.Status = "done"
	if err := db.UpdateImage(img); err != nil {
		return fmt.Errorf("%s: %v", op, err)
	}

	return nil
}
