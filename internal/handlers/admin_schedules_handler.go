package handlers

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/httpx"
	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/gorm"
)

// adminSchedulesQuery to strona harmonogramów wszystkich kont. Fraza pasuje
// do adresu celu albo adresu e-mail właściciela.
type adminSchedulesQuery struct {
	Q      string `form:"q" json:"q" binding:"max=2048"`
	State  string `form:"state" json:"state" binding:"omitempty,oneof=active paused blocked"`
	UserID string `form:"user_id" json:"user_id" binding:"omitempty,uuid"`
	listPage
}

type adminBlockRequest struct {
	// Reason widzi użytkownik przy zablokowanym harmonogramie.
	Reason string `json:"reason" binding:"max=200"`
}

// HandleAdminList zwraca stronę harmonogramów wszystkich kont, od najnowszych.
func (h *ScheduleHandler) HandleAdminList(c *gin.Context) {
	var query adminSchedulesQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		httpx.FailValidation(c, err)
		return
	}
	query.normalize()

	ctx := c.Request.Context()
	filtered := func() *gorm.DB {
		db := h.db.WithContext(ctx).Table("schedules s").Joins("LEFT JOIN users u ON u.id = s.user_id")
		switch query.State {
		case "active":
			db = db.Where("s.enabled = ? AND s.blocked_at IS NULL", true)
		case "paused":
			db = db.Where("s.enabled = ? AND s.blocked_at IS NULL", false)
		case "blocked":
			db = db.Where("s.blocked_at IS NOT NULL")
		}
		if query.UserID != "" {
			// Walidator przepuścił tylko poprawny UUID.
			owner, _ := uuid.Parse(query.UserID)
			db = db.Where("s.user_id = ?", owner)
		}
		if q := strings.TrimSpace(query.Q); q != "" {
			like := "%" + escapeLike(strings.ToLower(q)) + "%"
			db = db.Where(`(lower(s.target_url) LIKE ? ESCAPE '\' OR lower(u.email) LIKE ? ESCAPE '\')`, like, like)
		}
		return db
	}

	var total int64
	if err := filtered().Count(&total).Error; err != nil {
		log.Printf("AdminSchedules: nie udało się policzyć harmonogramów: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	var found []struct {
		models.Schedule
		Email *string
	}
	if err := filtered().
		Select("s.*, u.email AS email").
		Order("s.created_at DESC, s.id DESC").
		Limit(query.Limit).
		Offset(query.Offset).
		Scan(&found).Error; err != nil {
		log.Printf("AdminSchedules: nie udało się pobrać harmonogramów: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	rows := make([]scheduleRow, len(found))
	for i := range found {
		rows[i] = newScheduleRow(&found[i].Schedule)
		owner := found[i].UserID
		rows[i].UserID = &owner
		rows[i].UserEmail = found[i].Email
	}
	if err := h.describeSchedules(ctx, rows); err != nil {
		log.Printf("AdminSchedules: nie udało się opisać harmonogramów: %v", err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"schedules": rows,
		"total":     total,
		"limit":     query.Limit,
		"offset":    query.Offset,
	})
}

// HandleAdminBlock blokuje harmonogram: scheduler go pomija, a użytkownik
// nie może go wznowić. Ponowna blokada zmienia tylko powód.
func (h *ScheduleHandler) HandleAdminBlock(c *gin.Context) {
	adminID, ok := currentUserUUID(c)
	if !ok {
		return
	}
	var req adminBlockRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.FailValidation(c, err)
		return
	}
	schedule, ok := h.adminFind(c)
	if !ok {
		return
	}

	updates := map[string]any{"block_reason": strings.TrimSpace(req.Reason), "blocked_by": adminID}
	if schedule.BlockedAt == nil {
		updates["blocked_at"] = h.now()
	}
	if err := h.db.WithContext(c.Request.Context()).Model(schedule).Updates(updates).Error; err != nil {
		log.Printf("AdminSchedules: nie udało się zablokować harmonogramu %s: %v", schedule.ID, err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}
	auditAdmin(adminID, "schedule.block", schedule.ID)
	h.adminRespond(c, schedule)
}

// HandleAdminUnblock zdejmuje blokadę. Harmonogram, który użytkownik miał
// włączony, rusza od najbliższego terminu.
func (h *ScheduleHandler) HandleAdminUnblock(c *gin.Context) {
	adminID, ok := currentUserUUID(c)
	if !ok {
		return
	}
	schedule, ok := h.adminFind(c)
	if !ok {
		return
	}
	next, ok := h.nextRun(c, schedule)
	if !ok {
		return
	}

	if err := h.db.WithContext(c.Request.Context()).Model(schedule).Updates(map[string]any{
		"blocked_at":   nil,
		"blocked_by":   nil,
		"block_reason": "",
		"next_run_at":  next,
	}).Error; err != nil {
		log.Printf("AdminSchedules: nie udało się odblokować harmonogramu %s: %v", schedule.ID, err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}
	auditAdmin(adminID, "schedule.unblock", schedule.ID)
	h.adminRespond(c, schedule)
}

func (h *ScheduleHandler) adminFind(c *gin.Context) (*models.Schedule, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.FailWithFields(c, httpx.CodeValidationFailed, map[string]string{"id": "invalid"})
		return nil, false
	}
	var schedule models.Schedule
	err = h.db.WithContext(c.Request.Context()).Take(&schedule, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		httpx.Fail(c, httpx.CodeScheduleNotFound)
		return nil, false
	}
	if err != nil {
		log.Printf("AdminSchedules: nie udało się pobrać harmonogramu %s: %v", id, err)
		httpx.Fail(c, httpx.CodeInternal)
		return nil, false
	}
	return &schedule, true
}

// adminRespond odsyła harmonogram w kształcie wiersza listy admina.
func (h *ScheduleHandler) adminRespond(c *gin.Context, schedule *models.Schedule) {
	ctx := c.Request.Context()
	if err := h.db.WithContext(ctx).Take(schedule, "id = ?", schedule.ID).Error; err != nil {
		log.Printf("AdminSchedules: nie udało się odczytać harmonogramu %s: %v", schedule.ID, err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}
	row := newScheduleRow(schedule)
	owner := schedule.UserID
	row.UserID = &owner
	var user models.User
	if err := h.db.WithContext(ctx).Select("email").Take(&user, "id = ?", owner).Error; err == nil {
		row.UserEmail = &user.Email
	}
	rows := []scheduleRow{row}
	if err := h.describeSchedules(ctx, rows); err != nil {
		log.Printf("AdminSchedules: nie udało się opisać harmonogramu %s: %v", schedule.ID, err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}
	c.JSON(http.StatusOK, rows[0])
}
