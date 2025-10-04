package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/segmentio/kafka-go"

	"WB_L3_4/internal/models"
	"WB_L3_4/internal/server"
	"WB_L3_4/internal/storage"
)

func main() {
	cfg, err := models.LoadConfig("config.yaml")
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	db, err := storage.NewStorage(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("failed to init storage: %v", err)
	}
	defer db.Close()

	// Kafka producer
	producer := kafka.NewWriter(kafka.WriterConfig{
		Brokers: []string{cfg.KafkaBroker},
		Topic:   cfg.KafkaTopic,
	})

	// Start Kafka consumer in background
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		consumer := kafka.NewReader(kafka.ReaderConfig{
			Brokers: []string{cfg.KafkaBroker},
			Topic:   cfg.KafkaTopic,
			GroupID: "image-processor-group",
		})
		defer consumer.Close()

		for {
			msg, err := consumer.ReadMessage(ctx)
			if err != nil {
				if err == context.Canceled {
					return
				}
				log.Printf("error reading message: %v", err)
				continue
			}
			// Process image
			err = server.ProcessImage(string(msg.Value), cfg)
			if err != nil {
				log.Printf("error processing image: %v", err)
			}
		}
	}()

	srv := server.NewServer(cfg, db, producer)

	go func() {
		if err := srv.Start(); err != nil {
			log.Fatalf("failed to start server: %v", err)
		}
	}()

	// Graceful shutdown
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	cancel()
	srv.Stop()
	producer.Close()
}
