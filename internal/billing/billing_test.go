package billing

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// fakeGateway udaje Stripe: klientów numeruje po kolei, a subskrypcje zwraca
// z mapy ustawionej przez test.
type fakeGateway struct {
	created       int
	checkouts     []CheckoutParams
	deleted       []string
	subscriptions map[string]*RemoteSubscription
	gone          map[string]bool
	synced        []string
}

func newFakeGateway() *fakeGateway {
	return &fakeGateway{subscriptions: map[string]*RemoteSubscription{}, gone: map[string]bool{}}
}

func (f *fakeGateway) CreateCustomer(_ context.Context, _ uuid.UUID, _, _ string) (string, error) {
	f.created++
	return "cus_" + string(rune('a'+f.created-1)), nil
}

func (f *fakeGateway) CreateCheckoutSession(_ context.Context, p CheckoutParams) (string, error) {
	f.checkouts = append(f.checkouts, p)
	return "https://checkout.stripe.test/" + p.CustomerID, nil
}

func (f *fakeGateway) CreatePortalSession(_ context.Context, customerID, _ string) (string, error) {
	if f.gone[customerID] {
		return "", ErrCustomerGone
	}
	return "https://billing.stripe.test/" + customerID, nil
}

func (f *fakeGateway) LatestSubscription(_ context.Context, customerID string) (*RemoteSubscription, error) {
	f.synced = append(f.synced, customerID)
	if f.gone[customerID] {
		return nil, ErrCustomerGone
	}
	return f.subscriptions[customerID], nil
}

func (f *fakeGateway) DeleteCustomer(_ context.Context, customerID string) error {
	f.deleted = append(f.deleted, customerID)
	return nil
}

func testDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{
		TranslateError: true,
		Logger:         logger.Discard,
	})
	if err != nil {
		t.Fatalf("otwarcie bazy testowej: %v", err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Subscription{}); err != nil {
		t.Fatalf("migracja bazy testowej: %v", err)
	}
	return db
}

func seedUser(t *testing.T, db *gorm.DB) *models.User {
	t.Helper()

	id := uuid.New()
	user := &models.User{ID: id, Email: id.String() + "@example.com", FullName: "Jan Kowalski", Role: models.UserRoleUser}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("zasianie konta: %v", err)
	}
	return user
}

func seedSubscription(t *testing.T, db *gorm.DB, sub models.Subscription) {
	t.Helper()

	if err := db.Create(&sub).Error; err != nil {
		t.Fatalf("zasianie subskrypcji: %v", err)
	}
}

func active() *RemoteSubscription {
	end := time.Now().Add(30 * 24 * time.Hour).UTC().Truncate(time.Second)
	return &RemoteSubscription{ID: "sub_1", Status: "active", PriceID: "price_pro", CurrentPeriodEnd: &end}
}

func TestCheckoutCreatesCustomerOnceAndRemembersIt(t *testing.T) {
	db := testDB(t)
	gateway := newFakeGateway()
	s := New(db, gateway, "price_pro", "https://antiginx.test")
	user := seedUser(t, db)

	url, err := s.Checkout(context.Background(), user)
	if err != nil {
		t.Fatalf("pierwszy Checkout: %v", err)
	}
	if url != "https://checkout.stripe.test/cus_a" {
		t.Fatalf("adres Checkout = %q", url)
	}
	if _, err := s.Checkout(context.Background(), user); err != nil {
		t.Fatalf("drugi Checkout: %v", err)
	}

	if gateway.created != 1 {
		t.Fatalf("założono %d klientów, ma być jeden na konto", gateway.created)
	}
	got := gateway.checkouts[0]
	if got.PriceID != "price_pro" || got.UserID != user.ID {
		t.Fatalf("Checkout dostał %+v", got)
	}
	if got.SuccessURL != "https://antiginx.test/dashboard/profile?billing=success" {
		t.Fatalf("SuccessURL = %q", got.SuccessURL)
	}

	sub, err := s.Subscription(user.ID)
	if err != nil || sub == nil {
		t.Fatalf("Subscription: %v, %v", sub, err)
	}
	if sub.Status != models.SubscriptionStatusNone || sub.CheckoutStartedAt == nil {
		t.Fatalf("po Checkout status = %q, start = %v", sub.Status, sub.CheckoutStartedAt)
	}
}

func TestCheckoutRefusesSomeoneWhoAlreadyPays(t *testing.T) {
	db := testDB(t)
	gateway := newFakeGateway()
	s := New(db, gateway, "price_pro", "https://antiginx.test")
	user := seedUser(t, db)

	// W bazie jeszcze none, ale Stripe wie już o opłaconej subskrypcji.
	seedSubscription(t, db, models.Subscription{UserID: user.ID, StripeCustomerID: "cus_paid", Status: models.SubscriptionStatusNone})
	gateway.subscriptions["cus_paid"] = active()

	_, err := s.Checkout(context.Background(), user)
	if !errors.Is(err, ErrAlreadySubscribed) {
		t.Fatalf("Checkout = %v, oczekiwano ErrAlreadySubscribed", err)
	}
	if len(gateway.checkouts) != 0 {
		t.Fatal("otwarto drugą płatność za ten sam plan")
	}
}

func TestCheckoutKeepsTheCustomerSavedFirst(t *testing.T) {
	db := testDB(t)
	gateway := newFakeGateway()
	s := New(db, gateway, "price_pro", "https://antiginx.test")
	user := seedUser(t, db)

	// Równoległe żądanie zdążyło zapisać swojego klienta.
	seedSubscription(t, db, models.Subscription{UserID: user.ID, StripeCustomerID: "cus_first", Status: models.SubscriptionStatusNone})

	customerID, err := s.createCustomer(context.Background(), user)
	if err != nil {
		t.Fatalf("createCustomer: %v", err)
	}
	if customerID != "cus_first" {
		t.Fatalf("createCustomer = %q, ma wygrać zapisany wcześniej cus_first", customerID)
	}
	if len(gateway.deleted) != 1 || gateway.deleted[0] != "cus_a" {
		t.Fatalf("zdublowany klient nie został usunięty: %v", gateway.deleted)
	}
}

func TestSyncMirrorsStripe(t *testing.T) {
	db := testDB(t)
	gateway := newFakeGateway()
	s := New(db, gateway, "price_pro", "https://antiginx.test")
	user := seedUser(t, db)
	started := time.Now()
	seedSubscription(t, db, models.Subscription{
		UserID: user.ID, StripeCustomerID: "cus_1", Status: models.SubscriptionStatusNone, CheckoutStartedAt: &started,
	})

	remote := active()
	remote.CancelAt = remote.CurrentPeriodEnd
	gateway.subscriptions["cus_1"] = remote

	sub, err := s.Sync(context.Background(), "cus_1")
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if sub.Plan() != models.PlanPro || sub.StripeSubscriptionID != "sub_1" || sub.CancelAt == nil {
		t.Fatalf("po Sync: %+v", sub)
	}
	if sub.CheckoutStartedAt != nil {
		t.Fatal("opłacony Checkout dalej czeka na płatność")
	}
	if plan, _ := UserPlan(db, user.ID); plan != models.PlanPro {
		t.Fatalf("UserPlan = %q po opłaceniu", plan)
	}

	gateway.subscriptions["cus_1"].Status = "canceled"
	if _, err := s.Sync(context.Background(), "cus_1"); err != nil {
		t.Fatalf("Sync po anulowaniu: %v", err)
	}
	if plan, _ := UserPlan(db, user.ID); plan != models.PlanFree {
		t.Fatalf("UserPlan = %q po anulowaniu", plan)
	}
}

func TestSyncForgetsCustomerDeletedInStripe(t *testing.T) {
	db := testDB(t)
	gateway := newFakeGateway()
	s := New(db, gateway, "price_pro", "https://antiginx.test")
	user := seedUser(t, db)
	seedSubscription(t, db, models.Subscription{UserID: user.ID, StripeCustomerID: "cus_gone", Status: "active"})
	gateway.gone["cus_gone"] = true

	sub, err := s.Sync(context.Background(), "cus_gone")
	if err != nil || sub != nil {
		t.Fatalf("Sync = %v, %v; oczekiwano nil, nil", sub, err)
	}
	if left, _ := s.Subscription(user.ID); left != nil {
		t.Fatal("wiersz usuniętego klienta został w bazie")
	}
}

func TestSyncOfUnknownCustomer(t *testing.T) {
	s := New(testDB(t), newFakeGateway(), "price_pro", "https://antiginx.test")

	if _, err := s.Sync(context.Background(), "cus_obcy"); !errors.Is(err, ErrNoCustomer) {
		t.Fatalf("Sync = %v, oczekiwano ErrNoCustomer", err)
	}
}

func TestReconcileAsksOnlyAboutAccountsThatCanChange(t *testing.T) {
	db := testDB(t)
	gateway := newFakeGateway()
	s := New(db, gateway, "price_pro", "https://antiginx.test")

	recent := time.Now().Add(-time.Hour)
	stale := time.Now().Add(-48 * time.Hour)
	for customerID, sub := range map[string]models.Subscription{
		"cus_active":   {Status: "active"},
		"cus_past_due": {Status: "past_due"},
		"cus_canceled": {Status: "canceled"},
		"cus_paying":   {Status: models.SubscriptionStatusNone, CheckoutStartedAt: &recent},
		"cus_left":     {Status: models.SubscriptionStatusNone, CheckoutStartedAt: &stale},
		"cus_never":    {Status: models.SubscriptionStatusNone},
	} {
		sub.UserID = seedUser(t, db).ID
		sub.StripeCustomerID = customerID
		seedSubscription(t, db, sub)
	}

	if err := s.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	synced := map[string]bool{}
	for _, id := range gateway.synced {
		synced[id] = true
	}
	want := map[string]bool{"cus_active": true, "cus_past_due": true, "cus_paying": true}
	for id := range want {
		if !synced[id] {
			t.Errorf("%s nie został sprawdzony", id)
		}
	}
	for id := range synced {
		if !want[id] {
			t.Errorf("%s sprawdzony bez potrzeby", id)
		}
	}
}

func TestCloseAccountEndsTheSubscription(t *testing.T) {
	db := testDB(t)
	gateway := newFakeGateway()
	s := New(db, gateway, "price_pro", "https://antiginx.test")
	user := seedUser(t, db)
	seedSubscription(t, db, models.Subscription{UserID: user.ID, StripeCustomerID: "cus_1", Status: "active"})

	if err := s.CloseAccount(context.Background(), user.ID); err != nil {
		t.Fatalf("CloseAccount: %v", err)
	}
	if len(gateway.deleted) != 1 || gateway.deleted[0] != "cus_1" {
		t.Fatalf("usunięci klienci: %v", gateway.deleted)
	}
}

func TestCloseAccountWithoutStripeKeys(t *testing.T) {
	db := testDB(t)
	s := New(db, nil, "", "")

	free := seedUser(t, db)
	if err := s.CloseAccount(context.Background(), free.ID); err != nil {
		t.Fatalf("konto bez płatności: %v", err)
	}

	paying := seedUser(t, db)
	seedSubscription(t, db, models.Subscription{UserID: paying.ID, StripeCustomerID: "cus_1", Status: "active"})
	if err := s.CloseAccount(context.Background(), paying.ID); !errors.Is(err, ErrDisabled) {
		t.Fatalf("CloseAccount = %v; płacącego konta nie wolno usunąć bez anulowania", err)
	}
}

func TestPortalForgetsCustomerDeletedInStripe(t *testing.T) {
	db := testDB(t)
	gateway := newFakeGateway()
	s := New(db, gateway, "price_pro", "https://antiginx.test")
	user := seedUser(t, db)
	seedSubscription(t, db, models.Subscription{UserID: user.ID, StripeCustomerID: "cus_gone", Status: "canceled"})
	gateway.gone["cus_gone"] = true

	if _, err := s.Portal(context.Background(), user.ID); !errors.Is(err, ErrNoCustomer) {
		t.Fatalf("Portal = %v, oczekiwano ErrNoCustomer", err)
	}
	if left, _ := s.Subscription(user.ID); left != nil {
		t.Fatal("wiersz usuniętego klienta został w bazie")
	}
}
