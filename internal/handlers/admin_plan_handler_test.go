package handlers

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/prawo-i-piesc/backend/internal/httpx"
	"github.com/prawo-i-piesc/backend/internal/models"
)

type planBody struct {
	Plan         string `json:"plan"`
	CanGrant     bool   `json:"can_grant"`
	Subscription *struct {
		Status     string `json:"status"`
		CustomerID string `json:"stripe_customer_id"`
	} `json:"subscription"`
	Grant *struct {
		ExpiresAt      *time.Time `json:"expires_at"`
		GrantedByEmail *string    `json:"granted_by_email"`
	} `json:"grant"`
}

func decodePlan(t *testing.T, body []byte) planBody {
	t.Helper()
	var plan planBody
	if err := json.Unmarshal(body, &plan); err != nil {
		t.Fatalf("plan: %v in %s", err, body)
	}
	return plan
}

func TestAdminGrantsAndRevokesPro(t *testing.T) {
	db := testDB(t)
	adminID := seedUser(t, db, "admin@example.com", "Admin", models.UserRoleAdmin, time.Now())
	userID := seedUser(t, db, "user@example.com", "User", models.UserRoleUser, time.Now())
	r := adminRouter(db, adminID)

	w := send(t, r, http.MethodPut, "/admin/users/"+userID.String()+"/plan-grant", `{"days":30}`)
	if w.Code != http.StatusOK {
		t.Fatalf("grant: %d %s", w.Code, w.Body)
	}
	plan := decodePlan(t, w.Body.Bytes())
	if plan.Plan != models.PlanPro || plan.Grant == nil || plan.Grant.ExpiresAt == nil ||
		plan.Grant.GrantedByEmail == nil || *plan.Grant.GrantedByEmail != "admin@example.com" {
		t.Fatalf("plan after the grant = %+v", plan)
	}

	// Lista pokazuje plan każdego konta.
	var list struct {
		Users []struct {
			Email string `json:"email"`
			Plan  string `json:"plan"`
		} `json:"users"`
	}
	_ = json.Unmarshal(get(t, r, "/admin/users").Body.Bytes(), &list)
	for _, u := range list.Users {
		want := models.PlanFree
		if u.Email == "user@example.com" {
			want = models.PlanPro
		}
		if u.Plan != want {
			t.Errorf("%s listed as %s, want %s", u.Email, u.Plan, want)
		}
	}

	w = send(t, r, http.MethodDelete, "/admin/users/"+userID.String()+"/plan-grant", "")
	if plan := decodePlan(t, w.Body.Bytes()); w.Code != http.StatusOK || plan.Plan != models.PlanFree || plan.Grant != nil || !plan.CanGrant {
		t.Fatalf("revoke: %d %+v", w.Code, plan)
	}
}

func TestAdminCannotGrantProToAPayingAccount(t *testing.T) {
	db := testDB(t)
	adminID := seedUser(t, db, "admin@example.com", "Admin", models.UserRoleAdmin, time.Now())
	userID := seedUser(t, db, "user@example.com", "User", models.UserRoleUser, time.Now())
	if err := db.Create(&models.Subscription{UserID: userID, StripeCustomerID: "cus_1", Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	r := adminRouter(db, adminID)

	details := get(t, r, "/admin/users/"+userID.String())
	var body struct {
		Plan planBody `json:"plan"`
	}
	_ = json.Unmarshal(details.Body.Bytes(), &body)
	if body.Plan.Plan != models.PlanPro || body.Plan.CanGrant || body.Plan.Subscription == nil || body.Plan.Subscription.CustomerID != "cus_1" {
		t.Fatalf("details of a paying account: %+v", body.Plan)
	}

	w := send(t, r, http.MethodPut, "/admin/users/"+userID.String()+"/plan-grant", `{"days":0}`)
	if w.Code != http.StatusConflict || errorCode(t, w.Body.Bytes()) != httpx.CodeSubscriptionActive {
		t.Fatalf("grant to a paying account: %d %s", w.Code, w.Body)
	}
}

func TestAdminGrantValidatesItsInput(t *testing.T) {
	db := testDB(t)
	adminID := seedUser(t, db, "admin@example.com", "Admin", models.UserRoleAdmin, time.Now())
	userID := seedUser(t, db, "user@example.com", "User", models.UserRoleUser, time.Now())
	r := adminRouter(db, adminID)

	for _, body := range []string{`{}`, `{"days":-1}`, `{"days":3651}`, `{"days":"30"}`} {
		w := send(t, r, http.MethodPut, "/admin/users/"+userID.String()+"/plan-grant", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", body, w.Code)
		}
	}
	w := send(t, r, http.MethodPut, "/admin/users/"+newID(t).String()+"/plan-grant", `{"days":30}`)
	if w.Code != http.StatusNotFound || errorCode(t, w.Body.Bytes()) != httpx.CodeUserNotFound {
		t.Errorf("unknown account: %d %s", w.Code, w.Body)
	}

	// Własne konto też można: to nikomu nie odbiera dostępu.
	if w := send(t, r, http.MethodPut, "/admin/users/"+adminID.String()+"/plan-grant", `{"days":0}`); w.Code != http.StatusOK {
		t.Errorf("grant to oneself: %d %s", w.Code, w.Body)
	}
}

func TestDeletingAnAccountRemovesItsGrant(t *testing.T) {
	db := testDB(t)
	adminID := seedUser(t, db, "admin@example.com", "Admin", models.UserRoleAdmin, time.Now())
	userID := seedUser(t, db, "user@example.com", "User", models.UserRoleUser, time.Now())
	r := adminRouter(db, adminID)

	send(t, r, http.MethodPut, "/admin/users/"+userID.String()+"/plan-grant", `{"days":0}`)
	if w := send(t, r, http.MethodDelete, "/admin/users/"+userID.String(), ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	var count int64
	db.Model(&models.PlanGrant{}).Count(&count)
	if count != 0 {
		t.Errorf("%d grants left after deleting the account", count)
	}
}
