package internal

import (
	"errors"
	"fmt"
	"gopkg.in/yaml.v3"
	"os"
)

type openTelemetryConfig struct {
	EndpointURL *string `yaml:"endpoint_url"`
	Timeout     *int    `yaml:"timeout"`
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
			return fmt.Errorf("Exporter '%v' is not supported", config.Exporter)
		}
	} else {
		return errors.New("Missing 'exporter' field")
	}

	if config.OpenTelemetry != nil {
		if config.OpenTelemetry.EndpointURL == nil {
			return errors.New("Missing 'endpoint_url' field under 'opentelemetry'")
		}
	}

	return nil
}

func ReadLspwatchConfig(path string) (*LspwatchConfig, error) {
	fileBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("Failed to read config file: %v", err)
	}

	config := &LspwatchConfig{}
	err = yaml.Unmarshal(fileBytes, config)
	if err != nil {
		return nil, fmt.Errorf("Error decoding config YAML: %v", err)
	}

	err = validateLspwatchConfig(config)
	if err != nil {
		return nil, fmt.Errorf("Invalid lspwatch configuration: %v", err)
	}

	return config, nil
}
