package deploy_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

type logMetric struct {
	Name             string            `json:"name"`
	Description      string            `json:"description"`
	Filter           string            `json:"filter"`
	MetricDescriptor metricDescriptor  `json:"metricDescriptor"`
	LabelExtractors  map[string]string `json:"labelExtractors"`
	ValueExtractor   string            `json:"valueExtractor"`
	BucketOptions    json.RawMessage   `json:"bucketOptions"`
}

type metricDescriptor struct {
	MetricKind string            `json:"metricKind"`
	ValueType  string            `json:"valueType"`
	Unit       string            `json:"unit"`
	Labels     []labelDescriptor `json:"labels"`
}

type labelDescriptor struct {
	Key       string `json:"key"`
	ValueType string `json:"valueType"`
}

type alertPolicy struct {
	Name          string            `json:"name"`
	DisplayName   string            `json:"displayName"`
	Combiner      string            `json:"combiner"`
	Enabled       bool              `json:"enabled"`
	UserLabels    map[string]string `json:"userLabels"`
	Documentation documentation     `json:"documentation"`
	AlertStrategy alertStrategy     `json:"alertStrategy"`
	Conditions    []alertCondition  `json:"conditions"`
}

type documentation struct {
	MIMEType string `json:"mimeType"`
	Content  string `json:"content"`
}

type alertStrategy struct {
	AutoClose string `json:"autoClose"`
}

type alertCondition struct {
	Name               string          `json:"name"`
	DisplayName        string          `json:"displayName"`
	ConditionThreshold metricThreshold `json:"conditionThreshold"`
}

type metricThreshold struct {
	Filter                  string          `json:"filter"`
	Aggregations            []aggregation   `json:"aggregations"`
	DenominatorFilter       string          `json:"denominatorFilter"`
	DenominatorAggregations []aggregation   `json:"denominatorAggregations"`
	Comparison              string          `json:"comparison"`
	ThresholdValue          float64         `json:"thresholdValue"`
	Duration                string          `json:"duration"`
	EvaluationMissingData   string          `json:"evaluationMissingData"`
	ForecastOptions         json.RawMessage `json:"forecastOptions"`
	Trigger                 json.RawMessage `json:"trigger"`
}

type aggregation struct {
	AlignmentPeriod    string   `json:"alignmentPeriod"`
	PerSeriesAligner   string   `json:"perSeriesAligner"`
	CrossSeriesReducer string   `json:"crossSeriesReducer"`
	GroupByFields      []string `json:"groupByFields"`
}

type expectedMetric struct {
	signal         string
	valueExtractor string
	valueType      string
}

type expectedAlert struct {
	metric    string
	threshold float64
	duration  string
}

func TestCallCenterLogMetricDefinitionsAreBoundedAndComplete(t *testing.T) {
	directory := deployDirectory(t)
	metrics := decodeStrict[[]logMetric](
		t,
		filepath.Join(directory, "observability", "log-metrics.json"),
	)
	expected := expectedLogMetrics()
	if len(metrics) != len(expected) {
		t.Fatalf("log metric count = %d, want %d", len(metrics), len(expected))
	}
	allowedLabels := []string{
		"action",
		"cause",
		"metric_contract",
		"outcome",
		"failure_stage",
		"revision",
		"route",
		"runtime_role",
	}
	seen := make(map[string]bool, len(metrics))
	for _, metric := range metrics {
		want, ok := expected[metric.Name]
		if !ok {
			t.Errorf("unexpected log metric %q", metric.Name)
			continue
		}
		if seen[metric.Name] {
			t.Errorf("duplicate log metric %q", metric.Name)
		}
		seen[metric.Name] = true
		if metric.Description == "" ||
			!strings.Contains(metric.Filter, `jsonPayload.msg="call_center_metric"`) ||
			!strings.Contains(metric.Filter, `jsonPayload.metric_contract="1"`) ||
			!strings.Contains(
				metric.Filter,
				`jsonPayload.metric="`+want.signal+`"`,
			) {
			t.Errorf("%s filter does not select its fixed contract: %s", metric.Name, metric.Filter)
		}
		if !strings.Contains(metric.Filter, `resource.type="cloud_run_`) {
			t.Errorf("%s filter omits Cloud Run resource bound", metric.Name)
		}
		if metric.MetricDescriptor.MetricKind != "DELTA" ||
			metric.MetricDescriptor.ValueType != want.valueType {
			t.Errorf(
				"%s descriptor = %s/%s, want DELTA/%s",
				metric.Name,
				metric.MetricDescriptor.MetricKind,
				metric.MetricDescriptor.ValueType,
				want.valueType,
			)
		}
		labels := make([]string, 0, len(metric.MetricDescriptor.Labels))
		for _, label := range metric.MetricDescriptor.Labels {
			labels = append(labels, label.Key)
			if label.ValueType != "STRING" || !slices.Contains(allowedLabels, label.Key) {
				t.Errorf("%s has unbounded label %#v", metric.Name, label)
			}
			if metric.LabelExtractors[label.Key] !=
				"EXTRACT(jsonPayload."+label.Key+")" {
				t.Errorf("%s extractor for %s is not fixed", metric.Name, label.Key)
			}
		}
		if len(metric.LabelExtractors) != len(labels) {
			t.Errorf("%s descriptor/extractor label counts differ", metric.Name)
		}
		if metric.ValueExtractor != want.valueExtractor {
			t.Errorf(
				"%s value extractor = %q, want %q",
				metric.Name,
				metric.ValueExtractor,
				want.valueExtractor,
			)
		}
		if want.valueType == "DISTRIBUTION" && len(metric.BucketOptions) == 0 {
			t.Errorf("%s distribution omits bucket options", metric.Name)
		}
		if want.valueType == "INT64" &&
			(metric.ValueExtractor != "" || len(metric.BucketOptions) != 0) {
			t.Errorf("%s counter contains distribution fields", metric.Name)
		}
	}
}

func TestCallCenterAlertPoliciesMatchInitialOperatingThresholds(t *testing.T) {
	directory := deployDirectory(t)
	metrics := decodeStrict[[]logMetric](
		t,
		filepath.Join(directory, "observability", "log-metrics.json"),
	)
	metricNames := make(map[string]bool, len(metrics))
	for _, metric := range metrics {
		metricNames[metric.Name] = true
	}
	policies := decodeStrict[[]alertPolicy](
		t,
		filepath.Join(directory, "observability", "alert-policies.json"),
	)
	expected := expectedAlerts()
	seenPolicies := make(map[string]bool, len(policies))
	seenPolicyKeys := make(map[string]bool, len(policies))
	seenConditions := make(map[string]bool, len(expected))
	metricTypePattern := regexp.MustCompile(
		`metric\.type="logging\.googleapis\.com/user/([a-z0-9_]+)"`,
	)
	policyKeyPattern := regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	for _, policy := range policies {
		if policy.Name != "" {
			t.Errorf("%s contains provider-assigned name", policy.DisplayName)
		}
		if policy.DisplayName == "" || seenPolicies[policy.DisplayName] {
			t.Errorf("alert policy display name is empty or duplicate: %q", policy.DisplayName)
		}
		seenPolicies[policy.DisplayName] = true
		policyKey := policy.UserLabels["acuity_policy"]
		if !policyKeyPattern.MatchString(policyKey) ||
			seenPolicyKeys[policyKey] {
			t.Errorf("%s has empty, unsafe, or duplicate policy key %q", policy.DisplayName, policyKey)
		}
		seenPolicyKeys[policyKey] = true
		if policy.UserLabels["acuity_contract"] != "call_center_v1" ||
			policy.Documentation.MIMEType != "text/markdown" ||
			policy.Documentation.Content == "" ||
			policy.AlertStrategy.AutoClose == "" ||
			len(policy.Conditions) == 0 ||
			len(policy.Conditions) > 6 {
			t.Errorf("%s omits bounded policy metadata", policy.DisplayName)
		}
		if policy.Combiner != "OR" &&
			policy.Combiner != "AND_WITH_MATCHING_RESOURCE" {
			t.Errorf("%s has unsupported combiner %q", policy.DisplayName, policy.Combiner)
		}
		for _, condition := range policy.Conditions {
			if condition.Name != "" || seenConditions[condition.DisplayName] {
				t.Errorf("condition name is provider-assigned, empty, or duplicate: %q", condition.DisplayName)
			}
			seenConditions[condition.DisplayName] = true
			want, ok := expected[condition.DisplayName]
			if !ok {
				t.Errorf("unexpected alert condition %q", condition.DisplayName)
				continue
			}
			threshold := condition.ConditionThreshold
			matches := metricTypePattern.FindStringSubmatch(threshold.Filter)
			if len(matches) != 2 || matches[1] != want.metric ||
				!metricNames[want.metric] {
				t.Errorf("%s references unknown metric: %s", condition.DisplayName, threshold.Filter)
			}
			if !strings.Contains(threshold.Filter, `resource.type="cloud_run_`) {
				t.Errorf("%s filter omits monitored resource type", condition.DisplayName)
			}
			if strings.Contains(threshold.Filter, " OR metric.label.") {
				t.Errorf(
					"%s filter mixes metric-label AND/OR restrictions rejected by Cloud Monitoring: %s",
					condition.DisplayName,
					threshold.Filter,
				)
			}
			if threshold.Comparison != "COMPARISON_GT" ||
				threshold.ThresholdValue != want.threshold ||
				threshold.Duration != want.duration {
				t.Errorf(
					"%s threshold = %s %v for %s, want GT %v for %s",
					condition.DisplayName,
					threshold.Comparison,
					threshold.ThresholdValue,
					threshold.Duration,
					want.threshold,
					want.duration,
				)
			}
			assertDurationMultipleOfMinute(t, condition.DisplayName, threshold.Duration, true)
			for _, aggregation := range threshold.Aggregations {
				assertDurationMultipleOfMinute(
					t,
					condition.DisplayName+" alignment",
					aggregation.AlignmentPeriod,
					false,
				)
				if aggregation.PerSeriesAligner == "" {
					t.Errorf("%s omits per-series aligner", condition.DisplayName)
				}
			}
			if threshold.DenominatorFilter != "" {
				denominatorMatches := metricTypePattern.FindStringSubmatch(
					threshold.DenominatorFilter,
				)
				if len(denominatorMatches) != 2 ||
					denominatorMatches[1] != want.metric ||
					len(threshold.DenominatorAggregations) != len(threshold.Aggregations) {
					t.Errorf("%s ratio numerator/denominator do not align", condition.DisplayName)
				}
			}
		}
	}
	if len(seenConditions) != len(expected) {
		t.Errorf("alert condition count = %d, want %d", len(seenConditions), len(expected))
	}

	documentation, err := os.ReadFile(filepath.Join(
		directory,
		"observability",
		"README.md",
	))
	if err != nil {
		t.Fatalf("read observability deployment documentation: %v", err)
	}
	for displayName := range expected {
		if !bytes.Contains(documentation, []byte(displayName)) {
			t.Errorf("observability documentation omits %q", displayName)
		}
	}
}

func TestProductionDeployProfileConsumesCheckedContract(t *testing.T) {
	directory := deployDirectory(t)
	script, err := os.ReadFile(filepath.Join(
		directory,
		"cloud-run-commands.example.sh",
	))
	if err != nil {
		t.Fatalf("read Cloud Run deployment script: %v", err)
	}
	for _, required := range []string{
		"ACUITY_DEPLOYMENT_PROFILE",
		"USABLE_DATABASE_CONNECTIONS",
		"render-production-runtime-contract.mjs",
		`--min "$minimum"`,
		`--max "$maximum"`,
		`DATABASE_POOL_MAX=${pool}`,
	} {
		if !bytes.Contains(script, []byte(required)) {
			t.Errorf("production deployment script omits %q", required)
		}
	}
	if bytes.Contains(script, []byte("--min-instances")) ||
		bytes.Contains(script, []byte("--max-instances")) {
		t.Error("deployment script uses revision-scoped instance bounds")
	}
}

func TestBackendAvailabilitySLOAndBurnPoliciesAreExplicit(t *testing.T) {
	directory := deployDirectory(t)
	type serviceDefinition struct {
		ID          string         `json:"id"`
		DisplayName string         `json:"displayName"`
		Custom      map[string]any `json:"custom"`
	}
	service := decodeStrict[serviceDefinition](
		t,
		filepath.Join(directory, "observability", "backend-service.json"),
	)
	if service.ID != "acuity-portal-backend" ||
		service.DisplayName == "" || service.Custom == nil {
		t.Fatalf("backend service definition = %#v", service)
	}
	type sloIndicator struct {
		RequestBased struct {
			GoodTotalRatio struct {
				GoodServiceFilter  string `json:"goodServiceFilter"`
				TotalServiceFilter string `json:"totalServiceFilter"`
			} `json:"goodTotalRatio"`
		} `json:"requestBased"`
	}
	type sloDefinition struct {
		ID                    string       `json:"id"`
		DisplayName           string       `json:"displayName"`
		Goal                  float64      `json:"goal"`
		RollingPeriod         string       `json:"rollingPeriod"`
		ServiceLevelIndicator sloIndicator `json:"serviceLevelIndicator"`
	}
	slo := decodeStrict[sloDefinition](
		t,
		filepath.Join(directory, "observability", "backend-availability-slo.json"),
	)
	if slo.ID != "critical-read-availability" || slo.Goal != 0.999 ||
		slo.RollingPeriod != "2419200s" {
		t.Fatalf("backend availability SLO = %#v", slo)
	}
	good := slo.ServiceLevelIndicator.RequestBased.GoodTotalRatio.GoodServiceFilter
	total := slo.ServiceLevelIndicator.RequestBased.GoodTotalRatio.TotalServiceFilter
	if !strings.Contains(good, `metric.label.outcome="available"`) ||
		!strings.Contains(good, `metric.label.runtime_role="portal-api"`) ||
		!strings.Contains(good, `resource.type="cloud_run_revision"`) ||
		!strings.Contains(good, "acuity_backend_availability_count") ||
		!strings.Contains(total, "acuity_backend_availability_count") ||
		!strings.Contains(total, `metric.label.runtime_role="portal-api"`) ||
		!strings.Contains(total, `resource.type="cloud_run_revision"`) ||
		strings.Contains(total, "health/ready") {
		t.Fatalf("availability filters do not isolate the customer journey: good=%q total=%q", good, total)
	}

	type burnCondition struct {
		DisplayName        string `json:"displayName"`
		ConditionThreshold struct {
			Filter         string  `json:"filter"`
			Comparison     string  `json:"comparison"`
			ThresholdValue float64 `json:"thresholdValue"`
			Duration       string  `json:"duration"`
		} `json:"conditionThreshold"`
	}
	type burnPolicy struct {
		DisplayName   string            `json:"displayName"`
		Combiner      string            `json:"combiner"`
		Enabled       bool              `json:"enabled"`
		UserLabels    map[string]string `json:"userLabels"`
		Documentation documentation     `json:"documentation"`
		AlertStrategy alertStrategy     `json:"alertStrategy"`
		Conditions    []burnCondition   `json:"conditions"`
	}
	policies := decodeStrict[[]burnPolicy](
		t,
		filepath.Join(directory, "observability", "slo-burn-policies.json"),
	)
	if len(policies) != 2 {
		t.Fatalf("SLO burn policy count = %d, want 2", len(policies))
	}
	for _, policy := range policies {
		if policy.Combiner != "AND" || !policy.Enabled ||
			len(policy.Conditions) != 2 ||
			policy.UserLabels["acuity_slo"] != slo.ID ||
			policy.Documentation.MIMEType != "text/markdown" ||
			policy.Documentation.Content == "" ||
			policy.AlertStrategy.AutoClose == "" {
			t.Errorf("invalid multi-window burn policy: %#v", policy)
		}
		for _, condition := range policy.Conditions {
			threshold := condition.ConditionThreshold
			if !strings.Contains(threshold.Filter, "select_slo_burn_rate") ||
				!strings.Contains(threshold.Filter, slo.ID) ||
				threshold.Comparison != "COMPARISON_GT" ||
				threshold.ThresholdValue <= 1 || threshold.Duration == "" {
				t.Errorf("invalid burn condition: %#v", condition)
			}
		}
	}
}

func expectedLogMetrics() map[string]expectedMetric {
	counter := func(signal string) expectedMetric {
		return expectedMetric{signal: signal, valueType: "INT64"}
	}
	distribution := func(signal, field string) expectedMetric {
		return expectedMetric{
			signal:         signal,
			valueExtractor: "EXTRACT(jsonPayload." + field + ")",
			valueType:      "DISTRIBUTION",
		}
	}
	return map[string]expectedMetric{
		"acuity_backend_availability_count":                       counter("acuity_backend_availability"),
		"acuity_backend_availability_seconds":                     distribution("acuity_backend_availability", "seconds"),
		"acuity_call_center_webhook_acknowledgement_count":        counter("acuity_call_center_webhook_acknowledgement"),
		"acuity_call_center_webhook_acknowledgement_seconds":      distribution("acuity_call_center_webhook_acknowledgement", "seconds"),
		"acuity_call_center_receipt_queue_depth":                  distribution("acuity_call_center_receipt_queue", "depth"),
		"acuity_call_center_receipt_queue_oldest_age_seconds":     distribution("acuity_call_center_receipt_queue", "oldest_age_seconds"),
		"acuity_call_center_receipt_projection_retry_depth":       distribution("acuity_call_center_receipt_queue", "projection_retry_depth"),
		"acuity_call_center_receipt_related_fact_depth":           distribution("acuity_call_center_receipt_queue", "related_fact_depth"),
		"acuity_call_center_receipt_quarantine_depth":             distribution("acuity_call_center_receipt_queue", "quarantined_depth"),
		"acuity_call_center_receipt_processing_count":             counter("acuity_call_center_receipt_processing"),
		"acuity_call_center_receipt_queue_seconds":                distribution("acuity_call_center_receipt_processing", "queue_seconds"),
		"acuity_call_center_receipt_processing_seconds":           distribution("acuity_call_center_receipt_processing", "processing_seconds"),
		"acuity_call_center_provider_command_count":               counter("acuity_call_center_provider_command"),
		"acuity_call_center_provider_command_queue_seconds":       distribution("acuity_call_center_provider_command", "queue_seconds"),
		"acuity_call_center_provider_command_duration_seconds":    distribution("acuity_call_center_provider_command", "duration_seconds"),
		"acuity_call_center_database_pool_acquire_count":          counter("acuity_call_center_database_pool_acquire"),
		"acuity_call_center_database_pool_acquire_seconds":        distribution("acuity_call_center_database_pool_acquire", "seconds"),
		"acuity_backend_database_execution_count":                 counter("acuity_backend_database_execution"),
		"acuity_backend_database_execution_seconds":               distribution("acuity_backend_database_execution", "seconds"),
		"acuity_call_center_database_pool_acquired":               distribution("acuity_call_center_database_pool", "acquired"),
		"acuity_call_center_database_pool_idle":                   distribution("acuity_call_center_database_pool", "idle"),
		"acuity_call_center_database_pool_max":                    distribution("acuity_call_center_database_pool", "max"),
		"acuity_call_center_database_pool_saturation_ratio":       distribution("acuity_call_center_database_pool", "saturation_ratio"),
		"acuity_call_center_sse_stream_active":                    distribution("acuity_call_center_sse_stream", "active"),
		"acuity_call_center_sse_listener_disconnect_count":        counter("acuity_call_center_sse_listener"),
		"acuity_call_center_sse_listener_reconnect_failure_count": counter("acuity_call_center_sse_listener"),
		"acuity_call_center_staff_answer_count":                   counter("acuity_call_center_staff_answer"),
		"acuity_call_center_answer_to_bridge_seconds":             distribution("acuity_call_center_answer_to_bridge", "seconds"),
		"acuity_call_center_terminal_staff_occupancy":             distribution("acuity_call_center_terminal_cleanup", "staff_occupancy"),
	}
}

func expectedAlerts() map[string]expectedAlert {
	return map[string]expectedAlert{
		"Any unavailable webhook acknowledgement":                   {"acuity_call_center_webhook_acknowledgement_count", 0, "0s"},
		"Webhook acknowledgement p99 above one second":              {"acuity_call_center_webhook_acknowledgement_seconds", 1, "0s"},
		"Oldest receipt above 30 seconds":                           {"acuity_call_center_receipt_queue_oldest_age_seconds", 30, "60s"},
		"Receipt depth above 64 for five minutes":                   {"acuity_call_center_receipt_queue_depth", 64, "300s"},
		"Any quarantined provider receipt":                          {"acuity_call_center_receipt_quarantine_depth", 1, "0s"},
		"Dial command queue p95 above one second":                   {"acuity_call_center_provider_command_queue_seconds", 1, "60s"},
		"Rejected start ring-window command":                        {"acuity_call_center_provider_command_count", 0, "0s"},
		"Degraded caller audio after rejected stop ring-window":     {"acuity_call_center_provider_command_count", 0, "0s"},
		"Degraded caller audio after ambiguous stop ring-window":    {"acuity_call_center_provider_command_count", 0, "0s"},
		"Ambiguous service provider command":                        {"acuity_call_center_provider_command_count", 0, "0s"},
		"Ambiguous worker provider command":                         {"acuity_call_center_provider_command_count", 0, "0s"},
		"Service database acquisition timeout":                      {"acuity_call_center_database_pool_acquire_count", 0, "0s"},
		"Worker database acquisition timeout":                       {"acuity_call_center_database_pool_acquire_count", 0, "0s"},
		"Service database saturation above 0.8":                     {"acuity_call_center_database_pool_saturation_ratio", 0.8, "300s"},
		"Worker database saturation above 0.8":                      {"acuity_call_center_database_pool_saturation_ratio", 0.8, "300s"},
		"More than three listener disconnects in five minutes":      {"acuity_call_center_sse_listener_disconnect_count", 3, "0s"},
		"Any listener reconnect failure":                            {"acuity_call_center_sse_listener_reconnect_failure_count", 0, "0s"},
		"Lost Staff answer race ratio above 0.5":                    {"acuity_call_center_staff_answer_count", 0.5, "0s"},
		"At least ten Staff answers in five minutes":                {"acuity_call_center_staff_answer_count", 9, "0s"},
		"Answer-to-Bridge p95 above eight seconds":                  {"acuity_call_center_answer_to_bridge_seconds", 8, "60s"},
		"Any terminal Staff occupancy beyond reconciliation window": {"acuity_call_center_terminal_staff_occupancy", 1, "60s"},
	}
}

func deployDirectory(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate deploy contract test")
	}
	return filepath.Dir(filename)
}

func decodeStrict[T any](t *testing.T, path string) T {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var result T
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return result
}

func assertDurationMultipleOfMinute(
	t *testing.T,
	name, duration string,
	allowZero bool,
) {
	t.Helper()
	if !strings.HasSuffix(duration, "s") {
		t.Errorf("%s duration %q is not seconds", name, duration)
		return
	}
	seconds, err := strconv.Atoi(strings.TrimSuffix(duration, "s"))
	if err != nil || (!allowZero && seconds == 0) || seconds%60 != 0 {
		t.Errorf("%s duration %q is not a supported minute multiple", name, duration)
	}
}
