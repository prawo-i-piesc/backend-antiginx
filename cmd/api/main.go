package main

import (
	"log"

	"context"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"
	"github.com/prawo-i-piesc/backend/internal/api"
	"github.com/prawo-i-piesc/backend/internal/handlers"
	amqp "github.com/rabbitmq/amqp091-go"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Fatalf("Info: Nie znaleziono pliku .env")
	}
	log.Println("Uruchamiam serwer API...")
	conn_postgresql, err := pgx.Connect(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("Nie udało się połączyć z bazą danych: %v", err)
	}
	defer conn_postgresql.Close(context.Background())
	var version string
	if err := conn_postgresql.QueryRow(context.Background(), "SELECT version()").Scan(&version); err != nil {
		log.Fatalf("Nie udało się wykonać zapytania: %v", err)
	}

	log.Println("Połączono z:", version)

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

	scanHandler := handlers.NewScanHandler(ch)

	router := api.NewRouter(scanHandler)

	if err := router.Run(":8080"); err != nil {
		log.Fatalf("Could not start server: %v", err)
	}
}
