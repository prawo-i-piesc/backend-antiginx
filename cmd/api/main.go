package main

import (
	"log"

	"github.com/prawo-i-piesc/backend/internal/api"
	"github.com/prawo-i-piesc/backend/internal/handlers"
	amqp "github.com/rabbitmq/amqp091-go"
)

func main() {
	log.Println("Starting API server...")

	conn, err := amqp.Dial("amqp://guest:guest@localhost:5672/")
	if err != nil {
		log.Fatalf("Could not connect to RabbitMQ: %v", err)
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		log.Fatalf("Could not open channel: %v", err)
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
