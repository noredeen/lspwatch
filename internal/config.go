package internal

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type openTelemetryConfig struct {
	Endpoint *string             `yaml:"metrics_endpoint"`
	Headers  *map[string]*string `yaml:"headers"`
	Timeout  *int                `yaml:"timeout"`
	// TODO: protocol, TLS, retry, proxy, ...
}

type LspwatchConfig struct {
	Exporter      *string              `yaml:"exporter"`
	OpenTelemetry *openTelemetryConfig `yaml:"opentelemetry"`
}

var supportedExporters = [...]string{"opentelemetry", "datadog", "file"}

func validateLspwatchConfig(config *LspwatchConfig) error {
	if config.Exporter != nil {
		good := false

		for _, exporter := range supportedExporters {
			if *config.Exporter == exporter {
				good = true
			}
		}

		if !good {
			return fmt.Errorf("exporter '%v' is not supported", config.Exporter)
		}
	} else {
		return errors.New("missing 'exporter' field")
	}

	if config.OpenTelemetry != nil {
		if config.OpenTelemetry.Endpoint == nil {
			return errors.New("missing 'metrics_endpoint' field under 'opentelemetry'")
		}
	}

	return nil
}

func ReadLspwatchConfig(path string) (*LspwatchConfig, error) {
	fileBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %v", err)
	}

	config := &LspwatchConfig{}
	err = yaml.Unmarshal(fileBytes, config)
	if err != nil {
		return nil, fmt.Errorf("error decoding config YAML: %v", err)
	}

	err = validateLspwatchConfig(config)
	if err != nil {
		return nil, fmt.Errorf("invalid lspwatch configuration: %v", err)
	}

	return config, nil
}
