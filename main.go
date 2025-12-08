// Package main provides the entry point for the backend-antiginx API server.
//
// The server connects to PostgreSQL database and RabbitMQ message broker,
// performs database migrations, and starts an HTTP server for handling
// security scan requests.
//
// # Environment Variables
//
// The following environment variables are required:
//
//   - DATABASE_URL: PostgreSQL connection string (e.g., postgres://user:pass@host:5432/db)
//   - RABBITMQ_URL: RabbitMQ connection string (e.g., amqp://user:pass@host:5672/)
//
// # Example
//
// To run the server:
//
//	DATABASE_URL="postgres://..." RABBITMQ_URL="amqp://..." go run main.go
package main

import (
	"log"

	"os"

	"github.com/joho/godotenv"
	"github.com/prawo-i-piesc/backend/internal/api"
	"github.com/prawo-i-piesc/backend/internal/handlers"
	"github.com/prawo-i-piesc/backend/internal/models"
	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// main initializes and starts the backend-antiginx API server.
//
// It performs the following steps:
//  1. Loads environment variables from .env file (optional)
//  2. Establishes connection to PostgreSQL database
//  3. Runs database migrations for Scan and ScanResult models
//  4. Connects to RabbitMQ and declares the scan_queue
//  5. Initializes HTTP handlers and starts the server on port 8080
//
// The function will terminate with a fatal error if any critical
// initialization step fails (database connection, RabbitMQ connection, etc.)
func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("Info: Nie znaleziono pliku .env, używam zmiennych środowiskowych")
	}
	log.Println("Uruchamiam serwer API...")
	dsn := os.Getenv("DATABASE_URL")
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("Nie udało się połączyć z bazą danych: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("Błąd podczas pobierania instancji DB: %v", err)
	}
	if err := sqlDB.Ping(); err != nil {
		log.Fatalf("Błąd pingowania bazy danych: %v", err)
	}
	log.Println("Połączono z bazą danych przy użyciu GORM")

	if err := db.AutoMigrate(&models.Scan{}, &models.ScanResult{}); err != nil {
		log.Fatalf("Nie udało się wykonać migracji: %v", err)
	}

	conn, err := amqp.Dial(os.Getenv("RABBITMQ_URL"))
	if err != nil {
		log.Fatalf("Nie udało się połączyć z RabbitMQ: %v", err)
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		log.Fatalf("Nie udało się otworzyć kanału: %v", err)
	}
	defer ch.Close()

	_, err = ch.QueueDeclare(
		"scan_queue", // name
		true,         // durable
		false,        // delete when unused
		false,        // exclusive
		false,        // no-wait
		nil,          // arguments
	)
	if err != nil {
		log.Fatalf("Failed to declare a queue: %v", err)
	}

	scanHandler := handlers.NewScanHandler(ch, db)

	router := api.NewRouter(scanHandler)

	if err := router.Run(":8080"); err != nil {
		log.Fatalf("Could not start server: %v", err)
	}
}
