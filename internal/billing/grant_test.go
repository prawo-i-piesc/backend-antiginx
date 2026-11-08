package billing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/models"
)

func planOf(t *testing.T, s *Service, userID uuid.UUID) string {
	t.Helper()
	plan, err := UserPlan(s.db, userID)
	if err != nil {
		t.Fatalf("UserPlan: %v", err)
	}
	return plan
}

func TestAGrantGivesProUntilItEnds(t *testing.T) {
	db := testDB(t)
	s := New(db, nil, "", "")
	user := seedUser(t, db)
	admin := uuid.New()

	if plan := planOf(t, s, user.ID); plan != models.PlanFree {
		t.Fatalf("plan before any grant = %s", plan)
	}
	grant, err := s.Grant(context.Background(), user.ID, admin, 30)
	if err != nil {
		t.Fatal(err)
	}
	if grant.ExpiresAt == nil || time.Until(*grant.ExpiresAt) < 29*24*time.Hour {
		t.Fatalf("30-day grant ends at %v", grant.ExpiresAt)
	}
	if plan := planOf(t, s, user.ID); plan != models.PlanPro {
		t.Fatalf("plan with a grant = %s", plan)
	}

	// Wygasłe nadanie nie daje Pro, choć wiersz zostaje.
	past := time.Now().Add(-time.Minute)
	db.Model(&models.PlanGrant{}).Where("user_id = ?", user.ID).Update("expires_at", past)
	if plan := planOf(t, s, user.ID); plan != models.PlanFree {
		t.Fatalf("plan after the grant ended = %s", plan)
	}
}

func TestAGrantWithoutDaysNeverEndsAndALaterOneReplacesIt(t *testing.T) {
	db := testDB(t)
	s := New(db, nil, "", "")
	user := seedUser(t, db)
	first, second := uuid.New(), uuid.New()

	if _, err := s.Grant(context.Background(), user.ID, first, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Grant(context.Background(), user.ID, second, 7); err != nil {
		t.Fatal(err)
	}

	var grants []models.PlanGrant
	db.Find(&grants)
	if len(grants) != 1 || grants[0].GrantedBy != second || grants[0].ExpiresAt == nil {
		t.Fatalf("grants = %+v, want the second one only, with an end", grants)
	}

	if _, err := s.Grant(context.Background(), user.ID, first, 0); err != nil {
		t.Fatal(err)
	}
	grant, _ := ActiveGrant(db, user.ID, time.Now().AddDate(50, 0, 0))
	if grant == nil {
		t.Fatal("a grant without days ended")
	}

	if err := s.Revoke(user.ID); err != nil {
		t.Fatal(err)
	}
	if plan := planOf(t, s, user.ID); plan != models.PlanFree {
		t.Fatalf("plan after revoking = %s", plan)
	}
	if err := s.Revoke(user.ID); err != nil {
		t.Fatalf("revoking twice: %v", err)
	}
}

func TestAGrantIsRefusedWhileStripeSaysTheAccountPays(t *testing.T) {
	db := testDB(t)
	gateway := newFakeGateway()
	s := New(db, gateway, "price_pro", "https://antiginx.test")
	user := seedUser(t, db)
	seedSubscription(t, db, models.Subscription{UserID: user.ID, StripeCustomerID: "cus_pay", Status: "canceled"})

	// Wiersz jest nieaktualny: w Stripe subskrypcja już trwa. Decyduje Stripe.
	gateway.subscriptions["cus_pay"] = active()
	if _, err := s.Grant(context.Background(), user.ID, uuid.New(), 30); !errors.Is(err, ErrSubscriptionActive) {
		t.Fatalf("grant to a paying account: %v, want ErrSubscriptionActive", err)
	}
	if len(gateway.synced) != 1 {
		t.Fatalf("Stripe asked %d times, want once", len(gateway.synced))
	}

	// I odwrotnie: wiersz mówi „płaci”, Stripe — że anulowano.
	gateway.subscriptions["cus_pay"] = &RemoteSubscription{ID: "sub_1", Status: "canceled"}
	if _, err := s.Grant(context.Background(), user.ID, uuid.New(), 30); err != nil {
		t.Fatalf("grant after the subscription ended: %v", err)
	}
}

func TestCheckoutIsRefusedWhileAGrantLasts(t *testing.T) {
	db := testDB(t)
	gateway := newFakeGateway()
	s := New(db, gateway, "price_pro", "https://antiginx.test")
	user := seedUser(t, db)

	if _, err := s.Grant(context.Background(), user.ID, uuid.New(), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkout(context.Background(), user); !errors.Is(err, ErrAlreadySubscribed) {
		t.Fatalf("Checkout with a grant: %v, want ErrAlreadySubscribed", err)
	}
	if len(gateway.checkouts) != 0 {
		t.Fatalf("%d Checkout sessions opened", len(gateway.checkouts))
	}
}
