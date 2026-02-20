// Package hostmetrics provides minimal, dependency-free metric emission via CloudWatch Embedded Metric Format (EMF).
package hostmetrics

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
	namespace = normalizeNamespace(namespace)
	if len(dimensions) == 0 || len(metrics) == 0 {
		return
	}

	dimKeys := dimensionKeys(dimensions)
	if len(dimKeys) == 0 {
		return
	}

	payload := map[string]any{}
	addDimensions(payload, dimensions, dimKeys)
	addProperties(payload, properties)
	metricDefs := addMetricDefs(payload, metrics)

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

func normalizeNamespace(namespace string) string {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return "lesser-host"
	}
	return namespace
}

func dimensionKeys(dimensions map[string]string) []string {
	keys := make([]string, 0, len(dimensions))
	for k := range dimensions {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func addDimensions(payload map[string]any, dimensions map[string]string, dimKeys []string) {
	for _, k := range dimKeys {
		payload[k] = strings.TrimSpace(dimensions[k])
	}
}

func addProperties(payload map[string]any, properties map[string]any) {
	for k, v := range properties {
		if strings.TrimSpace(k) == "" {
			continue
		}
		payload[k] = v
	}
}

func addMetricDefs(payload map[string]any, metrics []Metric) []map[string]any {
	metricDefs := make([]map[string]any, 0, len(metrics))
	for _, m := range metrics {
		name := strings.TrimSpace(m.Name)
		if name == "" {
			continue
		}
		if math.IsNaN(m.Value) || math.IsInf(m.Value, 0) {
			continue
		}

		unit := m.Unit
		if strings.TrimSpace(string(unit)) == "" {
			unit = UnitNone
		}

		metricDefs = append(metricDefs, map[string]any{
			"Name": name,
			"Unit": string(unit),
		})
		payload[name] = m.Value
	}
	return metricDefs
}
