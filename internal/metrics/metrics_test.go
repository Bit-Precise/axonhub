package metrics

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

func TestLLMMetricsRecordLifecycleUsageAndPerformance(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	previous := Metrics
	t.Cleanup(func() { Metrics = previous })

	require.NoError(t, SetupMetrics(provider, "axonhub-test"))
	attrs := RequestAttributes{
		ProjectID:      1,
		ChannelID:      2,
		RequestModelID: "requested-model",
		ModelID:        "actual-model",
		APIKeyID:       3,
		UserID:         4,
		Source:         "api",
		Format:         "openai/chat_completions",
		Stream:         true,
	}

	// Routing and context enrichment change between creation and completion.
	downstreamCreated := attrs
	downstreamCreated.ChannelID = 0
	downstreamCreated.ModelID = ""
	downstreamCreated.UserID = 0
	upstreamCreated := attrs
	upstreamCreated.RequestModelID = ""
	upstreamCreated.APIKeyID = 0
	upstreamCreated.UserID = 0
	upstreamCreated.Source = ""
	Metrics.RecordDownstreamRequestCreated(t.Context(), downstreamCreated)
	Metrics.RecordUpstreamRequestCreated(t.Context(), upstreamCreated)
	active := metricdata.ResourceMetrics{}
	require.NoError(t, reader.Collect(t.Context(), &active))
	assert.Equal(t, int64(1), metricCounterValue(t, active, "axonhub_downstream_active_requests"))
	assert.Equal(t, int64(1), metricCounterValue(t, active, "axonhub_upstream_active_requests"))
	Metrics.RecordDownstreamRequestCompleted(t.Context(), attrs, "completed")
	Metrics.RecordUpstreamRequestCompleted(t.Context(), attrs, "failed")
	cost := 0.125
	Metrics.RecordLLMUsage(t.Context(), attrs, 100, 40, 20, 5, &cost)
	ttft := 0.25
	Metrics.RecordUpstreamPerformance(t.Context(), attrs, "completed", 1.5, &ttft)

	rm := metricdata.ResourceMetrics{}
	require.NoError(t, reader.Collect(context.Background(), &rm))

	assert.Equal(t, int64(1), metricCounterValue(t, rm, "axonhub_downstream_requests_total"))
	assert.Equal(t, int64(0), metricCounterValue(t, rm, "axonhub_downstream_active_requests"))
	assert.Equal(t, int64(1), metricCounterValue(t, rm, "axonhub_upstream_requests_total"))
	assert.Equal(t, int64(0), metricCounterValue(t, rm, "axonhub_upstream_active_requests"))
	assert.Equal(t, int64(100), metricCounterValueByType(t, rm, "axonhub_llm_tokens_total", "input"))
	assert.Equal(t, int64(40), metricCounterValueByType(t, rm, "axonhub_llm_tokens_total", "output"))
	assert.Equal(t, int64(20), metricCounterValueByType(t, rm, "axonhub_llm_tokens_total", "cached"))
	assert.Equal(t, int64(5), metricCounterValueByType(t, rm, "axonhub_llm_tokens_total", "reasoning"))
	assert.InDelta(t, cost, metricFloatCounterValue(t, rm, "axonhub_llm_cost_total"), 1e-9)
	assert.Equal(t, uint64(1), metricHistogramCount(t, rm, "axonhub_upstream_request_duration_seconds"))
	assert.Equal(t, uint64(1), metricHistogramCount(t, rm, "axonhub_upstream_ttft_seconds"))
}

func metricCounterValue(t *testing.T, rm metricdata.ResourceMetrics, name string) int64 {
	t.Helper()
	for _, scope := range rm.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name != name {
				continue
			}
			data, ok := metric.Data.(metricdata.Sum[int64])
			require.True(t, ok)
			require.Len(t, data.DataPoints, 1)
			return data.DataPoints[0].Value
		}
	}
	t.Fatalf("metric %q not found", name)
	return 0
}

func metricCounterValueByType(t *testing.T, rm metricdata.ResourceMetrics, name, tokenType string) int64 {
	t.Helper()
	for _, scope := range rm.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name != name {
				continue
			}
			data, ok := metric.Data.(metricdata.Sum[int64])
			require.True(t, ok)
			for _, point := range data.DataPoints {
				value, ok := point.Attributes.Value(attribute.Key("token_type"))
				if ok && value.AsString() == tokenType {
					return point.Value
				}
			}
		}
	}
	t.Fatalf("metric %q with token_type=%q not found", name, tokenType)
	return 0
}

func metricFloatCounterValue(t *testing.T, rm metricdata.ResourceMetrics, name string) float64 {
	t.Helper()
	for _, scope := range rm.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name != name {
				continue
			}
			data, ok := metric.Data.(metricdata.Sum[float64])
			require.True(t, ok)
			require.Len(t, data.DataPoints, 1)
			return data.DataPoints[0].Value
		}
	}
	t.Fatalf("metric %q not found", name)
	return 0
}

func metricHistogramCount(t *testing.T, rm metricdata.ResourceMetrics, name string) uint64 {
	t.Helper()
	for _, scope := range rm.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name != name {
				continue
			}
			data, ok := metric.Data.(metricdata.Histogram[float64])
			require.True(t, ok)
			require.Len(t, data.DataPoints, 1)
			return data.DataPoints[0].Count
		}
	}
	t.Fatalf("metric %q not found", name)
	return 0
}

func TestRecordChatRequestAndTokens(t *testing.T) {
	previousMetrics := Metrics
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	t.Cleanup(func() {
		Metrics = previousMetrics
		require.NoError(t, provider.Shutdown(context.Background()))
	})

	require.NoError(t, SetupMetrics(provider, "test"))

	ctx := context.Background()

	Metrics.RecordChatRequest(ctx, "1", "channel-a", 0.5, true)
	Metrics.RecordChatRequest(ctx, "1", "channel-a", 1.5, false)
	Metrics.RecordChatRequest(ctx, "", "", 2, false)
	Metrics.RecordChatTokens(ctx, "1", "channel-a", 100)
	Metrics.RecordChatTokens(ctx, "1", "channel-a", 0)

	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &data))

	metricsByName := collectMetricsByName(t, data)

	requestCount := sumInt64(t, metricsByName, "chat_request_count")
	require.Equal(t, int64(3), requestCount.total)
	channelA, ok := requestCount.byLabel["channel_id=1|channel_name=channel-a"]
	require.True(t, ok, "expected a data point attributed to channel-a")
	require.Equal(t, int64(2), channelA)

	require.Equal(t, int64(2), sumInt64(t, metricsByName, "chat_failure_count").total)
	require.Equal(t, int64(1), sumInt64(t, metricsByName, "chat_success_count").total)

	tokens := sumInt64(t, metricsByName, "chat_token_count")
	require.Equal(t, int64(100), tokens.total)
	require.Equal(t, int64(100), tokens.byLabel["channel_id=1|channel_name=channel-a"])

	duration := histogram(t, metricsByName, "chat_request_duration_seconds")
	require.Equal(t, uint64(3), duration.count)
	require.InDelta(t, 4.0, duration.sum, 1e-9)
}

type int64Sum struct {
	total   int64
	byLabel map[string]int64
}

func sumInt64(t *testing.T, metricsByName map[string]metricdata.Metrics, name string) int64Sum {
	t.Helper()

	m, ok := metricsByName[name]
	require.True(t, ok, "missing metric %s", name)

	sum, ok := m.Data.(metricdata.Sum[int64])
	require.True(t, ok, "metric %s is not an int64 sum", name)

	result := int64Sum{byLabel: map[string]int64{}}
	for _, dp := range sum.DataPoints {
		result.total += dp.Value
		result.byLabel[labelKey(dp.Attributes)] += dp.Value
	}

	return result
}

type histogramSummary struct {
	count uint64
	sum   float64
}

func histogram(t *testing.T, metricsByName map[string]metricdata.Metrics, name string) histogramSummary {
	t.Helper()

	m, ok := metricsByName[name]
	require.True(t, ok, "missing metric %s", name)

	hist, ok := m.Data.(metricdata.Histogram[float64])
	require.True(t, ok, "metric %s is not a float64 histogram", name)

	var result histogramSummary
	for _, dp := range hist.DataPoints {
		result.count += dp.Count
		result.sum += dp.Sum
	}

	return result
}

func collectMetricsByName(t *testing.T, data metricdata.ResourceMetrics) map[string]metricdata.Metrics {
	t.Helper()

	result := map[string]metricdata.Metrics{}
	for _, sm := range data.ScopeMetrics {
		for _, m := range sm.Metrics {
			result[m.Name] = m
		}
	}

	return result
}

func labelKey(attrs attribute.Set) string {
	parts := make([]string, 0, attrs.Len())
	for _, kv := range attrs.ToSlice() {
		parts = append(parts, string(kv.Key)+"="+kv.Value.Emit())
	}

	return strings.Join(parts, "|")
}

func TestEnabled(t *testing.T) {
	previousEnabled := enabled
	t.Cleanup(func() {
		enabled = previousEnabled
	})

	provider, err := NewProvider(Config{Enabled: false})
	require.NoError(t, err)
	require.False(t, Enabled())
	require.NoError(t, provider.Shutdown(context.Background()))

	provider, err = NewProvider(Config{
		Enabled:  true,
		Exporter: ExporterConfig{Type: "stdout"},
	})
	require.NoError(t, err)
	require.True(t, Enabled())
	require.NoError(t, provider.Shutdown(context.Background()))
}

func TestRecordChatRequestWithoutSetup(t *testing.T) {
	// The package-level default must be safe to use without SetupMetrics, as
	// happens in tests of packages that only record metrics.
	require.NotNil(t, Metrics)

	Metrics.RecordChatRequest(context.Background(), "1", "channel-a", 1, true)
	Metrics.RecordChatTokens(context.Background(), "1", "channel-a", 10)
}
