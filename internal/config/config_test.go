package config

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestReadLspwatchConfig(t *testing.T) {
	type testCase struct {
		name        string
		rawYaml     string
		expectedCfg LspwatchConfig
		err         bool
	}

	testCases := []testCase{
		{
			name: "opentelemetry with file protocol",
			rawYaml: `
exporter: opentelemetry
opentelemetry:
  protocol: file
  directory: ./`,
			expectedCfg: LspwatchConfig{
				Exporter: "opentelemetry",
				OpenTelemetry: &OpenTelemetryConfig{
					Protocol:  "file",
					Directory: "./",
				},
			},
			err: false,
		},

		{
			name: "opentelemetry with grpc protocol and default tls",
			rawYaml: `
exporter: opentelemetry
opentelemetry:
  protocol: grpc
  metrics_endpoint_url: http://localhost:4317/v1/metrics
  compression: gzip`,
			expectedCfg: LspwatchConfig{
				Exporter: "opentelemetry",
				OpenTelemetry: &OpenTelemetryConfig{
					Protocol:           "grpc",
					MetricsEndpointURL: "http://localhost:4317/v1/metrics",
					Compression:        "gzip",
				},
			},
			err: false,
		},

		{
			name: "opentelemetry with grpc protocol all the bells and whistles",
			rawYaml: `
exporter: opentelemetry
opentelemetry:
  protocol: grpc
  metrics_endpoint_url: http://localhost:4317/v1/metrics
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
				Exporter: "opentelemetry",
				OpenTelemetry: &OpenTelemetryConfig{
					Protocol:           "grpc",
					MetricsEndpointURL: "http://localhost:4317/v1/metrics",
					Compression:        "gzip",
					Headers:            map[string]string{"foo": "bar"},
					Timeout:            &[]int{10}[0],
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
exporter: opentelemetry
opentelemetry:
  protocol: http
  metrics_endpoint_url: http://localhost:4318/v1/metrics`,
			expectedCfg: LspwatchConfig{
				Exporter: "opentelemetry",
				OpenTelemetry: &OpenTelemetryConfig{
					Protocol:           "http",
					MetricsEndpointURL: "http://localhost:4318/v1/metrics",
				},
			},
			err: false,
		},

		{
			name: "missing opentelemetry config",
			rawYaml: `
exporter: opentelemetry
`,
			expectedCfg: LspwatchConfig{},
			err:         true,
		},

		{
			name: "missing opentelemetry protocol",
			rawYaml: `
exporter: opentelemetry
opentelemetry:
  directory: ./`,
			expectedCfg: LspwatchConfig{},
			err:         true,
		},

		{
			name: "datadog with all required fields",
			rawYaml: `
exporter: datadog
env_file: .env
datadog:
  client_api_key_env_var: CLIENT_API_KEY
  client_app_key_env_var: CLIENT_APP_KEY`,
			expectedCfg: LspwatchConfig{
				Exporter:    "datadog",
				EnvFilePath: ".env",
				Datadog: &DatadogConfig{
					ClientApiKeyEnvVar: "CLIENT_API_KEY",
					ClientAppKeyEnvVar: "CLIENT_APP_KEY",
				},
			},
			err: false,
		},

		{
			name: "missing datadog env file",
			rawYaml: `
exporter: datadog
datadog:
  client_api_key_env_var: CLIENT_API_KEY
  client_app_key_env_var: CLIENT_APP_KEY`,
			expectedCfg: LspwatchConfig{},
			err:         true,
		},

		{
			name: "missing datadog client api key env var",
			rawYaml: `
exporter: datadog
env_file: .env
datadog:
  client_app_key_env_var: CLIENT_APP_KEY`,
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
