package handlers

import (
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/httpx"
	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/gorm"
)

const adminPageDefault = 25

type adminPage struct {
	Limit  int `form:"limit" json:"limit" binding:"omitempty,min=1,max=100"`
	Offset int `form:"offset" json:"offset" binding:"omitempty,min=0"`
}

func (p *adminPage) normalize() {
	if p.Limit == 0 {
		p.Limit = adminPageDefault
	}
}

type adminUsersQuery struct {
	Q string `form:"q" json:"q" binding:"max=320"`
	adminPage
}

type adminScansQuery struct {
	Type   string `form:"type" json:"type" binding:"omitempty,oneof=free premium"`
	Status string `form:"status" json:"status" binding:"omitempty,oneof=PENDING RUNNING COMPLETED FAILED"`
	adminPage
}

type adminRoleRequest struct {
	Role string `json:"role" binding:"required,oneof=user admin"`
}

type adminUserSummary struct {
	ID            uuid.UUID `json:"id"`
	Email         string    `json:"email"`
	FullName      string    `json:"full_name"`
	Role          string    `json:"role"`
	CreatedAt     time.Time `json:"created_at"`
	EmailVerified bool      `json:"email_verified"`
	TOTPEnabled   bool      `json:"totp_enabled"`
}

func summarizeUser(user *models.User) adminUserSummary {
	return adminUserSummary{
		ID:            user.ID,
		Email:         user.Email,
		FullName:      user.FullName,
		Role:          user.Role,
		CreatedAt:     user.CreatedAt,
		EmailVerified: user.EmailVerified,
		TOTPEnabled:   user.TOTPEnabled(),
	}
}

type adminScanRow struct {
	ID        uuid.UUID  `json:"id"`
	TargetURL string     `json:"target_url"`
	Status    string     `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	UserID    *uuid.UUID `json:"user_id,omitempty"`
	Type      string     `json:"type"`
}

// auditAdmin zostawia jedną linię na każdą zapisującą operację administratora.
// Tabeli audytu nie ma — linia w logu wystarcza, dopóki nie trzeba w niej
// szukać po fakcie.
func auditAdmin(adminID uuid.UUID, action string, target uuid.UUID) {
	log.Printf("admin=%s action=%s target=%s", adminID, action, target)
}

func (h *AdminHandler) HandleListUsers(c *gin.Context) {
	var query adminUsersQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		httpx.FailValidation(c, err)
		return
	}
	query.normalize()

	filtered := func() *gorm.DB {
		db := h.db.Model(&models.User{})
		if q := strings.TrimSpace(query.Q); q != "" {
			pattern := "%" + escapeLike(strings.ToLower(q)) + "%"
			db = db.Where(`(lower(email) LIKE ? ESCAPE '\' OR lower(full_name) LIKE ? ESCAPE '\')`, pattern, pattern)
		}
		return db
	}

	var total int64
	if err := filtered().Count(&total).Error; err != nil {
		log.Printf("AdminUsers: nie udało się policzyć kont: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	var users []models.User
	if err := filtered().
		Order("created_at DESC").
		Limit(query.Limit).
		Offset(query.Offset).
		Find(&users).Error; err != nil {
		log.Printf("AdminUsers: nie udało się pobrać kont: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	summaries := make([]adminUserSummary, 0, len(users))
	for i := range users {
		summaries = append(summaries, summarizeUser(&users[i]))
	}

	c.JSON(http.StatusOK, gin.H{
		"users":  summaries,
		"total":  total,
		"limit":  query.Limit,
		"offset": query.Offset,
	})
}

func (h *AdminHandler) HandleGetUser(c *gin.Context) {
	var user models.User
	if !h.loadUserParam(c, &user) {
		return
	}

	now := time.Now()
	providers := []string{}
	var activeSessions, passkeys, recoveryCodes, premiumScans int64

	err := errors.Join(
		h.db.Model(&models.Session{}).
			Where("user_id = ? AND revoked_at IS NULL AND expires_at > ?", user.ID, now).
			Count(&activeSessions).Error,
		h.db.Model(&models.OAuthAccount{}).
			Where("user_id = ?", user.ID).
			Order("provider").
			Pluck("provider", &providers).Error,
		h.db.Model(&models.WebAuthnCredential{}).
			Where("user_id = ?", user.ID).
			Count(&passkeys).Error,
		h.db.Model(&models.RecoveryCode{}).
			Where("user_id = ? AND used_at IS NULL", user.ID).
			Count(&recoveryCodes).Error,
		h.db.Model(&models.PremiumScan{}).
			Where("user_id = ?", user.ID).
			Count(&premiumScans).Error,
	)
	if err != nil {
		log.Printf("AdminUser: nie udało się zebrać danych konta %s: %v", user.ID, err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"user":                     summarizeUser(&user),
		"password_set":             user.HasPassword(),
		"passkey_mode":             user.EffectivePasskeyMode(),
		"active_sessions":          activeSessions,
		"providers":                providers,
		"passkeys":                 passkeys,
		"recovery_codes_remaining": recoveryCodes,
		"premium_scans":            premiumScans,
	})
}

func (h *AdminHandler) HandleSetUserRole(c *gin.Context) {
	adminID, ok := currentUserUUID(c)
	if !ok {
		return
	}

	var req adminRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.FailValidation(c, err)
		return
	}

	var user models.User
	if !h.loadUserParam(c, &user) {
		return
	}

	// Własnej roli administrator nie zmienia: zdjęcie jej sobie to najprostszy
	// sposób na zostawienie systemu bez nikogo, kto mógłby to odkręcić.
	if user.ID == adminID {
		httpx.Fail(c, httpx.CodeAdminSelfAction)
		return
	}

	if user.Role != req.Role {
		update := h.db.Model(&models.User{}).Where("id = ?", user.ID)
		if user.Role == models.UserRoleAdmin {
			// Warunek w tym samym zapytaniu, a nie osobny COUNT przed nim:
			// dwóch adminów odbierających sobie nawzajem rolę nie może
			// jednocześnie zobaczyć, że „jest jeszcze ktoś”.
			update = update.Where("(SELECT count(*) FROM users WHERE role = ?) > 1", models.UserRoleAdmin)
		}

		result := update.Update("role", req.Role)
		if result.Error != nil {
			log.Printf("AdminRole: nie udało się zmienić roli %s: %v", user.ID, result.Error)
			httpx.Fail(c, httpx.CodeInternal)
			return
		}
		if result.RowsAffected == 0 {
			httpx.Fail(c, httpx.CodeLastAdmin)
			return
		}

		auditAdmin(adminID, "role:"+req.Role, user.ID)
		user.Role = req.Role
	}

	c.JSON(http.StatusOK, summarizeUser(&user))
}

func (h *AdminHandler) HandleLogoutUser(c *gin.Context) {
	adminID, ok := currentUserUUID(c)
	if !ok {
		return
	}

	var user models.User
	if !h.loadUserParam(c, &user) {
		return
	}

	if err := h.sessions.RevokeAll(user.ID); err != nil {
		log.Printf("AdminLogout: nie udało się unieważnić sesji %s: %v", user.ID, err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	auditAdmin(adminID, "logout", user.ID)
	c.Status(http.StatusNoContent)
}

func (h *AdminHandler) HandleDeleteUser(c *gin.Context) {
	adminID, ok := currentUserUUID(c)
	if !ok {
		return
	}

	var user models.User
	if !h.loadUserParam(c, &user) {
		return
	}

	// Własne konto administrator usuwa przez DELETE /api/auth/account, które
	// wymaga hasła i drugiego składnika. Tutaj tej ochrony nie ma.
	if user.ID == adminID {
		httpx.Fail(c, httpx.CodeAdminSelfAction)
		return
	}

	if err := h.billing.CloseAccount(c.Request.Context(), user.ID); err != nil {
		log.Printf("AdminDeleteUser: nie udało się zamknąć płatności konta %s: %v", user.ID, err)
		httpx.Fail(c, httpx.CodeBillingUnavailable)
		return
	}

	if err := deleteUserCascade(h.db, user.ID); err != nil {
		log.Printf("AdminDeleteUser: nie udało się usunąć konta %s: %v", user.ID, err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	auditAdmin(adminID, "delete_user", user.ID)
	c.Status(http.StatusNoContent)
}

func (h *AdminHandler) HandleListScans(c *gin.Context) {
	var query adminScansQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		httpx.FailValidation(c, err)
		return
	}
	query.normalize()

	var branches []string
	var args []any
	branch := func(sel, table string) {
		sql := sel + " FROM " + table
		if query.Status != "" {
			sql += " WHERE status = ?"
			args = append(args, query.Status)
		}
		branches = append(branches, sql)
	}
	if query.Type != "premium" {
		branch("SELECT id, target_url, status, created_at, NULL AS user_id, 'free' AS type", "scans")
	}
	if query.Type != "free" {
		branch("SELECT id, target_url, status, created_at, user_id, 'premium' AS type", "premium_scans")
	}
	union := "(" + strings.Join(branches, " UNION ALL ") + ") AS s"

	var total int64
	if err := h.db.Raw("SELECT count(*) FROM "+union, args...).Scan(&total).Error; err != nil {
		log.Printf("AdminScans: nie udało się policzyć skanów: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	scans := []adminScanRow{}
	pageArgs := append(append([]any{}, args...), query.Limit, query.Offset)
	if err := h.db.Raw("SELECT * FROM "+union+" ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?", pageArgs...).
		Scan(&scans).Error; err != nil {
		log.Printf("AdminScans: nie udało się pobrać skanów: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"scans":  scans,
		"total":  total,
		"limit":  query.Limit,
		"offset": query.Offset,
	})
}

func (h *AdminHandler) HandleDeleteScan(c *gin.Context) {
	adminID, ok := currentUserUUID(c)
	if !ok {
		return
	}

	scanID, ok := scanIDParam(c)
	if !ok {
		return
	}

	scan, err := findScan(h.db, scanID)
	if err != nil {
		log.Printf("AdminDeleteScan: nie udało się odczytać skanu %s: %v", scanID, err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}
	if scan == nil {
		httpx.Fail(c, httpx.CodeScanNotFound)
		return
	}

	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("scan_id = ?", scanID).Delete(&models.ScanResult{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", scanID).Delete(scan.model()).Error
	})
	if err != nil {
		log.Printf("AdminDeleteScan: nie udało się usunąć skanu %s: %v", scanID, err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	auditAdmin(adminID, "delete_scan", scanID)
	c.Status(http.StatusNoContent)
}

func (h *AdminHandler) loadUserParam(c *gin.Context, user *models.User) bool {
	userID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.FailWithFields(c, httpx.CodeValidationFailed, map[string]string{"id": "invalid"})
		return false
	}

	if err := h.db.Where("id = ?", userID).First(user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			httpx.Fail(c, httpx.CodeUserNotFound)
			return false
		}
		log.Printf("Admin: nie udało się pobrać konta %s: %v", userID, err)
		httpx.Fail(c, httpx.CodeInternal)
		return false
	}
	return true
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
