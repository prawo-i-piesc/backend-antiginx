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
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/prawo-i-piesc/backend/internal/api"
	"github.com/prawo-i-piesc/backend/internal/auth"
	"github.com/prawo-i-piesc/backend/internal/config"
	"github.com/prawo-i-piesc/backend/internal/handlers"
	"github.com/prawo-i-piesc/backend/internal/models"
	"github.com/prawo-i-piesc/backend/internal/results"
	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var errBrokerClosed = errors.New("kanał RabbitMQ jest zamknięty")

// main initializes and starts the backend-antiginx API server.
//
// It performs the following steps:
//  1. Loads environment variables from .env file (optional)
//  2. Establishes connection to PostgreSQL database
//  3. Runs database migrations for Scan and ScanResult models
//  4. Connects to RabbitMQ and declares scan_queue, scan_dlq and the results queue
//  5. Starts the results queue consumers
//  6. Initializes HTTP handlers and starts the server on port 4000
//
// The function will terminate with a fatal error if any critical
// initialization step fails (database connection, RabbitMQ connection, etc.)
func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("Info: Nie znaleziono pliku .env, używam zmiennych środowiskowych")
	}
	log.Println("Uruchamiam serwer API...")

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Nieprawidłowa konfiguracja: %v", err)
	}

	db, err := gorm.Open(postgres.Open(cfg.DatabaseURL), &gorm.Config{TranslateError: true})
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

	if err := db.AutoMigrate(&models.User{}, &models.PremiumScan{}, &models.Scan{}, &models.ScanResult{}, &models.RecoveryCode{}, &models.Session{}, &models.OAuthAccount{}, &models.WebAuthnCredential{}, &models.EmailVerificationToken{}, &models.PasswordResetToken{}); err != nil {
		log.Fatalf("Nie udało się wykonać migracji: %v", err)
	}

	conn, err := amqp.Dial(cfg.RabbitMQURL)
	if err != nil {
		log.Fatalf("Nie udało się połączyć z RabbitMQ: %v", err)
	}

	defer func() {
		if err := conn.Close(); err != nil {
			log.Printf("Błąd podczas zamykania połączenia z RabbitMQ: %v", err)
		} else {
			log.Println("Połączenie z RabbitMQ zostało poprawnie zamknięte.")
		}
	}()

	ch, err := conn.Channel()
	if err != nil {
		log.Fatalf("Nie udało się otworzyć kanału: %v", err)
	}

	defer func() {
		if err := ch.Close(); err != nil {
			log.Printf("Błąd podczas zamykania kanału RabbitMQ: %v", err)
		}
	}()

	if err := declareTopology(ch, cfg.ResultsQueue); err != nil {
		log.Fatalf("Nie udało się skonfigurować kolejek RabbitMQ: %v", err)
	}

	// Tryb potwierdzeń: bez niego handler nie zobaczy, że scan_queue odrzuciła
	// zadanie nackiem.
	if err := ch.Confirm(false); err != nil {
		log.Fatalf("Nie udało się włączyć potwierdzeń publikacji: %v", err)
	}

	log.Println("RabbitMQ queues successfully configured")

	scanHandler := handlers.NewScanHandler(ch, db)
	authHandler, err := handlers.NewAuthHandler(db, cfg)
	if err != nil {
		log.Fatalf("Nie udało się zainicjalizować obsługi uwierzytelniania: %v", err)
	}
	adminHandler := handlers.NewAdminHandler(db, auth.NewSessionService(db, cfg.RefreshTokenTTL))

	probes := []api.Probe{
		{Name: "db", Check: sqlDB.PingContext},
		{Name: "rabbitmq", Check: func(context.Context) error {
			if ch.IsClosed() {
				return errBrokerClosed
			}
			return nil
		}},
	}

	srv := &http.Server{
		Addr:              ":4000",
		Handler:           api.NewRouter(scanHandler, authHandler, adminHandler, cfg, probes),
		MaxHeaderBytes:    64 << 10,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	consumer := results.NewConsumer(conn, results.NewStore(db), cfg.ResultsQueue, cfg.ResultConsumers, cfg.ResultBatchSize)
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		if err := consumer.Run(ctx); err != nil {
			// Bez odbiorców wyniki tylko by się zbierały; zamykamy proces,
			// żeby orkiestrator postawił go od nowa z nowym połączeniem.
			log.Printf("Odbiór wyników zatrzymany: %v", err)
			stop()
		}
	}()
	log.Printf("Odbieram wyniki z %s (%d odbiorców, paczki po %d)", cfg.ResultsQueue, cfg.ResultConsumers, cfg.ResultBatchSize)

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Could not start server: %v", err)
		}
	}()
	log.Println("Serwer nasłuchuje na :4000")

	<-ctx.Done()
	stop()
	log.Println("Otrzymano sygnał zamknięcia, domykam serwer...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Serwer nie zamknął się w wyznaczonym czasie: %v", err)
	}
	<-consumerDone
}

// declareTopology deklaruje kolejki, z których korzysta daemon. Daemon sam
// niczego nie deklaruje, więc ich kształt jest ustalany tylko tutaj.
//
// scan_queue jest kolejką quorum: po x-delivery-limit nieudanych podejściach
// daemona zadanie trafia przez scan_dlx do scan_dlq. Wyniki daemon publikuje
// przez domyślny exchange z routing key równym nazwie kolejki wynikowej.
func declareTopology(ch *amqp.Channel, resultsQueue string) error {
	if err := ch.ExchangeDeclare("main_exchange", "direct", true, false, false, false, nil); err != nil {
		return fmt.Errorf("main_exchange: %w", err)
	}
	if err := ch.ExchangeDeclare("scan_dlx", "fanout", true, false, false, false, nil); err != nil {
		return fmt.Errorf("scan_dlx: %w", err)
	}

	scanQueueArgs := amqp.Table{
		"x-queue-type":           "quorum",
		"x-delivery-limit":       int32(3),
		"x-dead-letter-exchange": "scan_dlx",
		"x-dead-letter-strategy": "at-least-once",
		"x-overflow":             "reject-publish", // wymagane przez at-least-once
	}
	if _, err := ch.QueueDeclare("scan_queue", true, false, false, false, scanQueueArgs); err != nil {
		return fmt.Errorf("scan_queue: %w", err)
	}
	if err := ch.QueueBind("scan_queue", "scan_key", "main_exchange", false, nil); err != nil {
		return fmt.Errorf("bind scan_queue: %w", err)
	}

	if _, err := ch.QueueDeclare("scan_dlq", true, false, false, false, amqp.Table{"x-queue-type": "quorum"}); err != nil {
		return fmt.Errorf("scan_dlq: %w", err)
	}
	if err := ch.QueueBind("scan_dlq", "", "scan_dlx", false, nil); err != nil {
		return fmt.Errorf("bind scan_dlq: %w", err)
	}

	if _, err := ch.QueueDeclare(resultsQueue, true, false, false, false, nil); err != nil {
		return fmt.Errorf("%s: %w", resultsQueue, err)
	}
	return nil
}
