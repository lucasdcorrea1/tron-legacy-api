package contaazul

import (
	"context"
	"sort"
	"time"
)

// BuildSummary computes the headline numbers for the dashboard for the period [from, to].
// Optionally compares against the previous equivalent window.
func (c *Client) BuildSummary(ctx context.Context, from, to time.Time, withCompare bool) (*Summary, error) {
	rec, err := c.ListReceivables(ctx, from, to)
	if err != nil {
		return nil, err
	}
	pay, err := c.ListPayables(ctx, from, to)
	if err != nil {
		return nil, err
	}

	revenue, openRec := sumPaidAndOpen(rec)
	expense, openPay := sumPaidAndOpen(pay)

	s := &Summary{
		PeriodStart:    from,
		PeriodEnd:      to,
		TotalRevenue:   revenue,
		TotalExpense:   expense,
		Profit:         revenue - expense,
		OpenReceivable: openRec,
		OpenPayable:    openPay,
	}

	if withCompare {
		span := to.Sub(from)
		prevTo := from.Add(-24 * time.Hour)
		prevFrom := prevTo.Add(-span)
		prevRec, _ := c.ListReceivables(ctx, prevFrom, prevTo)
		prevPay, _ := c.ListPayables(ctx, prevFrom, prevTo)
		prevRev, _ := sumPaidAndOpen(prevRec)
		prevExp, _ := sumPaidAndOpen(prevPay)
		s.Compare = &Comparison{
			RevenueChangePct: pctChange(prevRev, revenue),
			ExpenseChangePct: pctChange(prevExp, expense),
			ProfitChangePct:  pctChange(prevRev-prevExp, revenue-expense),
		}
	}
	return s, nil
}

// BuildCashflow returns monthly cashflow points across [from, to].
func (c *Client) BuildCashflow(ctx context.Context, from, to time.Time) ([]CashFlowPoint, error) {
	rec, err := c.ListReceivables(ctx, from, to)
	if err != nil {
		return nil, err
	}
	pay, err := c.ListPayables(ctx, from, to)
	if err != nil {
		return nil, err
	}

	type bucket struct{ rev, exp float64 }
	months := map[string]*bucket{}
	bump := func(m string, fn func(*bucket)) {
		b, ok := months[m]
		if !ok {
			b = &bucket{}
			months[m] = b
		}
		fn(b)
	}
	for _, it := range rec {
		if it.Status != "PAID" || it.PaidDate.IsZero() {
			continue
		}
		bump(it.PaidDate.Format("2006-01"), func(b *bucket) { b.rev += it.Value })
	}
	for _, it := range pay {
		if it.Status != "PAID" || it.PaidDate.IsZero() {
			continue
		}
		bump(it.PaidDate.Format("2006-01"), func(b *bucket) { b.exp += it.Value })
	}

	keys := make([]string, 0, len(months))
	for k := range months {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]CashFlowPoint, 0, len(keys))
	balance := 0.0
	for _, k := range keys {
		b := months[k]
		balance += b.rev - b.exp
		out = append(out, CashFlowPoint{
			Date:    k,
			Revenue: b.rev,
			Expense: b.exp,
			Balance: balance,
		})
	}
	return out, nil
}

// BuildCategoryBreakdown groups installments by DRE category.
func (c *Client) BuildCategoryBreakdown(ctx context.Context, from, to time.Time) ([]CategoryBreakdown, error) {
	rec, err := c.ListReceivables(ctx, from, to)
	if err != nil {
		return nil, err
	}
	pay, err := c.ListPayables(ctx, from, to)
	if err != nil {
		return nil, err
	}

	type key struct{ id, kind string }
	type entry struct {
		name   string
		amount float64
	}
	byKey := map[key]*entry{}
	addAll := func(items []Installment, kind string) {
		for _, it := range items {
			if it.Status != "PAID" {
				continue
			}
			k := key{it.CategoryID, kind}
			e, ok := byKey[k]
			if !ok {
				e = &entry{name: nonEmpty(it.CategoryName, "Sem categoria")}
				byKey[k] = e
			}
			e.amount += it.Value
		}
	}
	addAll(rec, "revenue")
	addAll(pay, "expense")

	// totals per kind to compute percentages
	totals := map[string]float64{}
	for k, e := range byKey {
		totals[k.kind] += e.amount
	}

	out := make([]CategoryBreakdown, 0, len(byKey))
	for k, e := range byKey {
		pct := 0.0
		if totals[k.kind] > 0 {
			pct = (e.amount / totals[k.kind]) * 100
		}
		out = append(out, CategoryBreakdown{
			CategoryID: k.id,
			Name:       e.name,
			Amount:     e.amount,
			Percentage: pct,
			Kind:       k.kind,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Amount > out[j].Amount })
	return out, nil
}

// BuildUpcoming returns next pending receivables/payables due within `days` from now.
func (c *Client) BuildUpcoming(ctx context.Context, days int) ([]UpcomingItem, error) {
	now := time.Now()
	to := now.AddDate(0, 0, days)
	rec, err := c.ListReceivables(ctx, now, to)
	if err != nil {
		return nil, err
	}
	pay, err := c.ListPayables(ctx, now, to)
	if err != nil {
		return nil, err
	}
	out := make([]UpcomingItem, 0, len(rec)+len(pay))
	for _, it := range rec {
		if it.Status == "PAID" || it.Status == "CANCELED" {
			continue
		}
		out = append(out, UpcomingItem{
			ID: it.ID, Description: it.Description, DueDate: it.DueDate,
			Amount: it.Value, Kind: "receivable", Status: it.Status,
		})
	}
	for _, it := range pay {
		if it.Status == "PAID" || it.Status == "CANCELED" {
			continue
		}
		out = append(out, UpcomingItem{
			ID: it.ID, Description: it.Description, DueDate: it.DueDate,
			Amount: it.Value, Kind: "payable", Status: it.Status,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DueDate.Before(out[j].DueDate) })
	return out, nil
}

// ── Aggregator-local DTOs (independent from models package) ────────

type Summary struct {
	PeriodStart    time.Time   `json:"period_start"`
	PeriodEnd      time.Time   `json:"period_end"`
	TotalRevenue   float64     `json:"total_revenue"`
	TotalExpense   float64     `json:"total_expense"`
	Profit         float64     `json:"profit"`
	OpenReceivable float64     `json:"open_receivable"`
	OpenPayable    float64     `json:"open_payable"`
	Compare        *Comparison `json:"compare,omitempty"`
}

type Comparison struct {
	RevenueChangePct float64 `json:"revenue_change_pct"`
	ExpenseChangePct float64 `json:"expense_change_pct"`
	ProfitChangePct  float64 `json:"profit_change_pct"`
}

type CashFlowPoint struct {
	Date    string  `json:"date"`
	Revenue float64 `json:"revenue"`
	Expense float64 `json:"expense"`
	Balance float64 `json:"balance"`
}

type CategoryBreakdown struct {
	CategoryID string  `json:"category_id"`
	Name       string  `json:"name"`
	Amount     float64 `json:"amount"`
	Percentage float64 `json:"percentage"`
	Kind       string  `json:"kind"`
}

type UpcomingItem struct {
	ID          string    `json:"id"`
	Description string    `json:"description"`
	DueDate     time.Time `json:"due_date"`
	Amount      float64   `json:"amount"`
	Kind        string    `json:"kind"`
	Status      string    `json:"status"`
}

// ── helpers ────────────────────────────────────────────────────────

func sumPaidAndOpen(items []Installment) (paid, open float64) {
	for _, it := range items {
		switch it.Status {
		case "PAID":
			paid += it.Value
		case "PENDING", "OVERDUE":
			open += it.Value
		}
	}
	return
}

func pctChange(prev, cur float64) float64 {
	if prev == 0 {
		if cur == 0 {
			return 0
		}
		return 100
	}
	return ((cur - prev) / prev) * 100
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
