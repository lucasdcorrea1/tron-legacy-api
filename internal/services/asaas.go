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
	Customer     string  `json:"customer"`
	BillingType  string  `json:"billingType"` // BOLETO, CREDIT_CARD, PIX, UNDEFINED
	Value        float64 `json:"value"`
	Cycle        string  `json:"cycle"`       // MONTHLY, YEARLY
	Description  string  `json:"description"`
	NextDueDate  string  `json:"nextDueDate,omitempty"`
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
