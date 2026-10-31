package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/billing"
	"github.com/prawo-i-piesc/backend/internal/httpx"
	"github.com/prawo-i-piesc/backend/internal/models"
	"github.com/stripe/stripe-go/v87"
	"gorm.io/gorm"
)

const testWebhookSecret = "whsec_test"

// stripeStub odpowiada tylko na pytanie o subskrypcję; resztę Stripe
// sprawdzają testy pakietu billing.
type stripeStub struct {
	billing.Gateway
	subscriptions map[string]*billing.RemoteSubscription
	asked         []string
}

func (s *stripeStub) LatestSubscription(_ context.Context, customerID string) (*billing.RemoteSubscription, error) {
	s.asked = append(s.asked, customerID)
	return s.subscriptions[customerID], nil
}

func billingRouter(db *gorm.DB, service *billing.Service, webhookSecret string, userID uuid.UUID) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := NewBillingHandler(db, service, webhookSecret)

	r := gin.New()
	r.POST("/webhook", h.HandleWebhook)
	r.GET("/billing", func(c *gin.Context) {
		c.Set("userID", userID.String())
		h.HandleStatus(c)
	})
	return r
}

func postWebhook(r *gin.Engine, payload []byte, signature string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payload))
	req.Header.Set("Stripe-Signature", signature)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func signedEvent(t *testing.T, eventType, customerID string) ([]byte, string) {
	t.Helper()

	payload, err := json.Marshal(map[string]any{
		"id":          "evt_1",
		"object":      "event",
		"api_version": "2020-08-27",
		"type":        eventType,
		"data": map[string]any{
			"object": map[string]any{"id": "sub_1", "object": "subscription", "customer": customerID},
		},
	})
	if err != nil {
		t.Fatalf("zdarzenie: %v", err)
	}
	signed := stripe.GenerateTestSignedPayload(&stripe.UnsignedPayload{Payload: payload, Secret: testWebhookSecret})
	return payload, signed.Header
}

func TestWebhookSyncsTheCustomerItNames(t *testing.T) {
	db := testDB(t)
	userID := seedAccount(t, db, "jan@example.com")
	if err := db.Create(&models.Subscription{UserID: userID, StripeCustomerID: "cus_1", Status: models.SubscriptionStatusNone}).Error; err != nil {
		t.Fatalf("zasianie subskrypcji: %v", err)
	}
	stub := &stripeStub{subscriptions: map[string]*billing.RemoteSubscription{
		"cus_1": {ID: "sub_1", Status: "active", PriceID: "price_pro"},
	}}
	r := billingRouter(db, billing.New(db, stub, "price_pro", "https://antiginx.test"), testWebhookSecret, userID)

	payload, signature := signedEvent(t, "customer.subscription.updated", "cus_1")
	if w := postWebhook(r, payload, signature); w.Code != http.StatusOK {
		t.Fatalf("webhook = %d: %s", w.Code, w.Body.String())
	}

	if plan, _ := billing.UserPlan(db, userID); plan != models.PlanPro {
		t.Fatalf("plan po webhooku = %q", plan)
	}
}

func TestWebhookRejectsForgedSignature(t *testing.T) {
	db := testDB(t)
	stub := &stripeStub{}
	r := billingRouter(db, billing.New(db, stub, "price_pro", "https://antiginx.test"), testWebhookSecret, uuid.Nil)

	payload, _ := signedEvent(t, "customer.subscription.updated", "cus_1")
	forged := stripe.GenerateTestSignedPayload(&stripe.UnsignedPayload{Payload: payload, Secret: "whsec_inny"})

	w := postWebhook(r, payload, forged.Header)
	if w.Code != http.StatusBadRequest || errorCode(t, w.Body.Bytes()) != httpx.CodeWebhookSignatureFailed {
		t.Fatalf("podrobiony webhook = %d: %s", w.Code, w.Body.String())
	}
	if len(stub.asked) != 0 {
		t.Fatal("podrobione zdarzenie wywołało zapytanie do Stripe")
	}
}

func TestWebhookIgnoresEventsThatDoNotChangeTheSubscription(t *testing.T) {
	db := testDB(t)
	stub := &stripeStub{}
	r := billingRouter(db, billing.New(db, stub, "price_pro", "https://antiginx.test"), testWebhookSecret, uuid.Nil)

	payload, signature := signedEvent(t, "customer.updated", "cus_1")
	if w := postWebhook(r, payload, signature); w.Code != http.StatusOK {
		t.Fatalf("webhook = %d: %s", w.Code, w.Body.String())
	}
	if len(stub.asked) != 0 {
		t.Fatal("zdarzenie bez wpływu na subskrypcję wywołało zapytanie do Stripe")
	}
}

func TestWebhookWithoutSecretIsDisabled(t *testing.T) {
	db := testDB(t)
	r := billingRouter(db, billing.New(db, &stripeStub{}, "price_pro", "https://antiginx.test"), "", uuid.Nil)

	payload, signature := signedEvent(t, "customer.subscription.updated", "cus_1")
	w := postWebhook(r, payload, signature)
	if w.Code != http.StatusNotFound || errorCode(t, w.Body.Bytes()) != httpx.CodeBillingDisabled {
		t.Fatalf("webhook bez sekretu = %d: %s", w.Code, w.Body.String())
	}
}

func TestBillingStatusReadsTheLastSync(t *testing.T) {
	db := testDB(t)
	userID := seedAccount(t, db, "jan@example.com")
	if err := db.Create(&models.Subscription{UserID: userID, StripeCustomerID: "cus_1", Status: "active"}).Error; err != nil {
		t.Fatalf("zasianie subskrypcji: %v", err)
	}
	// Bez kluczy Stripe stan nadal da się odczytać, tylko nic nie da się kupić.
	r := billingRouter(db, billing.New(db, nil, "", ""), "", userID)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/billing", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	var got BillingStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("odpowiedź: %v", err)
	}
	if got.Enabled || got.Plan != models.PlanPro || got.Status != "active" || got.Manageable {
		t.Fatalf("status = %+v", got)
	}
}
