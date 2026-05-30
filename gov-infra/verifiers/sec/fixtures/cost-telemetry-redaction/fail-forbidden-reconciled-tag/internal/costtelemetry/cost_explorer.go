//go:build ignore

package costtelemetry

type ReconciledCostEntry struct {
	Date      string               `json:"date"`
	Service   string               `json:"service"`
	AccountID string               `json:"account_id"`
	Cost      float64              `json:"cost"`
	Currency  string               `json:"currency"`
	Metrics   []ServiceAttribution `json:"metrics,omitempty"`
}
