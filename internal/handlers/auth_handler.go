package handlers

import (
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/models"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type AuthHandler struct {
	db *gorm.DB
}

type RegisterRequest struct {
	FullName string `json:"full_name" binding:"required"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8"`
}

type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8"`
}

func NewAuthHandler(db *gorm.DB) *AuthHandler {
	return &AuthHandler{
		db: db,
	}
}

func (h *AuthHandler) Register(c *gin.Context) {
	var req RegisterRequest
	var existingUser models.User
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("Binding error: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resultEmailCheck := h.db.Where("email = ?", req.Email).First(&existingUser)

	if resultEmailCheck.Error == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "User with this email already exists"})
		return
	}

	if resultEmailCheck.Error != gorm.ErrRecordNotFound {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}

	newUserID, err := uuid.NewV7()
	if err != nil {
		log.Printf("Failed to generate UUIDv7: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	passwordBytes := []byte(req.Password)

	HashedPassword, err := bcrypt.GenerateFromPassword(passwordBytes, 12)
	if err != nil {
		log.Printf("Failed to encrypt provided password: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	newUser := models.User{
		ID:        newUserID,
		FullName:  req.FullName,
		Email:     req.Email,
		CreatedAt: time.Now(),
		Password:  HashedPassword,
	}

	resultCreateNewUser := h.db.Create(&newUser)
	if resultCreateNewUser.Error != nil {
		log.Printf("Failed to create new user in DB: %v", resultCreateNewUser.Error)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create new user"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"message": "User registered successfully",
	})
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	var existingUser models.User
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("Binding error: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result := h.db.Where("email = ?", req.Email).First(&existingUser)

	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid email or password"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}

	err := bcrypt.CompareHashAndPassword(existingUser.Password, []byte(req.Password))

	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid email or password"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"token": "tutaj_bedzie_jwt"})

}
