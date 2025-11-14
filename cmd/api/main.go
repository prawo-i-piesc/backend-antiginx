package main

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

func main() {
	log.Println("Uruchamiam serwer API...")

	r := gin.Default()

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status": "OK",
		})
	})

	if err := r.Run(":8080"); err != nil {
		log.Fatalf("Nie można uruchomić serwera: %v", err)
	}
}
