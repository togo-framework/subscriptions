// Package subscriptions is togo's subscription-management plugin: plans, subscribe /
// cancel / change, trials, and status — built on the `payment` plugin (charges are
// delegated to whatever PaymentProvider is registered in the kernel). If no payment
// provider is installed, it still manages subscription state locally.
//
// Install: `togo install togo-framework/subscriptions` (blank-import registers it).
package subscriptions

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/togo-framework/orm"
	"github.com/togo-framework/togo"
)

// Charger is the minimal slice of the payment plugin this needs. The payment
// plugin's service is looked up from the kernel container (no import dependency),
// so subscriptions builds + runs whether or not payment is installed.
type Charger interface {
	CreateSubscription(ctx context.Context, customer, plan string, meta map[string]string) (string, error)
}

func init() {
	togo.RegisterProviderFunc("subscriptions", togo.PriorityLate+15, func(k *togo.Kernel) error {
		s := &Service{k: k}
		if err := s.migrate(context.Background()); err != nil {
			return err
		}
		s.routes()
		k.Set("subscriptions", s)
		return nil
	})
}

type Service struct{ k *togo.Kernel }

func (s *Service) db(ctx context.Context) (*sql.DB, error) { return s.k.SQL(ctx) }

// ── Models ──────────────────────────────────────────────────────────────────

type Plan struct {
	ID        string  `db:"id" json:"id"`
	Name      string  `db:"name" json:"name"`
	Price     float64 `db:"price" json:"price"`
	Currency  string  `db:"currency" json:"currency"`
	Interval  string  `db:"interval" json:"interval"` // "month" | "year" | "week" | "day"
	Features  string  `db:"features" json:"features"` // free-form / JSON
	CreatedAt string  `db:"created_at" json:"created_at"`
}

type Subscription struct {
	ID               string `db:"id" json:"id"`
	UserID           string `db:"user_id" json:"user_id"`
	PlanID           string `db:"plan_id" json:"plan_id"`
	Status           string `db:"status" json:"status"` // trialing|active|canceled|past_due
	ProviderRef      string `db:"provider_ref" json:"provider_ref"`
	TrialEndsAt      string `db:"trial_ends_at" json:"trial_ends_at"`
	CurrentPeriodEnd string `db:"current_period_end" json:"current_period_end"`
	CreatedAt        string `db:"created_at" json:"created_at"`
	CanceledAt       string `db:"canceled_at" json:"canceled_at"`
}

func (s *Service) migrate(ctx context.Context) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS plans (id text PRIMARY KEY, name text NOT NULL, price double precision NOT NULL DEFAULT 0, currency text NOT NULL DEFAULT 'usd', interval text NOT NULL DEFAULT 'month', features text NOT NULL DEFAULT '', created_at text NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS subscriptions (id text PRIMARY KEY, user_id text NOT NULL, plan_id text NOT NULL, status text NOT NULL DEFAULT 'active', provider_ref text NOT NULL DEFAULT '', trial_ends_at text NOT NULL DEFAULT '', current_period_end text NOT NULL DEFAULT '', created_at text NOT NULL, canceled_at text NOT NULL DEFAULT '')`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) plans(db *sql.DB) *orm.Query[Plan] {
	return orm.For[Plan](db, s.k.Dialect(), "plans")
}
func (s *Service) subs(db *sql.DB) *orm.Query[Subscription] {
	return orm.For[Subscription](db, s.k.Dialect(), "subscriptions")
}

func newID() string  { b := make([]byte, 16); _, _ = rand.Read(b); return hex.EncodeToString(b) }
func nowRFC() string { return time.Now().UTC().Format(time.RFC3339) }

func periodEnd(interval string, from time.Time) string {
	switch interval {
	case "year":
		return from.AddDate(1, 0, 0).UTC().Format(time.RFC3339)
	case "week":
		return from.AddDate(0, 0, 7).UTC().Format(time.RFC3339)
	case "day":
		return from.AddDate(0, 0, 1).UTC().Format(time.RFC3339)
	default: // month
		return from.AddDate(0, 1, 0).UTC().Format(time.RFC3339)
	}
}

// charger returns the installed payment service if it satisfies Charger.
func (s *Service) charger() (Charger, bool) {
	if v, ok := s.k.Get("payment"); ok {
		if c, ok := v.(Charger); ok {
			return c, true
		}
	}
	return nil, false
}

// ── Operations ───────────────────────────────────────────────────────────────

func (s *Service) CreatePlan(ctx context.Context, p Plan) (*Plan, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	if p.Currency == "" {
		p.Currency = "usd"
	}
	if p.Interval == "" {
		p.Interval = "month"
	}
	return s.plans(db).Create(ctx, map[string]any{
		"id": newID(), "name": p.Name, "price": p.Price, "currency": p.Currency,
		"interval": p.Interval, "features": p.Features, "created_at": nowRFC(),
	})
}

func (s *Service) ListPlans(ctx context.Context) ([]Plan, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	return s.plans(db).Order("price ASC").Get(ctx)
}

// Subscribe creates a subscription for a user on a plan, with an optional trial.
// If a payment provider is installed it creates a provider-side subscription.
func (s *Service) Subscribe(ctx context.Context, userID, planID string, trialDays int) (*Subscription, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	plan, err := s.plans(db).Find(ctx, planID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	status := "active"
	trialEnds := ""
	if trialDays > 0 {
		status = "trialing"
		trialEnds = now.AddDate(0, 0, trialDays).UTC().Format(time.RFC3339)
	}
	interval := "month"
	if plan != nil {
		interval = plan.Interval
	}
	providerRef := ""
	if c, ok := s.charger(); ok {
		if ref, e := c.CreateSubscription(ctx, userID, planID, map[string]string{"user_id": userID, "plan_id": planID}); e == nil {
			providerRef = ref
		}
	}
	return s.subs(db).Create(ctx, map[string]any{
		"id": newID(), "user_id": userID, "plan_id": planID, "status": status,
		"provider_ref": providerRef, "trial_ends_at": trialEnds,
		"current_period_end": periodEnd(interval, now), "created_at": nowRFC(),
	})
}

func (s *Service) ListSubscriptions(ctx context.Context, userID string) ([]Subscription, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	return s.subs(db).Where("user_id", "=", userID).Order("created_at DESC").Get(ctx)
}

func (s *Service) Cancel(ctx context.Context, id string) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	return s.subs(db).Where("id", "=", id).Update(ctx, map[string]any{"status": "canceled", "canceled_at": nowRFC()})
}

// Change upgrades/downgrades a subscription to another plan.
func (s *Service) Change(ctx context.Context, id, planID string) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	plan, _ := s.plans(db).Find(ctx, planID)
	interval := "month"
	if plan != nil {
		interval = plan.Interval
	}
	return s.subs(db).Where("id", "=", id).Update(ctx, map[string]any{
		"plan_id": planID, "status": "active", "current_period_end": periodEnd(interval, time.Now()),
	})
}

// ── HTTP ──────────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Service) routes() {
	r := s.k.Router
	base := strings.TrimRight(s.k.Config.RESTPath, "/")
	r.Get(base+"/plans", s.handleListPlans)
	r.Post(base+"/plans", s.handleCreatePlan)
	r.Get(base+"/subscriptions", s.handleListSubs)
	r.Post(base+"/subscriptions", s.handleSubscribe)
	r.Post(base+"/subscriptions/{id}/cancel", s.handleCancel)
	r.Post(base+"/subscriptions/{id}/change", s.handleChange)
}

func (s *Service) handleListPlans(w http.ResponseWriter, r *http.Request) {
	plans, err := s.ListPlans(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, plans)
}

func (s *Service) handleCreatePlan(w http.ResponseWriter, r *http.Request) {
	var p Plan
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	rec, err := s.CreatePlan(r.Context(), p)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

func (s *Service) handleListSubs(w http.ResponseWriter, r *http.Request) {
	uid := r.URL.Query().Get("user_id")
	if uid == "" {
		uid = r.Header.Get("X-User-Id")
	}
	if uid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id required"})
		return
	}
	subs, err := s.ListSubscriptions(r.Context(), uid)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, subs)
}

func (s *Service) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserID    string `json:"user_id"`
		PlanID    string `json:"plan_id"`
		TrialDays int    `json:"trial_days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.UserID == "" || body.PlanID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id and plan_id required"})
		return
	}
	sub, err := s.Subscribe(r.Context(), body.UserID, body.PlanID, body.TrialDays)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, sub)
}

func (s *Service) handleCancel(w http.ResponseWriter, r *http.Request) {
	if err := s.Cancel(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) handleChange(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PlanID string `json:"plan_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.PlanID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "plan_id required"})
		return
	}
	if err := s.Change(r.Context(), chi.URLParam(r, "id"), body.PlanID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
