package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/auth"
	"github.com/prawo-i-piesc/backend/internal/httpx"
	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/gorm"
)

func adminRouter(db *gorm.DB, adminID uuid.UUID) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := NewAdminHandler(db, auth.NewSessionService(db, time.Hour))

	r := gin.New()
	admin := r.Group("/admin", func(c *gin.Context) {
		c.Set("userID", adminID.String())
		c.Next()
	})
	admin.GET("/users", h.HandleListUsers)
	admin.GET("/users/:id", h.HandleGetUser)
	admin.PATCH("/users/:id/role", h.HandleSetUserRole)
	admin.POST("/users/:id/logout", h.HandleLogoutUser)
	admin.DELETE("/users/:id", h.HandleDeleteUser)
	admin.GET("/scans", h.HandleListScans)
	admin.DELETE("/scans/:id", h.HandleDeleteScan)
	return r
}

func seedUser(t *testing.T, db *gorm.DB, email, name, role string, createdAt time.Time) uuid.UUID {
	t.Helper()
	id := newID(t)
	if err := db.Create(&models.User{ID: id, Email: email, FullName: name, Role: role, CreatedAt: createdAt}).Error; err != nil {
		t.Fatalf("zasianie konta: %v", err)
	}
	return id
}

func roleOf(t *testing.T, db *gorm.DB, id uuid.UUID) string {
	t.Helper()
	var user models.User
	if err := db.First(&user, "id = ?", id).Error; err != nil {
		t.Fatalf("odczyt konta: %v", err)
	}
	return user.Role
}

func TestAdminListUsersSearchesAndPaginates(t *testing.T) {
	db := testDB(t)
	base := time.Now().Add(-time.Hour)
	adminID := seedUser(t, db, "admin@example.com", "Admin", models.UserRoleAdmin, base)
	for i := range 5 {
		seedUser(t, db, fmt.Sprintf("jan%d@example.com", i), "Jan", models.UserRoleUser, base.Add(time.Duration(i+1)*time.Minute))
	}
	seedUser(t, db, "anna@example.com", "Anna 100%", models.UserRoleUser, base)

	r := adminRouter(db, adminID)

	w := get(t, r, "/admin/users?q=JAN&limit=2&offset=1")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	var page struct {
		Users  []adminUserSummary `json:"users"`
		Total  int64              `json:"total"`
		Limit  int                `json:"limit"`
		Offset int                `json:"offset"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if page.Total != 5 || len(page.Users) != 2 || page.Limit != 2 || page.Offset != 1 {
		t.Fatalf("total=%d len=%d limit=%d offset=%d, want 5/2/2/1", page.Total, len(page.Users), page.Limit, page.Offset)
	}
	if page.Users[0].Email != "jan3@example.com" {
		t.Errorf("pierwszy na stronie = %s, want jan3 (najnowsze pierwsze)", page.Users[0].Email)
	}

	// % w zapytaniu to zwykły znak, nie wildcard.
	w = get(t, r, "/admin/users?q=%25")
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if page.Total != 1 || page.Users[0].Email != "anna@example.com" {
		t.Errorf("wyszukanie %% zwróciło %d kont, want tylko Annę", page.Total)
	}
}

func TestAdminListUsersRejectsOversizedPages(t *testing.T) {
	db := testDB(t)
	r := adminRouter(db, seedUser(t, db, "admin@example.com", "Admin", models.UserRoleAdmin, time.Now()))

	for _, query := range []string{"limit=101", "offset=-1", "limit=abc"} {
		t.Run(query, func(t *testing.T) {
			w := get(t, r, "/admin/users?"+query)
			if w.Code != http.StatusBadRequest || errorCode(t, w.Body.Bytes()) != httpx.CodeValidationFailed {
				t.Errorf("got %d %s, want 400 VALIDATION_FAILED", w.Code, w.Body)
			}
		})
	}
}

func TestAdminGetUserReportsAccountState(t *testing.T) {
	db := testDB(t)
	adminID := seedUser(t, db, "admin@example.com", "Admin", models.UserRoleAdmin, time.Now())
	userID := seedAccount(t, db, "jan@example.com")

	w := get(t, adminRouter(db, adminID), "/admin/users/"+userID.String())
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	var body struct {
		ActiveSessions int64    `json:"active_sessions"`
		Providers      []string `json:"providers"`
		Passkeys       int64    `json:"passkeys"`
		PremiumScans   int64    `json:"premium_scans"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if body.ActiveSessions != 1 || len(body.Providers) != 1 || body.Passkeys != 1 || body.PremiumScans != 1 {
		t.Errorf("body = %+v, want po jednym z każdego", body)
	}

	w = get(t, adminRouter(db, adminID), "/admin/users/"+uuid.NewString())
	if w.Code != http.StatusNotFound || errorCode(t, w.Body.Bytes()) != httpx.CodeUserNotFound {
		t.Errorf("got %d %s, want 404 USER_NOT_FOUND", w.Code, w.Body)
	}
}

func TestAdminPromotesAndDemotesOtherUsers(t *testing.T) {
	db := testDB(t)
	adminID := seedUser(t, db, "admin@example.com", "Admin", models.UserRoleAdmin, time.Now())
	userID := seedUser(t, db, "jan@example.com", "Jan", models.UserRoleUser, time.Now())
	r := adminRouter(db, adminID)

	path := "/admin/users/" + userID.String() + "/role"
	if w := send(t, r, http.MethodPatch, path, `{"role":"admin"}`); w.Code != http.StatusOK {
		t.Fatalf("awans: status = %d, body = %s", w.Code, w.Body)
	}
	if got := roleOf(t, db, userID); got != models.UserRoleAdmin {
		t.Fatalf("rola po awansie = %s", got)
	}

	if w := send(t, r, http.MethodPatch, path, `{"role":"user"}`); w.Code != http.StatusOK {
		t.Fatalf("degradacja: status = %d, body = %s", w.Code, w.Body)
	}
	if got := roleOf(t, db, userID); got != models.UserRoleUser {
		t.Errorf("rola po degradacji = %s", got)
	}

	if w := send(t, r, http.MethodPatch, path, `{"role":"root"}`); w.Code != http.StatusBadRequest {
		t.Errorf("nieznana rola: status = %d, want 400", w.Code)
	}
}

func TestAdminCannotChangeOwnRole(t *testing.T) {
	db := testDB(t)
	adminID := seedUser(t, db, "admin@example.com", "Admin", models.UserRoleAdmin, time.Now())
	seedUser(t, db, "drugi@example.com", "Drugi", models.UserRoleAdmin, time.Now())

	w := send(t, adminRouter(db, adminID), http.MethodPatch, "/admin/users/"+adminID.String()+"/role", `{"role":"user"}`)
	if w.Code != http.StatusConflict || errorCode(t, w.Body.Bytes()) != httpx.CodeAdminSelfAction {
		t.Errorf("got %d %s, want 409 ADMIN_SELF_ACTION", w.Code, w.Body)
	}
	if got := roleOf(t, db, adminID); got != models.UserRoleAdmin {
		t.Errorf("rola = %s, admin odebrał ją sobie", got)
	}
}

func TestAdminCannotDemoteTheLastAdmin(t *testing.T) {
	db := testDB(t)
	// Wywołujący nie ma już roli w bazie (RequireAdmin przepuścił go chwilę
	// wcześniej), a cel to jedyny pozostały admin — sytuacja z wyścigu.
	callerID := seedUser(t, db, "byly@example.com", "Były", models.UserRoleUser, time.Now())
	lastAdmin := seedUser(t, db, "admin@example.com", "Admin", models.UserRoleAdmin, time.Now())

	w := send(t, adminRouter(db, callerID), http.MethodPatch, "/admin/users/"+lastAdmin.String()+"/role", `{"role":"user"}`)
	if w.Code != http.StatusConflict || errorCode(t, w.Body.Bytes()) != httpx.CodeLastAdmin {
		t.Errorf("got %d %s, want 409 LAST_ADMIN", w.Code, w.Body)
	}
	if got := roleOf(t, db, lastAdmin); got != models.UserRoleAdmin {
		t.Error("zdjęto rolę ostatniemu adminowi")
	}
}

func TestAdminLogoutRevokesEverySession(t *testing.T) {
	db := testDB(t)
	adminID := seedUser(t, db, "admin@example.com", "Admin", models.UserRoleAdmin, time.Now())
	userID := seedAccount(t, db, "jan@example.com")

	w := send(t, adminRouter(db, adminID), http.MethodPost, "/admin/users/"+userID.String()+"/logout", "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}

	var active int64
	db.Model(&models.Session{}).Where("user_id = ? AND revoked_at IS NULL", userID).Count(&active)
	if active != 0 {
		t.Errorf("zostało %d aktywnych sesji", active)
	}
}

func TestAdminDeletesOtherAccountsButNotItself(t *testing.T) {
	db := testDB(t)
	adminID := seedUser(t, db, "admin@example.com", "Admin", models.UserRoleAdmin, time.Now())
	userID := seedAccount(t, db, "jan@example.com")
	r := adminRouter(db, adminID)

	w := send(t, r, http.MethodDelete, "/admin/users/"+adminID.String(), "")
	if w.Code != http.StatusConflict || errorCode(t, w.Body.Bytes()) != httpx.CodeAdminSelfAction {
		t.Errorf("usunięcie siebie: got %d %s, want 409 ADMIN_SELF_ACTION", w.Code, w.Body)
	}

	if w := send(t, r, http.MethodDelete, "/admin/users/"+userID.String(), ""); w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	if got := countFor(t, db, &models.PremiumScan{}, userID); got != 0 {
		t.Errorf("zostało %d skanów usuniętego konta", got)
	}
	var users int64
	db.Model(&models.User{}).Where("id = ?", userID).Count(&users)
	if users != 0 {
		t.Error("konto nie zostało usunięte")
	}
}

func TestAdminListScansMergesFreeAndPremium(t *testing.T) {
	db := testDB(t)
	adminID := seedUser(t, db, "admin@example.com", "Admin", models.UserRoleAdmin, time.Now())
	base := time.Now().Add(-time.Hour)

	freeID, premiumID, oldID := newID(t), newID(t), newID(t)
	seedScan(t, db, &models.Scan{ID: oldID, TargetURL: "https://old.example", Status: models.ScanStatusCompleted, CreatedAt: base})
	seedScan(t, db, &models.Scan{ID: freeID, TargetURL: "https://free.example", Status: models.ScanStatusPending, CreatedAt: base.Add(2 * time.Minute)})
	seedScan(t, db, &models.PremiumScan{ID: premiumID, UserID: adminID, TargetURL: "https://premium.example", Status: models.ScanStatusCompleted, CreatedAt: base.Add(time.Minute)})

	r := adminRouter(db, adminID)

	var page struct {
		Scans []adminScanRow `json:"scans"`
		Total int64          `json:"total"`
	}
	w := get(t, r, "/admin/scans")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if page.Total != 3 || len(page.Scans) != 3 {
		t.Fatalf("total=%d len=%d, want 3/3", page.Total, len(page.Scans))
	}
	if page.Scans[0].ID != freeID || page.Scans[1].ID != premiumID || page.Scans[2].ID != oldID {
		t.Errorf("kolejność = %v, want najnowsze pierwsze", page.Scans)
	}
	if page.Scans[1].Type != "premium" || page.Scans[1].UserID == nil || *page.Scans[1].UserID != adminID {
		t.Errorf("skan premium = %+v, want type=premium z user_id", page.Scans[1])
	}
	if page.Scans[0].Type != "free" || page.Scans[0].UserID != nil {
		t.Errorf("skan darmowy = %+v, want type=free bez user_id", page.Scans[0])
	}

	w = get(t, r, "/admin/scans?type=free&status=COMPLETED")
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if page.Total != 1 || page.Scans[0].ID != oldID {
		t.Errorf("filtr type+status zwrócił %+v", page.Scans)
	}

	if w := get(t, r, "/admin/scans?status=DONE"); w.Code != http.StatusBadRequest {
		t.Errorf("nieznany status: %d, want 400", w.Code)
	}
}

func TestAdminDeleteScanRemovesResults(t *testing.T) {
	db := testDB(t)
	adminID := seedUser(t, db, "admin@example.com", "Admin", models.UserRoleAdmin, time.Now())
	scanID := newID(t)
	seedScan(t, db, &models.Scan{ID: scanID, TargetURL: "https://example.com", Status: models.ScanStatusCompleted, CreatedAt: time.Now()})
	seedScan(t, db, &models.ScanResult{ScanID: scanID, TestName: "https"})
	r := adminRouter(db, adminID)

	if w := send(t, r, http.MethodDelete, "/admin/scans/"+scanID.String(), ""); w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}

	var scans, results int64
	db.Model(&models.Scan{}).Count(&scans)
	db.Model(&models.ScanResult{}).Count(&results)
	if scans != 0 || results != 0 {
		t.Errorf("zostało %d skanów i %d wyników", scans, results)
	}

	w := send(t, r, http.MethodDelete, "/admin/scans/"+scanID.String(), "")
	if w.Code != http.StatusNotFound || errorCode(t, w.Body.Bytes()) != httpx.CodeScanNotFound {
		t.Errorf("ponowne usunięcie: got %d %s, want 404 SCAN_NOT_FOUND", w.Code, w.Body)
	}
}
