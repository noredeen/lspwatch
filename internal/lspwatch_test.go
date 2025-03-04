package internal

import (
	"testing"

	"github.com/noredeen/lspwatch/internal/config"
	"github.com/noredeen/lspwatch/internal/telemetry"
)

func TestNewMetricsExporter(t *testing.T) {
	t.Run("Datadog exporter", func(t *testing.T) {
		cfg := config.LspwatchConfig{
			Exporter: "datadog",
			Datadog: &config.DatadogConfig{
				ClientApiKeyEnvVar: "CLIENT_API_KEY",
				ClientAppKeyEnvVar: "CLIENT_APP_KEY",
			},
		}

		_, err := newMetricsExporter(cfg, "")
		if err != nil {
			t.Fatalf("expected no error creating Datadog exporter, but got: %v", err)
		}
	})

	t.Run("OpenTelemetry exporter", func(t *testing.T) {
		cfg := config.LspwatchConfig{
			Exporter: "opentelemetry",
			OpenTelemetry: &config.OpenTelemetryConfig{
				TLS: config.TLSConfig{
					Insecure: true,
				},
			},
		}

		_, err := newMetricsExporter(cfg, "")
		if err != nil {
			t.Fatalf("expected no error creating OpenTelemetry exporter, but got: %v", err)
		}
	})

	t.Run("invalid exporter", func(t *testing.T) {
		cfg := config.LspwatchConfig{
			Exporter: "invalid",
		}

		_, err := newMetricsExporter(cfg, "")
		if err == nil {
			t.Fatalf("expected error creating invalid exporter, but got nil")
		}
	})
}

func TestGetTagValues(t *testing.T) {
	t.Run("invalid tag", func(t *testing.T) {
		t.Parallel()
		cfg := config.LspwatchConfig{
			Tags: []string{"invalid"},
		}

		tagGetters := map[telemetry.AvailableTag]func() telemetry.TagValue{
			telemetry.LanguageServer: func() telemetry.TagValue {
				return telemetry.TagValue("server1")
			},
		}

		_, err := getTagValues(&cfg, tagGetters)
		if err == nil {
			t.Errorf("expected error getting tag values, but got nil")
		}
	})
}
