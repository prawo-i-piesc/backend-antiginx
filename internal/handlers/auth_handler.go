package handlers

import (
	"errors"
	"log"
	"net/http"

	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/auth"
	"github.com/prawo-i-piesc/backend/internal/config"
	"github.com/prawo-i-piesc/backend/internal/httpx"
	"github.com/prawo-i-piesc/backend/internal/models"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const bcryptCost = 12

type UpdateNameRequest struct {
	FullName string `json:"full_name" binding:"required,min=6"`
}

type UpdateEmailRequest struct {
	Email string `json:"email" binding:"required,email"`
}

type UpdatePasswordRequest struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=8"`
}

type AuthHandler struct {
	db  *gorm.DB
	cfg *config.Config
}

type RegisterRequest struct {
	FullName string `json:"full_name" binding:"required"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8"`
}

type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

func NewAuthHandler(db *gorm.DB, cfg *config.Config) *AuthHandler {
	return &AuthHandler{
		db:  db,
		cfg: cfg,
	}
}

func (h *AuthHandler) DB() *gorm.DB {
	return h.db
}

func (h *AuthHandler) GenerateToken(userID string, role string) (string, error) {
	return auth.GenerateToken(h.cfg.JWTSecret, auth.TokenTypeAccess, userID, role, h.cfg.AccessTokenTTL)
}

func byEmail(db *gorm.DB, email string) *gorm.DB {
	return db.Where("lower(email) = ?", email)
}

func currentUserID(c *gin.Context) (string, bool) {
	userID, exists := c.Get("userID")
	if !exists {
		log.Printf("Handler: brak userID w kontekście — trasa poza RequireAuth")
		httpx.Fail(c, httpx.CodeInternal)
		return "", false
	}

	userIDStr, ok := userID.(string)
	if !ok || userIDStr == "" {
		log.Printf("Handler: userID w kontekście ma nieoczekiwany typ %T", userID)
		httpx.Fail(c, httpx.CodeInternal)
		return "", false
	}
	return userIDStr, true
}

func (h *AuthHandler) loadCurrentUser(c *gin.Context, user *models.User) bool {
	userID, ok := currentUserID(c)
	if !ok {
		return false
	}

	if err := h.db.Where("id = ?", userID).First(user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			httpx.Fail(c, httpx.CodeSessionExpired)
			return false
		}
		log.Printf("Nie udało się pobrać użytkownika %s: %v", userID, err)
		httpx.Fail(c, httpx.CodeInternal)
		return false
	}
	return true
}

func (h *AuthHandler) Register(c *gin.Context) {
	var req RegisterRequest
	var existingUser models.User
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.FailValidation(c, err)
		return
	}

	email := auth.NormalizeEmail(req.Email)

	resultEmailCheck := byEmail(h.db, email).First(&existingUser)

	if resultEmailCheck.Error == nil {
		httpx.Fail(c, httpx.CodeEmailTaken)
		return
	}

	if !errors.Is(resultEmailCheck.Error, gorm.ErrRecordNotFound) {
		log.Printf("Register: błąd bazy danych przy sprawdzaniu adresu e-mail: %v", resultEmailCheck.Error)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	newUserID, err := uuid.NewV7()
	if err != nil {
		log.Printf("Register: nie udało się wygenerować UUIDv7: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		log.Printf("Register: nie udało się zahaszować hasła: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	newUser := models.User{
		ID:        newUserID,
		FullName:  req.FullName,
		Email:     email,
		Role:      models.UserRoleUser,
		CreatedAt: time.Now(),
		Password:  hashedPassword,
	}

	if err := h.db.Create(&newUser).Error; err != nil {

		if errors.Is(err, gorm.ErrDuplicatedKey) {
			httpx.Fail(c, httpx.CodeEmailTaken)
			return
		}
		log.Printf("Register: nie udało się utworzyć użytkownika: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
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
		httpx.FailValidation(c, err)
		return
	}

	result := byEmail(h.db, auth.NormalizeEmail(req.Email)).First(&existingUser)

	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			httpx.Fail(c, httpx.CodeInvalidCredentials)
			return
		}
		log.Printf("Login: błąd bazy danych przy pobieraniu użytkownika: %v", result.Error)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	if err := bcrypt.CompareHashAndPassword(existingUser.Password, []byte(req.Password)); err != nil {
		httpx.Fail(c, httpx.CodeInvalidCredentials)
		return
	}

	token, err := h.GenerateToken(existingUser.ID.String(), existingUser.Role)
	if err != nil {
		log.Printf("Login: nie udało się wygenerować tokenu: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token":      token,
		"expires_in": int(h.cfg.AccessTokenTTL.Seconds()),
	})
}

func (h *AuthHandler) Me(c *gin.Context) {
	var user models.User
	if !h.loadCurrentUser(c, &user) {
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":        user.ID,
		"full_name": user.FullName,
		"email":     user.Email,
		"role":      user.Role,
	})
}

func (h *AuthHandler) HandleUpdateFullName(c *gin.Context) {
	var req UpdateNameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.FailValidation(c, err)
		return
	}

	var user models.User
	if !h.loadCurrentUser(c, &user) {
		return
	}

	user.FullName = req.FullName

	if err := h.db.Save(&user).Error; err != nil {
		log.Printf("UpdateFullName: nie udało się zapisać zmiany: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Name updated successfully"})
}

func (h *AuthHandler) HandleUpdateEmail(c *gin.Context) {
	var req UpdateEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.FailValidation(c, err)
		return
	}

	var user models.User
	if !h.loadCurrentUser(c, &user) {
		return
	}

	email := auth.NormalizeEmail(req.Email)

	var existingUser models.User
	err := byEmail(h.db, email).Where("id <> ?", user.ID).First(&existingUser).Error
	switch {
	case err == nil:
		httpx.Fail(c, httpx.CodeEmailTaken)
		return
	case !errors.Is(err, gorm.ErrRecordNotFound):
		log.Printf("UpdateEmail: błąd bazy danych przy sprawdzaniu adresu: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	user.Email = email

	if err := h.db.Save(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			httpx.Fail(c, httpx.CodeEmailTaken)
			return
		}
		log.Printf("UpdateEmail: nie udało się zapisać zmiany: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Email updated successfully"})
}

func (h *AuthHandler) HandleUpdatePassword(c *gin.Context) {
	var req UpdatePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.FailValidation(c, err)
		return
	}

	var user models.User
	if !h.loadCurrentUser(c, &user) {
		return
	}

	if err := bcrypt.CompareHashAndPassword(user.Password, []byte(req.OldPassword)); err != nil {
		httpx.Fail(c, httpx.CodeInvalidCredentials)
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcryptCost)
	if err != nil {
		log.Printf("UpdatePassword: nie udało się zahaszować hasła: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}
	user.Password = hashedPassword

	if err := h.db.Save(&user).Error; err != nil {
		log.Printf("UpdatePassword: nie udało się zapisać hasła: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Password updated successfully"})
}
