package main

import (
	"log"

	"github.com/prawo-i-piesc/backend/internal/api"
)

func main() {
	log.Println("Uruchamiam serwer API...")

	router := api.NewRouter()

	if err := router.Run(":8080"); err != nil {
		log.Fatalf("Nie można uruchomić serwera: %v", err)
	}
}
