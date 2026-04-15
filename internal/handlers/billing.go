package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/tron-legacy/api/internal/database"
	"github.com/tron-legacy/api/internal/middleware"
	"github.com/tron-legacy/api/internal/models"
	"github.com/tron-legacy/api/internal/services"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Plan prices in BRL (monthly / yearly per-month)
var planPrices = map[string]map[string]float64{
	"starter":    {"monthly": 49.0, "yearly": 39.0},
	"pro":        {"monthly": 149.0, "yearly": 119.0},
	"enterprise": {"monthly": 399.0, "yearly": 319.0},
}

type CheckoutRequest struct {
	PlanID       string                       `json:"plan_id"`
	BillingCycle string                       `json:"billing_cycle"` // "monthly" or "yearly"
	BillingType  string                       `json:"billing_type"`  // "pix", "credit_card", "boleto"
	CpfCnpj      string                       `json:"cpf_cnpj,omitempty"`
	CreditCard   *services.CreditCardInfo      `json:"credit_card,omitempty"`
	CardHolder   *services.CreditCardHolderInfo `json:"card_holder,omitempty"`
}

type CheckoutResponse struct {
	Subscription  models.Subscription `json:"subscription"`
	PaymentID     string              `json:"payment_id,omitempty"`
	PixQrCode     string              `json:"pix_qr_code,omitempty"`
	PixPayload    string              `json:"pix_payload,omitempty"`
	PixExpiration string              `json:"pix_expiration,omitempty"`
	BoletoURL     string              `json:"boleto_url,omitempty"`
}

// Checkout godoc
// @Summary Criar ou atualizar assinatura com pagamento in-app
// @Description Cria ou faz upgrade da assinatura via Asaas com metodo de pagamento especifico (PIX, cartao ou boleto)
// @Tags billing
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body CheckoutRequest true "Dados do checkout"
// @Success 200 {object} CheckoutResponse
// @Failure 400 {string} string "Invalid request"
// @Failure 404 {string} string "Subscription not found"
// @Failure 500 {string} string "Failed to create subscription"
// @Router /orgs/current/subscription/checkout [post]
func Checkout(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)
	if orgID == primitive.NilObjectID {
		http.Error(w, `{"message":"Nenhuma organização selecionada"}`, http.StatusBadRequest)
		return
	}

	var req CheckoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"message":"Corpo da requisição inválido"}`, http.StatusBadRequest)
		return
	}

	// Validate plan
	prices, ok := planPrices[req.PlanID]
	if !ok {
		http.Error(w, `{"message":"Plano inválido. Escolha starter, pro ou enterprise"}`, http.StatusBadRequest)
		return
	}

	cycle := req.BillingCycle
	if cycle == "" {
		cycle = "monthly"
	}
	if cycle != "monthly" && cycle != "yearly" {
		http.Error(w, `{"message":"Ciclo inválido. Escolha monthly ou yearly"}`, http.StatusBadRequest)
		return
	}

	// Validate billing type
	billingType := req.BillingType
	if billingType == "" {
		billingType = "pix"
	}
	if billingType != "pix" && billingType != "credit_card" && billingType != "boleto" {
		http.Error(w, `{"message":"Método de pagamento inválido. Escolha pix, credit_card ou boleto"}`, http.StatusBadRequest)
		return
	}

	// Validate credit card data
	if billingType == "credit_card" {
		if req.CreditCard == nil || req.CardHolder == nil {
			http.Error(w, `{"message":"Dados do cartão são obrigatórios para pagamento com cartão"}`, http.StatusBadRequest)
			return
		}
	}

	price := prices[cycle]

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Get org to find owner
	var org models.Organization
	if err := database.DB.Collection("organizations").FindOne(ctx, bson.M{"_id": orgID}).Decode(&org); err != nil {
		http.Error(w, `{"message":"Organização não encontrada"}`, http.StatusNotFound)
		return
	}

	// Get owner user + profile
	var owner models.User
	if err := database.DB.Collection("users").FindOne(ctx, bson.M{"_id": org.OwnerUserID}).Decode(&owner); err != nil {
		http.Error(w, `{"message":"Usuário proprietário não encontrado"}`, http.StatusInternalServerError)
		return
	}

	var ownerProfile models.Profile
	database.DB.Collection("profiles").FindOne(ctx, bson.M{"user_id": org.OwnerUserID}).Decode(&ownerProfile)

	// Get current subscription
	var sub models.Subscription
	if err := database.DB.Collection("subscriptions").FindOne(ctx, bson.M{"org_id": orgID}).Decode(&sub); err != nil {
		http.Error(w, `{"message":"Assinatura não encontrada"}`, http.StatusNotFound)
		return
	}

	// ── Duplicate/same-plan guard ───────────────────────────────────
	if sub.PlanID == req.PlanID && sub.Status == "active" {
		http.Error(w, `{"message":"Você já possui o plano `+req.PlanID+` ativo. Não é necessário assinar novamente."}`, http.StatusConflict)
		return
	}

	// Block if there's a pending payment for the same plan (avoid double charge)
	if sub.PlanID == req.PlanID && sub.Status == "pending" {
		http.Error(w, `{"message":"Já existe um pagamento pendente para este plano. Aguarde a confirmação ou cancele antes de tentar novamente."}`, http.StatusConflict)
		return
	}

	// Block downgrade (must cancel first, then subscribe to lower plan)
	planRank := map[string]int{"free": 0, "starter": 1, "pro": 2, "enterprise": 3}
	if sub.Status == "active" && planRank[req.PlanID] < planRank[sub.PlanID] {
		http.Error(w, `{"message":"Para fazer downgrade, cancele o plano atual primeiro."}`, http.StatusBadRequest)
		return
	}

	// ── Asaas integration ───────────────────────────────────────────
	asaas := services.NewAsaasClient()

	// Find or create Asaas customer
	var customerID string
	if sub.AsaasCustomerID != "" {
		// Verify the saved customer still exists on Asaas
		if _, err := asaas.GetCustomer(sub.AsaasCustomerID); err == nil {
			customerID = sub.AsaasCustomerID
		} else {
			slog.Warn("asaas_saved_customer_invalid", "customer_id", sub.AsaasCustomerID, "error", err)
			// Fall through to find/create below
		}
	}
	if customerID == "" {
		existing, _ := asaas.FindCustomerByEmail(owner.Email)
		if existing != nil {
			customerID = existing.ID
		} else {
			cpfCnpj := req.CpfCnpj
			if cpfCnpj == "" && req.CardHolder != nil {
				cpfCnpj = req.CardHolder.CpfCnpj
			}
			if cpfCnpj == "" {
				http.Error(w, `{"message":"CPF/CNPJ é obrigatório para o primeiro pagamento"}`, http.StatusBadRequest)
				return
			}
			customer, err := asaas.CreateCustomer(services.CreateCustomerRequest{
				Name:    ownerProfile.Name,
				Email:   owner.Email,
				CpfCnpj: cpfCnpj,
			})
			if err != nil {
				slog.Error("asaas_create_customer_failed", "error", err)
				http.Error(w, `{"message":"Falha ao criar cliente no gateway de pagamento"}`, http.StatusInternalServerError)
				return
			}
			customerID = customer.ID
		}
	}

	// Cancel existing Asaas subscription if upgrading
	if sub.AsaasSubscriptionID != "" {
		_ = asaas.CancelSubscription(sub.AsaasSubscriptionID)
	}

	// Map billing type to Asaas format
	asaasBillingType := "UNDEFINED"
	switch billingType {
	case "pix":
		asaasBillingType = "PIX"
	case "credit_card":
		asaasBillingType = "CREDIT_CARD"
	case "boleto":
		asaasBillingType = "BOLETO"
	}

	asaasCycle := "MONTHLY"
	if cycle == "yearly" {
		asaasCycle = "YEARLY"
	}

	nextDueDate := time.Now().Format("2006-01-02")
	if billingType != "credit_card" {
		nextDueDate = time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	}
	subReq := services.CreateSubscriptionRequest{
		Customer:    customerID,
		BillingType: asaasBillingType,
		Value:       price,
		Cycle:       asaasCycle,
		Description: "Whodo " + req.PlanID + " plan",
		NextDueDate: nextDueDate,
	}

	// Attach credit card data if paying with card
	if billingType == "credit_card" {
		subReq.CreditCard = req.CreditCard
		subReq.CreditCardHolderInfo = req.CardHolder
		// Asaas requires remoteIp for credit card payments (anti-fraud)
		ip := r.Header.Get("X-Forwarded-For")
		if ip != "" {
			ip = strings.TrimSpace(strings.SplitN(ip, ",", 2)[0])
		} else if ip = r.Header.Get("X-Real-IP"); ip == "" {
			ip = r.RemoteAddr
		}
		if host, _, err := net.SplitHostPort(ip); err == nil {
			ip = host
		}
		subReq.RemoteIp = ip
	}

	asaasSub, err := asaas.CreateSubscription(subReq)
	if err != nil {
		slog.Error("asaas_create_subscription_failed", "error", err, "billing_type", billingType)
		errMsg := "Falha ao criar assinatura no gateway de pagamento"
		var asaasErr *services.AsaasError
		if errors.As(err, &asaasErr) {
			errMsg = asaasErr.Error()
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{"message": errMsg})
		return
	}

	// Update local subscription
	now := time.Now()

	// Parse next due date from Asaas response
	var nextDue time.Time
	if asaasSub.NextDueDate != "" {
		if parsed, err := time.Parse("2006-01-02", asaasSub.NextDueDate); err == nil {
			nextDue = parsed
		}
	}

	update := bson.M{
		"$set": bson.M{
			"plan_id":               req.PlanID,
			"status":                "pending",
			"asaas_customer_id":     customerID,
			"asaas_subscription_id": asaasSub.ID,
			"billing_cycle":         cycle,
			"billing_type":          billingType,
			"next_due_date":         nextDue,
			"overdue_since":         time.Time{},
			"previous_plan_id":      "",
			"downgraded_at":         time.Time{},
			"updated_at":            now,
		},
	}
	database.DB.Collection("subscriptions").UpdateOne(ctx, bson.M{"org_id": orgID}, update)

	sub.PlanID = req.PlanID
	sub.Status = "pending"
	sub.AsaasCustomerID = customerID
	sub.AsaasSubscriptionID = asaasSub.ID
	sub.BillingCycle = cycle
	sub.BillingType = billingType
	sub.NextDueDate = nextDue
	sub.UpdatedAt = now

	// ── Build response with payment data ────────────────────────────
	resp := CheckoutResponse{
		Subscription: sub,
	}

	// Fetch first payment from the new subscription to get payment-specific data
	payments, err := asaas.GetSubscriptionPayments(asaasSub.ID)
	if err != nil {
		slog.Warn("asaas_get_payments_failed", "error", err, "sub_id", asaasSub.ID)
		// Non-fatal: subscription created, webhook will handle confirmation
	} else if len(payments) > 0 {
		payment := payments[0]
		resp.PaymentID = payment.ID

		switch billingType {
		case "pix":
			qr, err := asaas.GetPixQrCode(payment.ID)
			if err != nil {
				slog.Warn("asaas_get_pix_qr_failed", "error", err, "payment_id", payment.ID)
			} else {
				resp.PixQrCode = qr.EncodedImage
				resp.PixPayload = qr.Payload
				resp.PixExpiration = qr.ExpirationDate
			}

		case "boleto":
			resp.BoletoURL = payment.BankSlipURL
		}
		// credit_card: Asaas charges immediately, webhook confirms
	}

	slog.Info("checkout_completed",
		"org_id", orgID.Hex(),
		"plan", req.PlanID,
		"cycle", cycle,
		"billing_type", billingType,
		"asaas_sub", asaasSub.ID,
	)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// GetBillingBalance godoc
// @Summary Obter saldo da conta Asaas
// @Description Retorna o saldo da conta Asaas (somente superusuário)
// @Tags billing
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]float64
// @Failure 502 {string} string "Failed to fetch balance from Asaas"
// @Router /admin/billing/balance [get]
func GetBillingBalance(w http.ResponseWriter, r *http.Request) {
	asaas := services.NewAsaasClient()
	balance, err := asaas.GetBalance()
	if err != nil {
		slog.Error("asaas_get_balance_failed", "error", err)
		http.Error(w, "Failed to fetch balance from Asaas", http.StatusBadGateway)
		return
	}

	json.NewEncoder(w).Encode(map[string]float64{"balance": balance})
}

// CancelSubscription godoc
// @Summary Cancelar assinatura
// @Description Cancela a assinatura atual da organização. O acesso continua até o fim do período
// @Tags billing
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]string
// @Failure 400 {string} string "Cannot cancel free plan"
// @Failure 404 {string} string "Subscription not found"
// @Router /orgs/current/subscription/cancel [post]
func CancelSubscription(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.GetOrgID(r)
	if orgID == primitive.NilObjectID {
		http.Error(w, "No organization selected", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var sub models.Subscription
	err := database.DB.Collection("subscriptions").FindOne(ctx, bson.M{"org_id": orgID}).Decode(&sub)
	if err != nil {
		http.Error(w, "Subscription not found", http.StatusNotFound)
		return
	}

	if sub.PlanID == "free" {
		http.Error(w, "Cannot cancel free plan", http.StatusBadRequest)
		return
	}

	// Cancel on Asaas
	if sub.AsaasSubscriptionID != "" {
		asaas := services.NewAsaasClient()
		if err := asaas.CancelSubscription(sub.AsaasSubscriptionID); err != nil {
			slog.Error("asaas_cancel_failed", "error", err)
			// Continue anyway — update local status
		}
	}

	// Update local subscription — downgrade to free immediately
	now := time.Now()
	update := bson.M{
		"$set": bson.M{
			"plan_id":    "free",
			"status":     "canceled",
			"updated_at": now,
		},
	}

	database.DB.Collection("subscriptions").UpdateOne(ctx, bson.M{"org_id": orgID}, update)

	slog.Info("subscription_canceled", "org_id", orgID.Hex(), "plan", sub.PlanID)

	json.NewEncoder(w).Encode(map[string]string{"message": "Subscription canceled. Access continues until period end."})
}
