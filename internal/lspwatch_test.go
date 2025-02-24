package internal

import (
	"testing"

	"github.com/noredeen/lspwatch/internal/config"
)

func TestNewMetricsOTelExporter(t *testing.T) {
	t.Run("Datadog exporter", func(t *testing.T) {
		cfg := config.LspwatchConfig{
			Exporter: "datadog",
		}

		_, err := newMetricsExporter(cfg, false)
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

		_, err := newMetricsExporter(cfg, false)
		if err != nil {
			t.Fatalf("expected no error creating OpenTelemetry exporter, but got: %v", err)
		}
	})

	t.Run("invalid exporter", func(t *testing.T) {
		cfg := config.LspwatchConfig{
			Exporter: "invalid",
		}

		_, err := newMetricsExporter(cfg, false)
		if err == nil {
			t.Fatalf("expected error creating invalid exporter, but got nil")
		}
	})
}
