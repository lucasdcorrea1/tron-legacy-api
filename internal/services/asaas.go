package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/tron-legacy/api/internal/config"
)

const (
	asaasProductionURL = "https://api.asaas.com/v3"
	asaasSandboxURL    = "https://sandbox.asaas.com/api/v3"
)

type AsaasClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewAsaasClient creates a new Asaas API client.
func NewAsaasClient() *AsaasClient {
	cfg := config.Get()
	base := asaasProductionURL
	if cfg.AsaasSandbox {
		base = asaasSandboxURL
	}
	return &AsaasClient{
		baseURL: base,
		apiKey:  cfg.AsaasAPIKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ── Request/Response types ───────────────────────────────────────────

type AsaasCustomer struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	CpfCnpj string `json:"cpfCnpj,omitempty"`
}

type CreateCustomerRequest struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	CpfCnpj string `json:"cpfCnpj,omitempty"`
}

type AsaasSubscription struct {
	ID          string `json:"id"`
	Customer    string `json:"customer"`
	Status      string `json:"status"`
	BillingType string `json:"billingType"`
	Value       float64 `json:"value"`
	NextDueDate string `json:"nextDueDate"`
}

type CreateSubscriptionRequest struct {
	Customer             string                `json:"customer"`
	BillingType          string                `json:"billingType"` // BOLETO, CREDIT_CARD, PIX, UNDEFINED
	Value                float64               `json:"value"`
	Cycle                string                `json:"cycle"`       // MONTHLY, YEARLY
	Description          string                `json:"description"`
	NextDueDate          string                `json:"nextDueDate,omitempty"`
	CreditCard           *CreditCardInfo       `json:"creditCard,omitempty"`
	CreditCardHolderInfo *CreditCardHolderInfo `json:"creditCardHolderInfo,omitempty"`
	RemoteIp             string                `json:"remoteIp,omitempty"`
}

// CreditCardInfo holds credit card data for Asaas subscription creation.
type CreditCardInfo struct {
	HolderName  string `json:"holderName"`
	Number      string `json:"number"`
	ExpiryMonth string `json:"expiryMonth"`
	ExpiryYear  string `json:"expiryYear"`
	Ccv         string `json:"ccv"`
}

// CreditCardHolderInfo holds cardholder details required by Asaas.
type CreditCardHolderInfo struct {
	Name          string `json:"name"`
	Email         string `json:"email"`
	CpfCnpj       string `json:"cpfCnpj"`
	PostalCode    string `json:"postalCode"`
	AddressNumber string `json:"addressNumber"`
	Phone         string `json:"phone"`
}

// PixQrCode holds PIX payment QR code data from Asaas.
type PixQrCode struct {
	EncodedImage   string `json:"encodedImage"`
	Payload        string `json:"payload"`
	ExpirationDate string `json:"expirationDate"`
}

type AsaasError struct {
	Errors []struct {
		Code        string `json:"code"`
		Description string `json:"description"`
	} `json:"errors"`
}

func (e *AsaasError) Error() string {
	if len(e.Errors) > 0 {
		return e.Errors[0].Description
	}
	return "Asaas API error"
}

// ── API Methods ──────────────────────────────────────────────────────

func (c *AsaasClient) doRequest(method, path string, body interface{}) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequest(method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("access_token", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var asaasErr AsaasError
		if json.Unmarshal(respBody, &asaasErr) == nil && len(asaasErr.Errors) > 0 {
			return nil, &asaasErr
		}
		return nil, fmt.Errorf("Asaas API error: %s (status %d)", string(respBody), resp.StatusCode)
	}

	return respBody, nil
}

// CreateCustomer creates or finds a customer on Asaas.
func (c *AsaasClient) CreateCustomer(req CreateCustomerRequest) (*AsaasCustomer, error) {
	data, err := c.doRequest("POST", "/customers", req)
	if err != nil {
		return nil, err
	}
	var customer AsaasCustomer
	if err := json.Unmarshal(data, &customer); err != nil {
		return nil, fmt.Errorf("unmarshal customer: %w", err)
	}
	return &customer, nil
}

// FindCustomerByEmail searches for a customer by email.
func (c *AsaasClient) FindCustomerByEmail(email string) (*AsaasCustomer, error) {
	data, err := c.doRequest("GET", "/customers?email="+email, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Data []AsaasCustomer `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if len(result.Data) > 0 {
		return &result.Data[0], nil
	}
	return nil, nil
}

// CreateSubscription creates a new subscription on Asaas.
func (c *AsaasClient) CreateSubscription(req CreateSubscriptionRequest) (*AsaasSubscription, error) {
	data, err := c.doRequest("POST", "/subscriptions", req)
	if err != nil {
		return nil, err
	}
	var sub AsaasSubscription
	if err := json.Unmarshal(data, &sub); err != nil {
		return nil, fmt.Errorf("unmarshal subscription: %w", err)
	}
	return &sub, nil
}

// CancelSubscription cancels a subscription on Asaas.
func (c *AsaasClient) CancelSubscription(subscriptionID string) error {
	_, err := c.doRequest("DELETE", "/subscriptions/"+subscriptionID, nil)
	return err
}

// AsaasBalance represents the Asaas account balance.
type AsaasBalance struct {
	Balance float64 `json:"balance"`
}

// GetBalance retrieves the current account balance from Asaas.
func (c *AsaasClient) GetBalance() (float64, error) {
	data, err := c.doRequest("GET", "/finance/balance", nil)
	if err != nil {
		return 0, err
	}
	var result AsaasBalance
	if err := json.Unmarshal(data, &result); err != nil {
		return 0, fmt.Errorf("unmarshal balance: %w", err)
	}
	return result.Balance, nil
}

// GetSubscription retrieves a subscription from Asaas.
func (c *AsaasClient) GetSubscription(subscriptionID string) (*AsaasSubscription, error) {
	data, err := c.doRequest("GET", "/subscriptions/"+subscriptionID, nil)
	if err != nil {
		return nil, err
	}
	var sub AsaasSubscription
	if err := json.Unmarshal(data, &sub); err != nil {
		return nil, err
	}
	return &sub, nil
}

// AsaasPayment represents a payment from Asaas.
type AsaasPayment struct {
	ID          string  `json:"id"`
	Status      string  `json:"status"`
	Value       float64 `json:"value"`
	BillingType string  `json:"billingType"`
	InvoiceURL  string  `json:"invoiceUrl"`
	BankSlipURL string  `json:"bankSlipUrl"`
}

// GetSubscriptionPaymentURL retrieves the invoiceUrl of the first pending payment for a subscription.
func (c *AsaasClient) GetSubscriptionPaymentURL(subscriptionID string) (string, error) {
	data, err := c.doRequest("GET", "/subscriptions/"+subscriptionID+"/payments", nil)
	if err != nil {
		return "", err
	}
	var result struct {
		Data []AsaasPayment `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("unmarshal payments: %w", err)
	}
	for _, p := range result.Data {
		if p.InvoiceURL != "" {
			return p.InvoiceURL, nil
		}
	}
	return "", fmt.Errorf("no payment with invoiceUrl found for subscription %s", subscriptionID)
}

// GetSubscriptionPayments returns all payments for a subscription.
func (c *AsaasClient) GetSubscriptionPayments(subscriptionID string) ([]AsaasPayment, error) {
	data, err := c.doRequest("GET", "/subscriptions/"+subscriptionID+"/payments", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Data []AsaasPayment `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("unmarshal payments: %w", err)
	}
	return result.Data, nil
}

// GetPixQrCode retrieves the PIX QR code for a payment.
func (c *AsaasClient) GetPixQrCode(paymentID string) (*PixQrCode, error) {
	data, err := c.doRequest("GET", "/payments/"+paymentID+"/pixQrCode", nil)
	if err != nil {
		return nil, err
	}
	var qr PixQrCode
	if err := json.Unmarshal(data, &qr); err != nil {
		return nil, fmt.Errorf("unmarshal pix qr: %w", err)
	}
	return &qr, nil
}
