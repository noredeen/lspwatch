package config

import (
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TODO: Test env var expansion
func TestReadLspwatchConfig_Validations(t *testing.T) {
	type testCase struct {
		name        string
		rawYaml     string
		expectedCfg LspwatchConfig
		err         bool
	}

	testCases := []testCase{
		{
			name: "invalid YAML",
			rawYaml: `
exporter opentelemetry -
`,
			expectedCfg: LspwatchConfig{},
			err:         true,
		},

		{
			name: "opentelemetry with file protocol",
			rawYaml: `
project: lspwatch
exporter: opentelemetry
opentelemetry:
  protocol: file
  directory: ./`,
			expectedCfg: LspwatchConfig{
				Exporter: "opentelemetry",
				Project:  "lspwatch",
				OpenTelemetry: &OpenTelemetryConfig{
					Protocol:  "file",
					Directory: "./",
				},
			},
			err: false,
		},

		{
			name: "missing project",
			rawYaml: `
exporter: opentelemetry
opentelemetry:
  protocol: file
  directory: ./`,
			expectedCfg: LspwatchConfig{},
			err:         true,
		},

		{
			name: "opentelemetry with grpc protocol and default tls",
			rawYaml: `
project: lspwatch
exporter: opentelemetry
opentelemetry:
  protocol: grpc
  endpoint: localhost:4317
  compression: gzip`,
			expectedCfg: LspwatchConfig{
				Exporter: "opentelemetry",
				Project:  "lspwatch",
				OpenTelemetry: &OpenTelemetryConfig{
					Protocol:    "grpc",
					Endpoint:    "localhost:4317",
					Compression: "gzip",
				},
			},
			err: false,
		},

		{
			name: "opentelemetry with grpc protocol all the bells and whistles",
			rawYaml: `
project: lspwatch
exporter: opentelemetry
opentelemetry:
  protocol: grpc
  endpoint: localhost:4317
  timeout: 10
  compression: gzip
  headers:
    foo: bar
  tls:
    insecure: true
    ca_file: ./ca.pem
    cert_file: ./cert.pem
    key_file: ./key.pem`,
			expectedCfg: LspwatchConfig{
				Project:  "lspwatch",
				Exporter: "opentelemetry",
				OpenTelemetry: &OpenTelemetryConfig{
					Protocol:    "grpc",
					Endpoint:    "localhost:4317",
					Compression: "gzip",
					Headers:     map[string]string{"foo": "bar"},
					Timeout:     &[]int{10}[0],
					TLS: TLSConfig{
						Insecure: true,
						CAFile:   "./ca.pem",
						CertFile: "./cert.pem",
						KeyFile:  "./key.pem",
					},
				},
			},
			err: false,
		},

		{
			name: "opentelemetry with http protocol",
			rawYaml: `
project: lspwatch
exporter: opentelemetry
opentelemetry:
  protocol: http
  endpoint: localhost:4318`,
			expectedCfg: LspwatchConfig{
				Project:  "lspwatch",
				Exporter: "opentelemetry",
				OpenTelemetry: &OpenTelemetryConfig{
					Protocol: "http",
					Endpoint: "localhost:4318",
				},
			},
			err: false,
		},

		{
			name: "missing opentelemetry config",
			rawYaml: `
project: lspwatch
exporter: opentelemetry
`,
			expectedCfg: LspwatchConfig{},
			err:         true,
		},

		{
			name: "missing opentelemetry protocol",
			rawYaml: `
project: lspwatch
exporter: opentelemetry
opentelemetry:
  directory: ./`,
			expectedCfg: LspwatchConfig{},
			err:         true,
		},

		{
			name: "datadog with all required fields",
			rawYaml: `
project: lspwatch
exporter: datadog
datadog:
  client_api_key: api-key
  client_app_key: app-key`,
			expectedCfg: LspwatchConfig{
				Project:  "lspwatch",
				Exporter: "datadog",
				Datadog: &DatadogConfig{
					ClientApiKey: "api-key",
					ClientAppKey: "app-key",
				},
			},
			err: false,
		},

		{
			name: "missing datadog client api key",
			rawYaml: `
project: lspwatch
exporter: datadog
env_file: .env
datadog:
  client_app_key: app-key`,
			expectedCfg: LspwatchConfig{},
			err:         true,
		},

		{
			name: "invalid datadog batch size",
			rawYaml: `
project: lspwatch
exporter: datadog
env_file: .env
datadog:
  client_api_key: api-key
  client_app_key: app-key
  exporter_batch_size: 800`,
			expectedCfg: LspwatchConfig{},
			err:         true,
		},

		{
			name: "invalid datadog batch timeout",
			rawYaml: `
project: lspwatch
exporter: datadog
env_file: .env
datadog:
  client_api_key: api-key
  client_app_key: app-key
  exporter_batch_timeout: 0`,
			expectedCfg: LspwatchConfig{},
			err:         true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			cfg, err := ReadLspwatchConfig([]byte(testCase.rawYaml))
			if testCase.err {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Fatalf("expected no error, got: %v", err)
				}
			}

			if !cmp.Equal(cfg, testCase.expectedCfg) {
				t.Errorf("expected %+v, got %+v", testCase.expectedCfg, cfg)
			}
		})
	}
}

func TestReadLspwatchConfig_EnvVarExpansion(t *testing.T) {
	rawYaml := `project: lspwatch
exporter: opentelemetry
datadog:
  client_api_key: ${DATADOG_API_KEY}
  client_app_key: ${DATADOG_APP_KEY}
opentelemetry:
  endpoint: localhost:4317
  protocol: grpc
  headers:
    x-auth-token: ${AUTH_TOKEN}`

	os.Setenv("DATADOG_API_KEY", "api-key")
	os.Setenv("DATADOG_APP_KEY", "app-key")
	os.Setenv("AUTH_TOKEN", "auth-token")

	cfg, err := ReadLspwatchConfig([]byte(rawYaml))
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if val := cfg.OpenTelemetry.Headers["x-auth-token"]; val != "auth-token" {
		t.Errorf("expected x-auth-token to be 'auth-token', got %s", val)
	}

	if val := cfg.Datadog.ClientApiKey; val != "api-key" {
		t.Errorf("expected client_api_key to be 'api-key', got %s", val)
	}

	if val := cfg.Datadog.ClientAppKey; val != "app-key" {
		t.Errorf("expected client_app_key to be 'app-key', got %s", val)
	}
}

func TestGetDefaultConfig(t *testing.T) {
	expectedCfg := LspwatchConfig{
		Exporter: "opentelemetry",
		OpenTelemetry: &OpenTelemetryConfig{
			Protocol:  "file",
			Directory: "./",
		},
	}

	cfg := GetDefaultConfig()
	if !cmp.Equal(cfg, expectedCfg) {
		t.Errorf("expected %+v, got %+v", expectedCfg, cfg)
	}
}
