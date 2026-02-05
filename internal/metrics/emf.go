// Package metrics provides minimal, dependency-free metric emission via CloudWatch Embedded Metric Format (EMF).
package metrics

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"
)

// Unit is a CloudWatch metric unit string for EMF emission.
type Unit string

const (
	// UnitCount is a count unit.
	UnitCount Unit = "Count"
	// UnitMilliseconds is a milliseconds unit.
	UnitMilliseconds Unit = "Milliseconds"
	// UnitNone is an unspecified unit.
	UnitNone Unit = "None"
)

// Metric is a single metric value emitted in an EMF record.
type Metric struct {
	Name  string
	Unit  Unit
	Value float64
}

// Emit writes a CloudWatch Embedded Metric Format (EMF) JSON log line to stdout.
//
// Dimensions define the CloudWatch metric dimensions for this record. Keys must match the values emitted into
// the top-level JSON object.
func Emit(namespace string, dimensions map[string]string, metrics []Metric, properties map[string]any) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		namespace = "lesser-host"
	}

	if len(dimensions) == 0 || len(metrics) == 0 {
		return
	}

	dimKeys := make([]string, 0, len(dimensions))
	for k := range dimensions {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		dimKeys = append(dimKeys, k)
	}
	sort.Strings(dimKeys)
	if len(dimKeys) == 0 {
		return
	}

	metricDefs := make([]map[string]any, 0, len(metrics))
	payload := map[string]any{}

	for _, k := range dimKeys {
		payload[k] = strings.TrimSpace(dimensions[k])
	}
	for k, v := range properties {
		if strings.TrimSpace(k) == "" {
			continue
		}
		payload[k] = v
	}

	for _, m := range metrics {
		m.Name = strings.TrimSpace(m.Name)
		if m.Name == "" {
			continue
		}
		if math.IsNaN(m.Value) || math.IsInf(m.Value, 0) {
			continue
		}
		if strings.TrimSpace(string(m.Unit)) == "" {
			m.Unit = UnitNone
		}

		metricDefs = append(metricDefs, map[string]any{
			"Name": m.Name,
			"Unit": string(m.Unit),
		})
		payload[m.Name] = m.Value
	}

	if len(metricDefs) == 0 {
		return
	}

	payload["_aws"] = map[string]any{
		"Timestamp": time.Now().UnixMilli(),
		"CloudWatchMetrics": []any{map[string]any{
			"Namespace":  namespace,
			"Dimensions": [][]string{dimKeys},
			"Metrics":    metricDefs,
		}},
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintln(os.Stdout, string(b))
}
