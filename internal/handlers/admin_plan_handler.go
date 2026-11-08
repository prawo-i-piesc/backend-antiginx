package handlers

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/billing"
	"github.com/prawo-i-piesc/backend/internal/httpx"
	"github.com/prawo-i-piesc/backend/internal/models"
)

// adminPlan to plan konta w panelu admina: skąd ma Pro, jeśli ma, i czy
// administrator może mu je nadać.
type adminPlan struct {
	Plan string `json:"plan"`
	// Subscription to odbicie Stripe; null, gdy konto nigdy nie zaczęło płatności.
	Subscription *adminSubscription `json:"subscription"`
	// Grant to obowiązujące nadanie od administratora; wygasłe się nie liczy.
	Grant *adminGrant `json:"grant"`
	// CanGrant: płatna subskrypcja nie trwa, według ostatniej synchronizacji.
	// Nadanie i tak sprawdza to jeszcze raz w Stripe.
	CanGrant bool `json:"can_grant"`
}

type adminSubscription struct {
	Status           string     `json:"status"`
	CurrentPeriodEnd *time.Time `json:"current_period_end"`
	CancelAt         *time.Time `json:"cancel_at"`
	CustomerID       string     `json:"stripe_customer_id"`
	SyncedAt         time.Time  `json:"synced_at"`
}

type adminGrant struct {
	GrantedAt time.Time  `json:"granted_at"`
	ExpiresAt *time.Time `json:"expires_at"`
	GrantedBy uuid.UUID  `json:"granted_by"`
	// GrantedByEmail jest null, gdy konta administratora już nie ma.
	GrantedByEmail *string `json:"granted_by_email"`
}

type adminGrantRequest struct {
	// Days to długość nadania w dniach; 0 nadaje bezterminowo.
	Days *int `json:"days" binding:"required,min=0,max=3650"`
}

func (h *AdminHandler) planOf(userID uuid.UUID) (adminPlan, error) {
	sub, err := h.billing.Subscription(userID)
	if err != nil {
		return adminPlan{}, err
	}
	grant, err := billing.ActiveGrant(h.db, userID, time.Now())
	if err != nil {
		return adminPlan{}, err
	}

	plan := adminPlan{Plan: sub.Plan(), CanGrant: sub.Plan() == models.PlanFree}
	if sub != nil {
		plan.Subscription = &adminSubscription{
			Status:           sub.Status,
			CurrentPeriodEnd: sub.CurrentPeriodEnd,
			CancelAt:         sub.CancelAt,
			CustomerID:       sub.StripeCustomerID,
			SyncedAt:         sub.UpdatedAt,
		}
	}
	if grant != nil {
		plan.Plan = models.PlanPro
		plan.Grant = &adminGrant{GrantedAt: grant.GrantedAt, ExpiresAt: grant.ExpiresAt, GrantedBy: grant.GrantedBy}
		var emails []string
		if err := h.db.Model(&models.User{}).Where("id = ?", grant.GrantedBy).Limit(1).Pluck("email", &emails).Error; err != nil {
			return adminPlan{}, err
		}
		if len(emails) == 1 {
			plan.Grant.GrantedByEmail = &emails[0]
		}
	}
	return plan, nil
}

// proUsers mówi, które z kont mają Pro: z trwającej subskrypcji albo z
// nadania. Dwa zapytania na całą stronę listy, nie po jednym na konto.
func (h *AdminHandler) proUsers(ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	pro := make(map[uuid.UUID]bool)
	if len(ids) == 0 {
		return pro, nil
	}
	var paying, granted []uuid.UUID
	if err := h.db.Model(&models.Subscription{}).
		Where("user_id IN ? AND status IN ?", ids, models.LiveSubscriptionStatuses).
		Pluck("user_id", &paying).Error; err != nil {
		return nil, err
	}
	if err := h.db.Model(&models.PlanGrant{}).
		Where("user_id IN ? AND (expires_at IS NULL OR expires_at > ?)", ids, time.Now()).
		Pluck("user_id", &granted).Error; err != nil {
		return nil, err
	}
	for _, id := range append(paying, granted...) {
		pro[id] = true
	}
	return pro, nil
}

// HandleGrantPro nadaje kontu Pro na podaną liczbę dni albo bezterminowo.
// Odmawia kontu, które płaci za Pro w Stripe. Własne konto też można: to nie
// odbiera nikomu dostępu, a ułatwia sprawdzenie funkcji Pro.
func (h *AdminHandler) HandleGrantPro(c *gin.Context) {
	adminID, ok := currentUserUUID(c)
	if !ok {
		return
	}

	var req adminGrantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.FailValidation(c, err)
		return
	}

	var user models.User
	if !h.loadUserParam(c, &user) {
		return
	}

	if _, err := h.billing.Grant(c.Request.Context(), user.ID, adminID, *req.Days); err != nil {
		failBilling(c, "Grant", err)
		return
	}
	auditAdmin(adminID, fmt.Sprintf("plan_grant:%dd", *req.Days), user.ID)
	h.respondPlan(c, user.ID)
}

// HandleRevokePro odbiera nadanie Pro. Subskrypcji w Stripe nie rusza: tę
// anuluje właściciel w panelu klienta.
func (h *AdminHandler) HandleRevokePro(c *gin.Context) {
	adminID, ok := currentUserUUID(c)
	if !ok {
		return
	}

	var user models.User
	if !h.loadUserParam(c, &user) {
		return
	}

	if err := h.billing.Revoke(user.ID); err != nil {
		log.Printf("AdminRevokePro: nie udało się odebrać nadania %s: %v", user.ID, err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}
	auditAdmin(adminID, "plan_revoke", user.ID)
	h.respondPlan(c, user.ID)
}

func (h *AdminHandler) respondPlan(c *gin.Context, userID uuid.UUID) {
	plan, err := h.planOf(userID)
	if err != nil {
		log.Printf("AdminPlan: nie udało się odczytać planu %s: %v", userID, err)
		httpx.Fail(c, httpx.CodeInternal)
		return
	}
	c.JSON(http.StatusOK, plan)
}
