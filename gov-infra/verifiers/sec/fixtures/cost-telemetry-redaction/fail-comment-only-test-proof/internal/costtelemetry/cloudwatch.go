//go:build ignore

package costtelemetry

type ServiceAttribution struct {
	Service    string  `json:"service"`
	MetricName string  `json:"metric_name"`
	Stat       string  `json:"stat"`
	Unit       string  `json:"unit"`
	Value      float64 `json:"value"`
}
