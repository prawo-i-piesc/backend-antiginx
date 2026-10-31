package billing

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/prawo-i-piesc/backend/internal/models"
	"github.com/stripe/stripe-go/v87"
)

// ErrCustomerGone znaczy, że klienta usunięto po stronie Stripe, np. ręcznie w
// panelu. Wiersz z jego ID trzeba wtedy porzucić, a przy następnym Checkout
// założyć nowego.
var ErrCustomerGone = errors.New("klient Stripe nie istnieje")

// Gateway to wycinek API Stripe, z którego korzysta Service. Testy podstawiają
// własną implementację, zamiast rozmawiać ze Stripe.
type Gateway interface {
	CreateCustomer(ctx context.Context, userID uuid.UUID, email, name string) (string, error)
	// CreateCheckoutSession zwraca adres strony płatności Stripe.
	CreateCheckoutSession(ctx context.Context, params CheckoutParams) (string, error)
	// CreatePortalSession zwraca adres panelu klienta Stripe.
	CreatePortalSession(ctx context.Context, customerID, returnURL string) (string, error)
	// LatestSubscription zwraca nil, gdy klient nigdy nie miał subskrypcji.
	LatestSubscription(ctx context.Context, customerID string) (*RemoteSubscription, error)
	// DeleteCustomer od razu anuluje wszystkie subskrypcje klienta.
	DeleteCustomer(ctx context.Context, customerID string) error
}

type CheckoutParams struct {
	CustomerID string
	PriceID    string
	UserID     uuid.UUID
	SuccessURL string
	CancelURL  string
}

type RemoteSubscription struct {
	ID               string
	Status           string
	PriceID          string
	CurrentPeriodEnd *time.Time
	CancelAt         *time.Time
}

type stripeGateway struct {
	client *stripe.Client
}

func NewStripeGateway(secretKey string) Gateway {
	return &stripeGateway{client: stripe.NewClient(secretKey)}
}

func (g *stripeGateway) CreateCustomer(ctx context.Context, userID uuid.UUID, email, name string) (string, error) {
	params := &stripe.CustomerCreateParams{
		Email: stripe.String(email),
		Name:  stripe.String(name),
	}
	params.AddMetadata("user_id", userID.String())

	customer, err := g.client.V1Customers.Create(ctx, params)
	if err != nil {
		return "", err
	}
	return customer.ID, nil
}

func (g *stripeGateway) CreateCheckoutSession(ctx context.Context, p CheckoutParams) (string, error) {
	params := &stripe.CheckoutSessionCreateParams{
		Mode:     stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		Customer: stripe.String(p.CustomerID),
		LineItems: []*stripe.CheckoutSessionCreateLineItemParams{
			{Price: stripe.String(p.PriceID), Quantity: stripe.Int64(1)},
		},
		ClientReferenceID: stripe.String(p.UserID.String()),
		SuccessURL:        stripe.String(p.SuccessURL),
		CancelURL:         stripe.String(p.CancelURL),
		Locale:            stripe.String("auto"),
		SubscriptionData: &stripe.CheckoutSessionCreateSubscriptionDataParams{
			Metadata: map[string]string{"user_id": p.UserID.String()},
		},
	}

	session, err := g.client.V1CheckoutSessions.Create(ctx, params)
	if err != nil {
		return "", err
	}
	return session.URL, nil
}

func (g *stripeGateway) CreatePortalSession(ctx context.Context, customerID, returnURL string) (string, error) {
	session, err := g.client.V1BillingPortalSessions.Create(ctx, &stripe.BillingPortalSessionCreateParams{
		Customer:  stripe.String(customerID),
		ReturnURL: stripe.String(returnURL),
	})
	if err != nil {
		return "", missingAsGone(err)
	}
	return session.URL, nil
}

// LatestSubscription wybiera subskrypcję, która daje dostęp, a jeśli takiej
// nie ma, najnowszą. Stripe zwraca listę od najnowszej, więc anulowana
// wcześniej i opłacona później nie przesłonią się nawzajem.
func (g *stripeGateway) LatestSubscription(ctx context.Context, customerID string) (*RemoteSubscription, error) {
	params := &stripe.SubscriptionListParams{
		Customer: stripe.String(customerID),
		Status:   stripe.String("all"),
	}
	params.Limit = stripe.Int64(10)
	params.Single = true

	var latest *stripe.Subscription
	for sub, err := range g.client.V1Subscriptions.List(ctx, params).All(ctx) {
		if err != nil {
			return nil, missingAsGone(err)
		}
		if models.PlanForStatus(string(sub.Status)) != models.PlanFree {
			latest = sub
			break
		}
		if latest == nil {
			latest = sub
		}
	}
	if latest == nil {
		return nil, nil
	}
	return toRemote(latest), nil
}

func (g *stripeGateway) DeleteCustomer(ctx context.Context, customerID string) error {
	_, err := g.client.V1Customers.Delete(ctx, customerID, nil)
	if err != nil {
		if err := missingAsGone(err); errors.Is(err, ErrCustomerGone) {
			return nil
		}
		return err
	}
	return nil
}

func toRemote(sub *stripe.Subscription) *RemoteSubscription {
	remote := &RemoteSubscription{
		ID:     sub.ID,
		Status: string(sub.Status),
	}

	// Od API 2025-03-31 okres rozliczeniowy należy do pozycji subskrypcji, nie
	// do niej samej. Pro ma jedną pozycję, więc bierzemy pierwszą.
	if sub.Items != nil && len(sub.Items.Data) > 0 {
		item := sub.Items.Data[0]
		if item.Price != nil {
			remote.PriceID = item.Price.ID
		}
		remote.CurrentPeriodEnd = unixTime(item.CurrentPeriodEnd)
	}

	// Panel klienta anuluje na koniec okresu przez cancel_at_period_end, ale
	// datę można też ustawić wprost przez cancel_at; obie znaczą to samo.
	switch {
	case sub.CancelAt > 0:
		remote.CancelAt = unixTime(sub.CancelAt)
	case sub.CancelAtPeriodEnd:
		remote.CancelAt = remote.CurrentPeriodEnd
	}
	return remote
}

func unixTime(seconds int64) *time.Time {
	if seconds <= 0 {
		return nil
	}
	t := time.Unix(seconds, 0).UTC()
	return &t
}

func missingAsGone(err error) error {
	var stripeErr *stripe.Error
	if errors.As(err, &stripeErr) && stripeErr.Code == stripe.ErrorCodeResourceMissing {
		return ErrCustomerGone
	}
	return err
}
