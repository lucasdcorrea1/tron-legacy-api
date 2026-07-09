// seed-conta-azul popula o sandbox da Conta Azul com dados de teste
// realistas para validar o dashboard do portal.
//
// Uso:
//
//	# token via flag
//	go run ./cmd/seed-conta-azul --token=<ACCESS_TOKEN>
//
//	# token via env
//	CONTA_AZUL_ACCESS_TOKEN=eyJ... go run ./cmd/seed-conta-azul
//
//	# parâmetros opcionais
//	go run ./cmd/seed-conta-azul --token=X --months=6 --per-month=4 --upcoming=8
//	go run ./cmd/seed-conta-azul --token=X --dry-run    # imprime payloads, não posta
//	go run ./cmd/seed-conta-azul --token=X --base=https://api-v2.contaazul.com
//
// O script é tolerante a falha por item — se um POST der 4xx, ele
// imprime o erro e segue. No final mostra um sumário (sucessos / falhas).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const defaultBase = "https://api-v2.contaazul.com"

// ── Payloads (best-effort schema; ajustar via feedback do servidor) ──

type createReceivableReq struct {
	Descricao       string  `json:"descricao"`
	DataVencimento  string  `json:"data_vencimento"`            // 2006-01-02
	DataCompetencia string  `json:"data_competencia,omitempty"` // 2006-01-02
	DataPagamento   string  `json:"data_pagamento,omitempty"`   // 2006-01-02 (se PAID)
	Valor           float64 `json:"valor"`
	CategoriaID     string  `json:"categoria_id,omitempty"`
	Status          string  `json:"status,omitempty"` // PENDING | PAID
	NumeroParcelas  int     `json:"numero_parcelas,omitempty"`
}

type createPayableReq = createReceivableReq

type categoria struct {
	ID         string `json:"id"`
	Nome       string `json:"nome"`
	Tipo       string `json:"tipo"`             // RECEITA / DESPESA
	EntradaDRE string `json:"entrada_dre,omitempty"`
	Pai        string `json:"categoria_pai,omitempty"`
}

type listCategoriasResp struct {
	Itens []categoria `json:"itens"`
}

// ── Realistic descriptions ──────────────────────────────────────

var revDescs = []string{
	"Mensalidade contábil — cliente %s",
	"Honorários contábeis %s",
	"Consultoria fiscal — %s",
	"Serviços de apuração — %s",
	"Assessoria tributária %s",
	"Folha de pagamento — %s",
	"Apuração de impostos %s",
	"Revisão fiscal — %s",
}

var expDescs = []string{
	"Aluguel do escritório — %s",
	"Salário equipe — folha %s",
	"Energia elétrica %s",
	"Internet + telefonia %s",
	"Software contábil — assinatura %s",
	"Material de escritório — %s",
	"Combustível visitas %s",
	"Manutenção predial %s",
	"Marketing digital — Ads %s",
	"Contador terceirizado %s",
}

var clientNames = []string{
	"ABC Ltda", "XYZ Comercial", "Padaria do João", "Boutique Maria",
	"Auto Peças Silva", "Restaurante Sabor", "Loja TechFix", "Studio Bella",
	"Construtora Norte", "Farmácia Vida", "Pet Shop Amigo", "Clínica Saúde+",
}

// ── Main ────────────────────────────────────────────────────────────

func main() {
	rand.Seed(time.Now().UnixNano())

	token := flag.String("token", os.Getenv("CONTA_AZUL_ACCESS_TOKEN"), "Conta Azul access_token (ou use --oauth)")
	base := flag.String("base", defaultBase, "Base URL da API")
	months := flag.Int("months", 6, "Quantos meses passados popular")
	perMonth := flag.Int("per-month", 5, "Itens por mês de cada tipo (receivable+payable)")
	upcoming := flag.Int("upcoming", 6, "Itens futuros pendentes (próximos 30 dias)")
	dryRun := flag.Bool("dry-run", false, "Imprime payloads sem postar")
	verbose := flag.Bool("v", false, "Imprime resposta completa de erros")

	// OAuth flow flags
	oauthMode := flag.Bool("oauth", false, "Faz o fluxo OAuth: abre browser, captura callback local, troca code por token")
	oauthPort := flag.Int("oauth-port", 8888, "Porta do servidor local de callback")
	oauthRedirect := flag.String("oauth-redirect", "", "Override do redirect_uri (default http://localhost:{port}/callback)")
	oauthManual := flag.Bool("oauth-manual", false, "Não sobe servidor — vc cola o code (URL ?code=XXX) no terminal")
	oauthClientID := flag.String("oauth-client-id", os.Getenv("CONTA_AZUL_CLIENT_ID"), "client_id (default = env CONTA_AZUL_CLIENT_ID)")
	oauthClientSecret := flag.String("oauth-client-secret", os.Getenv("CONTA_AZUL_CLIENT_SECRET"), "client_secret (default = env CONTA_AZUL_CLIENT_SECRET)")
	oauthAuthURL := flag.String("oauth-auth-url", "https://auth.contaazul.com/oauth2/authorize", "URL de autorização")
	oauthTokenURL := flag.String("oauth-token-url", "https://auth.contaazul.com/oauth2/token", "URL de troca de token")
	oauthScope := flag.String("oauth-scope", "openid profile aws.cognito.signin.user.admin", "Scope OAuth")
	tokenOnly := flag.Bool("token-only", false, "Roda só o OAuth, imprime os tokens e sai (não seeda)")

	flag.Parse()

	if *oauthMode {
		newToken, refresh, err := runOAuthFlow(oauthFlowConfig{
			ClientID:     *oauthClientID,
			ClientSecret: *oauthClientSecret,
			AuthURL:      *oauthAuthURL,
			TokenURL:     *oauthTokenURL,
			Scope:        *oauthScope,
			Port:         *oauthPort,
			RedirectURI:  *oauthRedirect,
			ManualCode:   *oauthManual,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERRO OAuth: %v\n", err)
			os.Exit(1)
		}
		fmt.Println()
		fmt.Println("════════════════════════════════════════")
		fmt.Println(" ✓ OAuth concluído")
		fmt.Println("════════════════════════════════════════")
		fmt.Println("access_token:", newToken)
		if refresh != "" {
			fmt.Println("refresh_token:", refresh)
		}
		fmt.Println()
		if *tokenOnly {
			return
		}
		*token = newToken
	}

	if *token == "" {
		fmt.Fprintln(os.Stderr, "ERRO: --token, env CONTA_AZUL_ACCESS_TOKEN ou --oauth é obrigatório")
		flag.Usage()
		os.Exit(1)
	}

	s := &seeder{
		token:   *token,
		base:    strings.TrimRight(*base, "/"),
		dryRun:  *dryRun,
		verbose: *verbose,
		http:    &http.Client{Timeout: 30 * time.Second},
	}

	// 1) Resolver categorias (em dry-run, segue sem)
	var revCats, expCats []categoria
	if !*dryRun {
		cats, err := s.fetchCategorias()
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERRO ao listar categorias: %v\n", err)
			os.Exit(1)
		}
		revCats, expCats = splitCats(cats)
		fmt.Printf("→ %d categorias encontradas (%d receita, %d despesa)\n", len(cats), len(revCats), len(expCats))
	} else {
		fmt.Println("[DRY] pulando GET de categorias")
	}

	if len(revCats) == 0 || len(expCats) == 0 {
		fmt.Println("⚠ sandbox sem categorias suficientes — vou postar sem categoria_id")
	}

	stats := &runStats{}

	// 2) Receivables (PAGAS) espalhadas pelos últimos N meses
	for m := *months - 1; m >= 0; m-- {
		for i := 0; i < *perMonth; i++ {
			due := monthOffset(-m, randDayOfMonth(m, i))
			paid := due.AddDate(0, 0, -rand.Intn(5)) // pago alguns dias antes do vencimento
			payload := createReceivableReq{
				Descricao:       fmt.Sprintf(pick(revDescs), pick(clientNames)),
				DataVencimento:  due.Format("2006-01-02"),
				DataCompetencia: due.AddDate(0, 0, -10).Format("2006-01-02"),
				DataPagamento:   paid.Format("2006-01-02"),
				Valor:           round2(800 + rand.Float64()*11200),
				CategoriaID:     pickID(revCats),
				Status:          "PAID",
				NumeroParcelas:  1,
			}
			s.post("/v1/financeiro/contas-a-receber", payload, stats, "receivable-pago")
		}
	}

	// 3) Payables (PAGAS) espalhadas pelos últimos N meses
	for m := *months - 1; m >= 0; m-- {
		for i := 0; i < *perMonth; i++ {
			due := monthOffset(-m, randDayOfMonth(m, i))
			paid := due.AddDate(0, 0, -rand.Intn(3))
			payload := createPayableReq{
				Descricao:       fmt.Sprintf(pick(expDescs), monthLabel(due)),
				DataVencimento:  due.Format("2006-01-02"),
				DataCompetencia: due.AddDate(0, 0, -10).Format("2006-01-02"),
				DataPagamento:   paid.Format("2006-01-02"),
				Valor:           round2(300 + rand.Float64()*5700),
				CategoriaID:     pickID(expCats),
				Status:          "PAID",
				NumeroParcelas:  1,
			}
			s.post("/v1/financeiro/contas-a-pagar", payload, stats, "payable-pago")
		}
	}

	// 4) Upcoming (PENDENTES) — futuros próximos 30 dias
	for i := 0; i < *upcoming; i++ {
		due := time.Now().AddDate(0, 0, 1+rand.Intn(30))
		isReceivable := rand.Intn(2) == 0
		if isReceivable {
			payload := createReceivableReq{
				Descricao:      fmt.Sprintf(pick(revDescs), pick(clientNames)),
				DataVencimento: due.Format("2006-01-02"),
				Valor:          round2(1000 + rand.Float64()*8000),
				CategoriaID:    pickID(revCats),
				Status:         "PENDING",
				NumeroParcelas: 1,
			}
			s.post("/v1/financeiro/contas-a-receber", payload, stats, "receivable-pendente")
		} else {
			payload := createPayableReq{
				Descricao:      fmt.Sprintf(pick(expDescs), monthLabel(due)),
				DataVencimento: due.Format("2006-01-02"),
				Valor:          round2(200 + rand.Float64()*2800),
				CategoriaID:    pickID(expCats),
				Status:         "PENDING",
				NumeroParcelas: 1,
			}
			s.post("/v1/financeiro/contas-a-pagar", payload, stats, "payable-pendente")
		}
	}

	// 5) Sumário
	fmt.Println()
	fmt.Println("════════════════════════════════════════")
	fmt.Printf(" ✓ %d sucessos   ✗ %d falhas\n", stats.ok, stats.err)
	fmt.Println("════════════════════════════════════════")
	if stats.err > 0 {
		fmt.Println("Erros mais comuns:")
		for msg, n := range stats.errCounts {
			fmt.Printf("  [%dx] %s\n", n, truncate(msg, 200))
		}
		fmt.Println()
		fmt.Println("Se todos falharam com o mesmo erro de schema:")
		fmt.Println("  1) Rode com -v pra ver o body completo da resposta")
		fmt.Println("  2) O ajuste é só editar os structs createReceivableReq/createPayableReq")
		fmt.Println("  3) Schema correto está em devportal.contaazul.com/docs")
		os.Exit(1)
	}
	fmt.Println("Tudo certo. Abre o dashboard pra ver os números.")
}

// ══════════════════════════════════════════════════════════════════
// OAuth helper — Authorization Code flow with localhost callback
// ══════════════════════════════════════════════════════════════════

type oauthFlowConfig struct {
	ClientID, ClientSecret, AuthURL, TokenURL, Scope string
	Port                                             int
	RedirectURI                                      string
	ManualCode                                       bool
}

func runOAuthFlow(c oauthFlowConfig) (accessToken, refreshToken string, err error) {
	if c.ClientID == "" || c.ClientSecret == "" {
		return "", "", errors.New("client_id e client_secret são obrigatórios (--oauth-client-id/--oauth-client-secret ou env)")
	}
	redirectURI := c.RedirectURI
	if redirectURI == "" {
		redirectURI = fmt.Sprintf("http://localhost:%d/callback", c.Port)
	}

	// Build authorize URL.
	q := url.Values{}
	q.Set("client_id", c.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", c.Scope)
	q.Set("state", fmt.Sprintf("seed-%d", time.Now().Unix()))
	authURL := strings.TrimRight(c.AuthURL, "/") + "?" + q.Encode()

	fmt.Println("Abra a URL de autorização no seu browser:")
	fmt.Println()
	fmt.Println("  ", authURL)
	fmt.Println()
	fmt.Println("⚠ O redirect_uri usado é:", redirectURI)
	fmt.Println("  Esse exato valor PRECISA estar cadastrado no devportal.contaazul.com → seu app → Redirect URIs.")
	fmt.Println()

	var code string

	if c.ManualCode {
		fmt.Println("Após autorizar, o browser vai redirecionar para uma URL com ?code=XXX")
		fmt.Println("Cole AQUI só o valor do code (sem o &state=...), depois Enter:")
		fmt.Print("code> ")
		var typed string
		if _, err := fmt.Scanln(&typed); err != nil {
			return "", "", fmt.Errorf("ler code do stdin: %w", err)
		}
		code = strings.TrimSpace(typed)
		// Extra: aceita também a URL completa colada
		if strings.Contains(code, "code=") {
			if u, err := url.Parse(code); err == nil {
				if c2 := u.Query().Get("code"); c2 != "" {
					code = c2
				}
			}
		}
		if code == "" {
			return "", "", errors.New("code vazio")
		}
	} else {
		// Start local server to capture the callback.
		codeCh := make(chan string, 1)
		errCh := make(chan error, 1)

		mux := http.NewServeMux()
		mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			if e := q.Get("error"); e != "" {
				desc := q.Get("error_description")
				fmt.Fprintf(w, "Erro autorização: %s — %s\nPode fechar essa janela.", e, desc)
				errCh <- fmt.Errorf("authorize error: %s: %s", e, desc)
				return
			}
			cb := q.Get("code")
			if cb == "" {
				fmt.Fprintln(w, "Faltou parâmetro 'code'.")
				errCh <- errors.New("callback sem code")
				return
			}
			fmt.Fprintln(w, "✓ Autorizado! Pode fechar essa aba — retorne ao terminal.")
			codeCh <- cb
		})
		srv := &http.Server{
			Addr:    fmt.Sprintf("127.0.0.1:%d", c.Port),
			Handler: mux,
		}
		go func() {
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("local server: %w", err)
			}
		}()
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = srv.Shutdown(ctx)
		}()

		_ = openBrowser(authURL)
		fmt.Println("Aguardando callback…")
		select {
		case code = <-codeCh:
			fmt.Println("✓ Code recebido, trocando por token…")
		case err := <-errCh:
			return "", "", err
		case <-time.After(5 * time.Minute):
			return "", "", errors.New("timeout aguardando autorização (5min)")
		}
	}

	// Exchange code for token.
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("token exchange: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", "", fmt.Errorf("token http %d: %s", resp.StatusCode, string(body))
	}
	var tr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", "", fmt.Errorf("decode token: %w", err)
	}
	return tr.AccessToken, tr.RefreshToken, nil
}

func openBrowser(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	case "darwin":
		cmd = exec.Command("open", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	return cmd.Start()
}

// ── seeder ──────────────────────────────────────────────────────────

type seeder struct {
	token   string
	base    string
	dryRun  bool
	verbose bool
	http    *http.Client
}

type runStats struct {
	ok        int
	err       int
	errCounts map[string]int
}

func (s *seeder) post(path string, payload any, stats *runStats, kind string) {
	if stats.errCounts == nil {
		stats.errCounts = map[string]int{}
	}

	if s.dryRun {
		b, _ := json.MarshalIndent(payload, "", "  ")
		fmt.Printf("[DRY] POST %s\n%s\n\n", path, b)
		stats.ok++
		return
	}

	body, err := json.Marshal(payload)
	if err != nil {
		stats.err++
		stats.errCounts[err.Error()]++
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.base+path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := s.http.Do(req)
	if err != nil {
		stats.err++
		stats.errCounts[err.Error()]++
		fmt.Printf("✗ %s — network: %v\n", kind, err)
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		stats.ok++
		fmt.Printf("✓ %s\n", kind)
		if s.verbose {
			fmt.Printf("   %s\n", truncate(string(respBody), 200))
		}
		return
	}

	stats.err++
	key := fmt.Sprintf("HTTP %d", resp.StatusCode)
	if s.verbose {
		key = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(respBody))
	} else if len(respBody) > 0 && len(respBody) < 240 {
		key = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	stats.errCounts[key]++
	fmt.Printf("✗ %s — %s\n", kind, key)
}

func (s *seeder) fetchCategorias() ([]categoria, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.base+"/v1/financeiro/categorias-dre", nil)
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Accept", "application/json")

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	// Tentar primeiro o shape com {itens: [...]}
	var wrap listCategoriasResp
	if err := json.Unmarshal(body, &wrap); err == nil && len(wrap.Itens) > 0 {
		return wrap.Itens, nil
	}
	// Senão tentar array no root
	var arr []categoria
	if err := json.Unmarshal(body, &arr); err != nil {
		return nil, errors.New("formato de categorias inesperado: " + truncate(string(body), 200))
	}
	return arr, nil
}

// ── Helpers ─────────────────────────────────────────────────────────

func splitCats(all []categoria) (rev, exp []categoria) {
	for _, c := range all {
		switch strings.ToUpper(c.Tipo) {
		case "RECEITA":
			rev = append(rev, c)
		case "DESPESA":
			exp = append(exp, c)
		}
	}
	return
}

func pick[T any](items []T) T {
	var zero T
	if len(items) == 0 {
		return zero
	}
	return items[rand.Intn(len(items))]
}

func pickID(cats []categoria) string {
	if len(cats) == 0 {
		return ""
	}
	return cats[rand.Intn(len(cats))].ID
}

func round2(v float64) float64 {
	return float64(int(v*100)) / 100
}

func monthOffset(monthsBack, day int) time.Time {
	now := time.Now()
	t := time.Date(now.Year(), now.Month()+time.Month(monthsBack), 1, 0, 0, 0, 0, now.Location())
	last := time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, t.Location()).Day()
	if day < 1 {
		day = 1
	}
	if day > last {
		day = last
	}
	return time.Date(t.Year(), t.Month(), day, 0, 0, 0, 0, t.Location())
}

func randDayOfMonth(monthIdx, itemIdx int) int {
	// Distribui dias 1..28 com determinismo amigável
	return 1 + ((monthIdx*7 + itemIdx*5) % 28)
}

func monthLabel(t time.Time) string {
	months := []string{"jan", "fev", "mar", "abr", "mai", "jun", "jul", "ago", "set", "out", "nov", "dez"}
	return fmt.Sprintf("%s/%s", months[int(t.Month())-1], t.Format("06"))
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
