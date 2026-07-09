// Package contaazul provides a client for the Conta Azul API v2.
//
// Authentication: OAuth 2.0 Authorization Code flow.
// API base: https://api-v2.contaazul.com
// Auth/Token base: https://auth.contaazul.com
//
// All tokens are encrypted at rest via internal/crypto and refreshed
// transparently when expired.
package contaazul

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tron-legacy/api/internal/config"
)

// Client is a per-EndClient Conta Azul API client.
// It carries the access token in memory and refreshes when needed.
type Client struct {
	http         *http.Client
	accessToken  string
	refreshToken string
	expiresAt    time.Time
	apiBase      string
	tokenURL     string
	clientID     string
	clientSecret string
}

// New builds a client with the given access/refresh tokens.
func New(accessToken, refreshToken string, expiresAt time.Time) *Client {
	cfg := config.Get()
	return &Client{
		http:         &http.Client{Timeout: 30 * time.Second},
		accessToken:  accessToken,
		refreshToken: refreshToken,
		expiresAt:    expiresAt,
		apiBase:      cfg.ContaAzulAPIBaseURL,
		tokenURL:     cfg.ContaAzulTokenURL,
		clientID:     cfg.ContaAzulClientID,
		clientSecret: cfg.ContaAzulClientSecret,
	}
}

// AccessToken returns the current (possibly refreshed) access token.
func (c *Client) AccessToken() string { return c.accessToken }

// RefreshToken returns the current refresh token.
func (c *Client) RefreshToken() string { return c.refreshToken }

// ExpiresAt returns the expiration timestamp of the current access token.
func (c *Client) ExpiresAt() time.Time { return c.expiresAt }

// TokenResponse mirrors the OAuth token response.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
}

// ExchangeCode exchanges an authorization code for tokens.
func ExchangeCode(ctx context.Context, code, redirectURI string) (*TokenResponse, error) {
	cfg := config.Get()
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", cfg.ContaAzulClientID)
	form.Set("client_secret", cfg.ContaAzulClientSecret)
	return postToken(ctx, cfg.ContaAzulTokenURL, form)
}

// Refresh trades the refresh token for a new access token. Updates the client state on success.
func (c *Client) Refresh(ctx context.Context) error {
	if c.refreshToken == "" {
		return errors.New("conta_azul: missing refresh token")
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", c.refreshToken)
	form.Set("client_id", c.clientID)
	form.Set("client_secret", c.clientSecret)

	tok, err := postToken(ctx, c.tokenURL, form)
	if err != nil {
		return err
	}
	c.accessToken = tok.AccessToken
	if tok.RefreshToken != "" {
		c.refreshToken = tok.RefreshToken
	}
	c.expiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	return nil
}

func postToken(ctx context.Context, tokenURL string, form url.Values) (*TokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("conta_azul: token request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("conta_azul: token http %d: %s", resp.StatusCode, string(body))
	}
	var tr TokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("conta_azul: decode token: %w", err)
	}
	return &tr, nil
}

// get performs a GET request against the API, refreshing the token if expired.
// The response body (raw bytes) is decoded into out (JSON).
func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	if time.Until(c.expiresAt) < 60*time.Second {
		if err := c.Refresh(ctx); err != nil {
			return fmt.Errorf("conta_azul: refresh before call: %w", err)
		}
	}

	u := strings.TrimRight(c.apiBase, "/") + path
	if query != nil && len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("conta_azul: GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusUnauthorized {
		// token possibly invalid mid-call — try one refresh
		if err := c.Refresh(ctx); err == nil {
			return c.get(ctx, path, query, out)
		}
		return fmt.Errorf("conta_azul: 401 unauthorized on %s", path)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("conta_azul: GET %s http %d: %s", path, resp.StatusCode, string(body))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

// ── Domain types (subset of Conta Azul responses we care about) ────

// Installment represents an installment of a payable or receivable.
// The actual API response has many more fields; we keep what the dashboard needs.
type Installment struct {
	ID            string    `json:"id"`
	Description   string    `json:"descricao"`
	DueDate       time.Time `json:"data_vencimento"`
	PaidDate      time.Time `json:"data_pagamento,omitempty"`
	CompetencyDate time.Time `json:"data_competencia,omitempty"`
	Value         float64   `json:"valor"`
	Status        string    `json:"status"` // PENDING, PAID, OVERDUE, CANCELED
	CategoryID    string    `json:"categoria_id,omitempty"`
	CategoryName  string    `json:"categoria_nome,omitempty"`
}

// InstallmentPage is a paginated response of installments.
type InstallmentPage struct {
	Items      []Installment `json:"itens"`
	Total      int           `json:"total"`
	Page       int           `json:"pagina"`
	PageSize   int           `json:"tamanho_pagina"`
}

// DRECategory is a DRE category entry.
type DRECategory struct {
	ID     string `json:"id"`
	Name   string `json:"nome"`
	Kind   string `json:"tipo"` // RECEITA / DESPESA
	Parent string `json:"categoria_pai,omitempty"`
}

// ── High-level operations ──────────────────────────────────────────

// ListReceivables fetches receivable installments in [from, to].
func (c *Client) ListReceivables(ctx context.Context, from, to time.Time) ([]Installment, error) {
	return c.listInstallments(ctx, "/v1/financeiro/contas-a-receber/parcelas", from, to)
}

// ListPayables fetches payable installments in [from, to].
func (c *Client) ListPayables(ctx context.Context, from, to time.Time) ([]Installment, error) {
	return c.listInstallments(ctx, "/v1/financeiro/contas-a-pagar/parcelas", from, to)
}

// ListDRECategories fetches the chart of accounts (DRE).
func (c *Client) ListDRECategories(ctx context.Context) ([]DRECategory, error) {
	var out struct {
		Items []DRECategory `json:"itens"`
	}
	if err := c.get(ctx, "/v1/financeiro/categorias-dre", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (c *Client) listInstallments(ctx context.Context, path string, from, to time.Time) ([]Installment, error) {
	const layout = "2006-01-02"
	var all []Installment
	page := 1
	for {
		q := url.Values{}
		q.Set("data_vencimento_inicio", from.Format(layout))
		q.Set("data_vencimento_fim", to.Format(layout))
		q.Set("pagina", fmt.Sprintf("%d", page))
		q.Set("tamanho_pagina", "100")

		var pg InstallmentPage
		if err := c.get(ctx, path, q, &pg); err != nil {
			return nil, err
		}
		all = append(all, pg.Items...)
		if len(pg.Items) < 100 || len(all) >= pg.Total {
			break
		}
		page++
		if page > 50 {
			break // hard safety cap
		}
	}
	return all, nil
}
