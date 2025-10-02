package server

import (
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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
	r.GET("/image/:id/info", s.handleGetImageInfo)
	r.GET("/image/:id/original", s.handleGetOriginalImage)
	r.GET("/image/:id/thumbnail", s.handleGetThumbnail)
	r.GET("/image/:id/watermarked", s.handleGetWatermarkedImage)
	r.DELETE("/image/:id", s.handleDeleteImage)

	// Individual processing endpoints
	r.POST("/image/:id/resize", s.handleResizeImage)
	r.POST("/image/:id/thumbnail", s.handleThumbnailImage)
	r.POST("/image/:id/watermark", s.handleWatermarkImage)
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

func (s *Server) isValidImageType(file *multipart.FileHeader) bool {
	validTypes := map[string]bool{
		"image/jpeg": true,
		"image/jpg":  true,
		"image/png":  true,
		"image/gif":  true,
	}

	// Check MIME type from header
	src, err := file.Open()
	if err != nil {
		return false
	}
	defer src.Close()

	// Read first 512 bytes to detect content type
	buffer := make([]byte, 512)
	_, err = src.Read(buffer)
	if err != nil {
		return false
	}

	contentType := http.DetectContentType(buffer)
	return validTypes[contentType]
}

func (s *Server) validateImageFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	_, _, err = image.DecodeConfig(file)
	return err
}

func (s *Server) fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (s *Server) handleUpload(c *gin.Context) {
	const op = "server.handleUpload"

	file, err := c.FormFile("image")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No image file provided"})
		return
	}

	// Validate file type
	if !s.isValidImageType(file) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid image format. Only JPEG, PNG, and GIF are supported"})
		return
	}

	// Validate file size (10MB limit)
	if file.Size > 10*1024*1024 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "File too large. Maximum size is 10MB"})
		return
	}

	id := uuid.New()
	ext := strings.ToLower(filepath.Ext(file.Filename))
	if ext == "" {
		ext = ".jpg" // Default extension
	}
	originalPath := filepath.Join(s.cfg.StoragePath, "original", id.String()+ext)

	if err := os.MkdirAll(filepath.Dir(originalPath), 0755); err != nil {
		log.Printf("%s: failed to create directory: %v", op, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create storage directory"})
		return
	}

	f, err := os.Create(originalPath)
	if err != nil {
		log.Printf("%s: failed to create file: %v", op, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create file"})
		return
	}
	defer f.Close()

	src, err := file.Open()
	if err != nil {
		log.Printf("%s: failed to open uploaded file: %v", op, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to open uploaded file"})
		return
	}
	defer src.Close()

	if _, err := io.Copy(f, src); err != nil {
		log.Printf("%s: failed to copy file: %v", op, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save file"})
		return
	}

	// Validate that it's actually a valid image by trying to decode it
	if err := s.validateImageFile(originalPath); err != nil {
		os.Remove(originalPath) // Clean up invalid file
		log.Printf("%s: invalid image file: %v", op, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid or corrupted image file"})
		return
	}

	img := models.Image{
		ID:              id,
		Status:          "pending",
		OriginalPath:    originalPath,
		ResizeStatus:    "pending",
		ThumbnailStatus: "pending",
		WatermarkStatus: "pending",
	}
	if err := s.db.SaveImage(&img); err != nil {
		log.Printf("%s: failed to save to database: %v", op, err)
		os.Remove(originalPath) // Clean up file
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save image metadata"})
		return
	}

	// Send to Kafka
	err = s.producer.WriteMessages(c.Request.Context(), kafka.Message{Value: []byte(id.String())})
	if err != nil {
		log.Printf("%s: failed to send to kafka: %v", op, err)
		// Don't return error here, just log it - the image is saved and can be processed manually
	}

	log.Printf("Image uploaded successfully: %s", id.String())
	c.JSON(http.StatusOK, gin.H{
		"id":      id.String(),
		"message": "Image uploaded successfully",
	})
}

func (s *Server) handleGetImage(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid image ID"})
		return
	}

	img, err := s.db.GetImage(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Image not found"})
		return
	}

	// Return status if not done processing
	if img.Status != "done" {
		c.JSON(http.StatusAccepted, gin.H{
			"id":     img.ID.String(),
			"status": img.Status,
		})
		return
	}

	// Check if processed file exists
	if img.ProcessedPath == "" || !s.fileExists(img.ProcessedPath) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Processed image not found"})
		return
	}

	// Return the processed image file
	c.File(img.ProcessedPath)
}

func (s *Server) handleGetImageInfo(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid image ID"})
		return
	}

	img, err := s.db.GetImage(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Image not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":               img.ID.String(),
		"status":           img.Status,
		"original_path":    img.OriginalPath,
		"processed_path":   img.ProcessedPath,
		"thumbnail_path":   img.ThumbnailPath,
		"watermarked_path": img.WatermarkedPath,
		"resize_status":    img.ResizeStatus,
		"thumbnail_status": img.ThumbnailStatus,
		"watermark_status": img.WatermarkStatus,
	})
}

func (s *Server) handleGetOriginalImage(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid image ID"})
		return
	}

	img, err := s.db.GetImage(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Image not found"})
		return
	}

	if !s.fileExists(img.OriginalPath) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Original image file not found"})
		return
	}

	c.File(img.OriginalPath)
}

func (s *Server) handleGetThumbnail(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid image ID"})
		return
	}

	img, err := s.db.GetImage(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Image not found"})
		return
	}

	if img.Status != "done" || img.ThumbnailPath == "" || !s.fileExists(img.ThumbnailPath) {
		// Return original image if thumbnail not ready
		if s.fileExists(img.OriginalPath) {
			c.File(img.OriginalPath)
		} else {
			c.JSON(http.StatusNotFound, gin.H{"error": "Image not available"})
		}
		return
	}

	c.File(img.ThumbnailPath)
}

func (s *Server) handleGetWatermarkedImage(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid image ID"})
		return
	}

	img, err := s.db.GetImage(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Image not found"})
		return
	}

	if img.WatermarkStatus != "done" || img.WatermarkedPath == "" || !s.fileExists(img.WatermarkedPath) {
		// Return original image if watermarked not ready
		if s.fileExists(img.OriginalPath) {
			c.File(img.OriginalPath)
		} else {
			c.JSON(http.StatusNotFound, gin.H{"error": "Image not available"})
		}
		return
	}

	c.File(img.WatermarkedPath)
}

func (s *Server) handleResizeImage(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid image ID"})
		return
	}

	img, err := s.db.GetImage(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Image not found"})
		return
	}

	if img.ResizeStatus == "processing" {
		c.JSON(http.StatusAccepted, gin.H{"message": "Resize already in progress"})
		return
	}

	if img.ResizeStatus == "done" {
		c.JSON(http.StatusOK, gin.H{"message": "Resize already completed", "path": img.ProcessedPath})
		return
	}

	// Start resize processing
	go func() {
		src, err := imaging.Open(img.OriginalPath)
		if err != nil {
			log.Printf("Failed to open image for resize: %v", err)
			return
		}

		processor := NewImageProcessor(s.cfg)
		if err := processor.ResizeHandler(img, src); err != nil {
			log.Printf("Resize processing failed: %v", err)
		}
	}()

	c.JSON(http.StatusAccepted, gin.H{"message": "Resize processing started"})
}

func (s *Server) handleThumbnailImage(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid image ID"})
		return
	}

	img, err := s.db.GetImage(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Image not found"})
		return
	}

	if img.ThumbnailStatus == "processing" {
		c.JSON(http.StatusAccepted, gin.H{"message": "Thumbnail generation already in progress"})
		return
	}

	if img.ThumbnailStatus == "done" {
		c.JSON(http.StatusOK, gin.H{"message": "Thumbnail already completed", "path": img.ThumbnailPath})
		return
	}

	// Start thumbnail processing
	go func() {
		src, err := imaging.Open(img.OriginalPath)
		if err != nil {
			log.Printf("Failed to open image for thumbnail: %v", err)
			return
		}

		processor := NewImageProcessor(s.cfg)
		if err := processor.ThumbnailHandler(img, src); err != nil {
			log.Printf("Thumbnail processing failed: %v", err)
		}
	}()

	c.JSON(http.StatusAccepted, gin.H{"message": "Thumbnail processing started"})
}

func (s *Server) handleWatermarkImage(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid image ID"})
		return
	}

	img, err := s.db.GetImage(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Image not found"})
		return
	}

	if img.WatermarkStatus == "processing" {
		c.JSON(http.StatusAccepted, gin.H{"message": "Watermark processing already in progress"})
		return
	}

	if img.WatermarkStatus == "done" {
		c.JSON(http.StatusOK, gin.H{"message": "Watermark already completed", "path": img.WatermarkedPath})
		return
	}

	// Start watermark processing
	go func() {
		src, err := imaging.Open(img.OriginalPath)
		if err != nil {
			log.Printf("Failed to open image for watermark: %v", err)
			return
		}

		processor := NewImageProcessor(s.cfg)
		if err := processor.WatermarkHandler(img, src); err != nil {
			log.Printf("Watermark processing failed: %v", err)
		}
	}()

	c.JSON(http.StatusAccepted, gin.H{"message": "Watermark processing started"})
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

// Separate processing handlers
type ImageProcessor struct {
	cfg *models.Config
}

func NewImageProcessor(cfg *models.Config) *ImageProcessor {
	return &ImageProcessor{cfg: cfg}
}

// ResizeHandler handles image resizing
func (p *ImageProcessor) ResizeHandler(img *models.Image, src image.Image) error {
	const op = "ImageProcessor.ResizeHandler"

	log.Printf("%s: starting resize for image %s", op, img.ID.String())

	// Update status to processing
	img.ResizeStatus = "processing"
	db, err := storage.NewStorage(p.cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("%s: %v", op, err)
	}
	defer db.Close()

	if err := db.UpdateImage(img); err != nil {
		log.Printf("%s: failed to update resize status: %v", op, err)
	}

	// Create processed directory if it doesn't exist
	processedDir := filepath.Join(p.cfg.StoragePath, "processed")
	if err := os.MkdirAll(processedDir, 0755); err != nil {
		img.ResizeStatus = "error"
		db.UpdateImage(img)
		return fmt.Errorf("%s: failed to create processed directory: %v", op, err)
	}

	// Resize to 800px width, maintain aspect ratio
	resized := imaging.Resize(src, 800, 0, imaging.Lanczos)
	resizedPath := filepath.Join(processedDir, img.ID.String()+"_resized.jpg")

	if err := imaging.Save(resized, resizedPath); err != nil {
		log.Printf("%s: failed to save resized image: %v", op, err)
		img.ResizeStatus = "error"
		db.UpdateImage(img)
		return fmt.Errorf("%s: %v", op, err)
	}

	img.ProcessedPath = resizedPath
	img.ResizeStatus = "done"

	if err := db.UpdateImage(img); err != nil {
		log.Printf("%s: failed to update image with resize results: %v", op, err)
		return fmt.Errorf("%s: %v", op, err)
	}

	log.Printf("%s: successfully resized image %s to %s", op, img.ID.String(), resizedPath)
	return nil
}

// ThumbnailHandler handles thumbnail generation
func (p *ImageProcessor) ThumbnailHandler(img *models.Image, src image.Image) error {
	const op = "ImageProcessor.ThumbnailHandler"

	log.Printf("%s: starting thumbnail generation for image %s", op, img.ID.String())

	// Update status to processing
	img.ThumbnailStatus = "processing"
	db, err := storage.NewStorage(p.cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("%s: %v", op, err)
	}
	defer db.Close()

	if err := db.UpdateImage(img); err != nil {
		log.Printf("%s: failed to update thumbnail status: %v", op, err)
	}

	// Create processed directory if it doesn't exist
	processedDir := filepath.Join(p.cfg.StoragePath, "processed")
	if err := os.MkdirAll(processedDir, 0755); err != nil {
		img.ThumbnailStatus = "error"
		db.UpdateImage(img)
		return fmt.Errorf("%s: failed to create processed directory: %v", op, err)
	}

	// Generate 100x100 thumbnail
	thumb := imaging.Thumbnail(src, 100, 100, imaging.Lanczos)
	thumbPath := filepath.Join(processedDir, img.ID.String()+"_thumb.jpg")

	if err := imaging.Save(thumb, thumbPath); err != nil {
		log.Printf("%s: failed to save thumbnail: %v", op, err)
		img.ThumbnailStatus = "error"
		db.UpdateImage(img)
		return fmt.Errorf("%s: %v", op, err)
	}

	img.ThumbnailPath = thumbPath
	img.ThumbnailStatus = "done"

	if err := db.UpdateImage(img); err != nil {
		log.Printf("%s: failed to update image with thumbnail results: %v", op, err)
		return fmt.Errorf("%s: %v", op, err)
	}

	log.Printf("%s: successfully created thumbnail %s for image %s", op, thumbPath, img.ID.String())
	return nil
}

// WatermarkHandler handles watermark application
func (p *ImageProcessor) WatermarkHandler(img *models.Image, src image.Image) error {
	const op = "ImageProcessor.WatermarkHandler"

	log.Printf("%s: starting watermark application for image %s", op, img.ID.String())

	// Update status to processing
	img.WatermarkStatus = "processing"
	db, err := storage.NewStorage(p.cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("%s: %v", op, err)
	}
	defer db.Close()

	if err := db.UpdateImage(img); err != nil {
		log.Printf("%s: failed to update watermark status: %v", op, err)
	}

	// Create processed directory if it doesn't exist
	processedDir := filepath.Join(p.cfg.StoragePath, "processed")
	if err := os.MkdirAll(processedDir, 0755); err != nil {
		img.WatermarkStatus = "error"
		db.UpdateImage(img)
		return fmt.Errorf("%s: failed to create processed directory: %v", op, err)
	}

	// Load watermark image
	watermarkPath := filepath.Join("/app", "watermark.png")
	if _, err := os.Stat(watermarkPath); os.IsNotExist(err) {
		// Try local path for development
		watermarkPath = "watermark.png"
	}

	watermark, err := imaging.Open(watermarkPath)
	if err != nil {
		log.Printf("%s: failed to open watermark image %s: %v", op, watermarkPath, err)
		// Don't fail the entire process if watermark fails, just skip it
		img.WatermarkStatus = "error"
		db.UpdateImage(img)
		return fmt.Errorf("%s: watermark not available: %v", op, err)
	}

	// Scale watermark to be 20% of the image width
	bounds := src.Bounds()
	watermarkWidth := bounds.Dx() / 5
	watermark = imaging.Resize(watermark, watermarkWidth, 0, imaging.Lanczos)

	// Position watermark in bottom-right corner with some padding
	position := image.Point{
		X: bounds.Dx() - watermark.Bounds().Dx() - 20,
		Y: bounds.Dy() - watermark.Bounds().Dy() - 20,
	}

	watermarked := imaging.Overlay(src, watermark, position, 0.7)
	watermarkedPath := filepath.Join(processedDir, img.ID.String()+"_watermarked.jpg")

	if err := imaging.Save(watermarked, watermarkedPath); err != nil {
		log.Printf("%s: failed to save watermarked image: %v", op, err)
		img.WatermarkStatus = "error"
		db.UpdateImage(img)
		return fmt.Errorf("%s: %v", op, err)
	}

	img.WatermarkedPath = watermarkedPath
	img.WatermarkStatus = "done"

	if err := db.UpdateImage(img); err != nil {
		log.Printf("%s: failed to update image with watermark results: %v", op, err)
		return fmt.Errorf("%s: %v", op, err)
	}

	log.Printf("%s: successfully watermarked image %s to %s", op, img.ID.String(), watermarkedPath)
	return nil
}

func ProcessImage(idStr string, cfg *models.Config) error {
	const op = "server.processImage"
	id, err := uuid.Parse(idStr)
	if err != nil {
		log.Printf("%s: invalid UUID %s: %v", op, idStr, err)
		return fmt.Errorf("%s: %v", op, err)
	}

	log.Printf("%s: starting processing for image %s", op, id.String())

	db, err := storage.NewStorage(cfg.DatabaseURL)
	if err != nil {
		log.Printf("%s: failed to connect to database: %v", op, err)
		return fmt.Errorf("%s: %v", op, err)
	}
	defer db.Close()

	img, err := db.GetImage(id)
	if err != nil {
		log.Printf("%s: failed to get image %s from database: %v", op, id.String(), err)
		return fmt.Errorf("%s: %v", op, err)
	}

	if img.Status != "pending" {
		log.Printf("%s: image %s already processed (status: %s)", op, id.String(), img.Status)
		return nil // Already processed or error
	}

	// Update main status to processing
	img.Status = "processing"
	if err := db.UpdateImage(img); err != nil {
		log.Printf("%s: failed to update status to processing: %v", op, err)
		return fmt.Errorf("%s: %v", op, err)
	}

	// Open and validate the image once for all processors
	src, err := imaging.Open(img.OriginalPath)
	if err != nil {
		log.Printf("%s: failed to open image %s: %v", op, img.OriginalPath, err)
		img.Status = "error"
		img.ResizeStatus = "error"
		img.ThumbnailStatus = "error"
		img.WatermarkStatus = "error"
		db.UpdateImage(img)
		return fmt.Errorf("%s: failed to open image: %v", op, err)
	}

	log.Printf("%s: successfully opened image %s", op, id.String())

	// Create image processor
	processor := NewImageProcessor(cfg)

	// Process with separate handlers
	var processingErrors []error

	// Run resize handler
	if err := processor.ResizeHandler(img, src); err != nil {
		log.Printf("%s: resize failed: %v", op, err)
		processingErrors = append(processingErrors, fmt.Errorf("resize: %v", err))
	}

	// Run thumbnail handler
	if err := processor.ThumbnailHandler(img, src); err != nil {
		log.Printf("%s: thumbnail failed: %v", op, err)
		processingErrors = append(processingErrors, fmt.Errorf("thumbnail: %v", err))
	}

	// Run watermark handler
	if err := processor.WatermarkHandler(img, src); err != nil {
		log.Printf("%s: watermark failed: %v", op, err)
		processingErrors = append(processingErrors, fmt.Errorf("watermark: %v", err))
	}

	// Determine final status based on individual processing results
	if len(processingErrors) == 3 {
		// All processing failed
		img.Status = "error"
	} else if len(processingErrors) > 0 {
		// Some processing failed, but at least one succeeded
		img.Status = "partial"
		log.Printf("%s: partial processing completed with errors: %v", op, processingErrors)
	} else {
		// All processing succeeded
		img.Status = "done"
	}

	// Update final status
	if err := db.UpdateImage(img); err != nil {
		log.Printf("%s: failed to update final status: %v", op, err)
		return fmt.Errorf("%s: %v", op, err)
	}

	if len(processingErrors) > 0 {
		log.Printf("%s: processing completed with some errors for image %s", op, id.String())
		return fmt.Errorf("%s: processing completed with errors", op)
	}

	log.Printf("%s: successfully processed image %s", op, id.String())
	return nil
}
