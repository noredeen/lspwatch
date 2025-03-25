package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/noredeen/lspwatch/internal/testutil"
)

// Test flow for http/grpc OTel:
//
//	(go test process) ------launch------
//	    |                               |
//	    |                               v
//	    |                  lspwatch instance [ w/ compiled mock language server ]
//	    |                                   |
//	     ---launch/check---     ---export---
//	                       |   |
//	                       v   v
//	             **OTel collector in Docker

// For grpc/http exporters.
type otelExternalExportedObject struct {
	ResourceMetrics []struct {
		ScopeMetrics []struct {
			Metrics []struct {
				Name      string
				Histogram struct {
					DataPoints []struct {
						Count      string
						Attributes []struct {
							Key   string
							Value struct {
								StringValue string
							}
						}
					}
				}
			}
		}
	}
}

func TestLspwatchWithOtel(t *testing.T) {
	testDataDir := os.Getenv("TEST_DATA_DIR")
	if testDataDir == "" {
		t.Fatalf("TEST_DATA_DIR is not set")
	}

	bytes, err := os.ReadFile(filepath.Join(testDataDir, "client_messages.json"))
	if err != nil {
		t.Fatalf("failed to read client messages: %v", err)
	}

	var messages []string
	err = json.Unmarshal(bytes, &messages)
	if err != nil {
		t.Fatalf("failed to unmarshal client messages: %v", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("error getting working directory: %v", err)
	}

	t.Run("gprc exporter", func(t *testing.T) {
		t.Parallel()
		otelExportsDir := createTempWritableDir(t, "otel-grpc-exports")
		otelConfigFile := filepath.Join(cwd, "config", "otel_config.yaml")
		lspwatchConfigFile := filepath.Join(cwd, "config", "otel_grpc_lspwatch.yaml")
		spinUpOtelCollector(
			t,
			otelExportsDir,
			otelConfigFile,
			nat.PortSet{
				"4317": struct{}{},
			},
			nat.PortMap{
				"4317/tcp": []nat.PortBinding{
					{
						HostIP:   "0.0.0.0",
						HostPort: "4317",
					},
				},
			},
		)
		runTest(t, lspwatchConfigFile, messages)
		// Wait for the language server to send all its messages and for the exporter to flush.
		time.Sleep(13 * time.Second)
		assertExternalExporterOTelMetrics(t, filepath.Join(otelExportsDir, "metrics.json"))
	})

	t.Run("http exporter", func(t *testing.T) {
		t.Parallel()
		otelExportsDir := createTempWritableDir(t, "otel-http-exports")
		otelConfigFile := filepath.Join(cwd, "config", "otel_config.yaml")
		lspwatchConfigFile := filepath.Join(cwd, "config", "otel_http_lspwatch.yaml")
		spinUpOtelCollector(
			t,
			otelExportsDir,
			otelConfigFile,
			nat.PortSet{
				"4318": struct{}{},
			},
			nat.PortMap{
				"4318/tcp": []nat.PortBinding{
					{
						HostIP:   "0.0.0.0",
						HostPort: "4318",
					},
				},
			},
		)
		runTest(t, lspwatchConfigFile, messages)
		// Wait for the language server to send all its messages and for the exporter to flush.
		time.Sleep(13 * time.Second)
		assertExternalExporterOTelMetrics(t, filepath.Join(otelExportsDir, "metrics.json"))
	})

	t.Run("file exporter", func(t *testing.T) {
		t.Parallel()
		lspwatchConfigFile := filepath.Join(cwd, "config", "otel_file_lspwatch.yaml")
		runTest(t, lspwatchConfigFile, messages)
		time.Sleep(13 * time.Second)
		// Wait for the language server to send all its messages and for the exporter to flush.
	})
}

func runTest(t *testing.T, configFile string, messages []string) {
	t.Helper()
	cmd := testutil.PrepareIntegrationTest(
		t,
		"--config",
		configFile,
		"--",
		"./build/mock_language_server",
	)

	serverStdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("failed to create stdin pipe: %v", err)
	}
	serverStdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to create stdout pipe: %v", err)
	}

	err = cmd.Start()
	if err != nil {
		t.Fatalf("failed to start lspwatch: %v", err)
	}

	t.Cleanup(func() {
		// For an unknown reason, this Close() is required to make the
		// process exit gracefully.
		err := serverStdin.Close()
		if err != nil {
			t.Fatalf("failed to close server stdin: %v", err)
		}

		err = cmd.Process.Signal(os.Interrupt)
		if err != nil {
			t.Fatalf("failed to signal lspwatch to shutdown: %v", err)
		}

		// Give it some time to flush coverage data
		done := make(chan error)
		go func() {
			done <- cmd.Wait()
		}()

		// Wait up to 5 seconds for graceful shutdown
		select {
		case <-time.After(10 * time.Second):
			t.Log("Process didn't exit gracefully, forcing kill")
			cmd.Process.Kill()
		case err := <-done:
			t.Logf("Process exited with: %v", err)
		}
	})

	// TODO: Need this?
	go func() {
		for {
			buf := make([]byte, 100)
			serverStdout.Read(buf)
		}
	}()

	// Send client messages to lspwatch.
	for _, message := range messages {
		_, err = serverStdin.Write([]byte(message))
		if err != nil {
			t.Fatalf("failed to write message to lspwatch stdin: %v", err)
		}

		time.Sleep(100 * time.Millisecond)
	}

	t.Logf("Sent %d messages", len(messages))
}

// For grpc/http exporters.
func assertExternalExporterOTelMetrics(t *testing.T, otelFilePath string) {
	otelFile, err := os.Open(otelFilePath)
	if err != nil {
		t.Fatalf("failed to open otel metrics file: %v", err)
	}

	decoder := json.NewDecoder(otelFile)
	foundRequestMetric := false
	foundServerMetric := false
	for {
		var exportedObject otelExternalExportedObject
		err = decoder.Decode(&exportedObject)
		if err != nil {
			t.Logf("failed to decode otel metrics JSON export: %v", err)
			break
		}

		for _, resourceMetrics := range exportedObject.ResourceMetrics {
			for _, scopeMetrics := range resourceMetrics.ScopeMetrics {
				for _, metric := range scopeMetrics.Metrics {
					if metric.Name == "lspwatch.request.duration" {
						foundRequestMetric = true
					}
					if metric.Name == "lspwatch.server.rss" {
						foundServerMetric = true
					}

					firstDataPointAttrs := metric.Histogram.DataPoints[0].Attributes
					assertOTelAttributes(
						t,
						firstDataPointAttrs,
						[]string{"user", "os", "ram", "language_server", "project"},
					)
				}
			}
		}
	}

	if !foundRequestMetric {
		t.Errorf("expected to find request metrics")
	}
	if !foundServerMetric {
		t.Errorf("expected to find server metrics")
	}
}

func assertOTelAttributes(
	t *testing.T,
	attrs []struct {
		Key   string
		Value struct {
			StringValue string
		}
	},
	targets []string,
) {
	found := 0
	for _, target := range targets {
		for _, attr := range attrs {
			if attr.Key == target {
				found++
				break
			}
		}
	}

	if found != len(targets) {
		t.Errorf("expected to find attributes %v", targets)
	}
}

func spinUpOtelCollector(
	t *testing.T,
	otelExportsDir string,
	otelConfigFile string,
	exposedPorts nat.PortSet,
	portMap nat.PortMap,
) string {
	ctx := context.Background()

	if err := os.MkdirAll(otelExportsDir, 0777); err != nil {
		t.Fatalf("error creating OTel export directory: %v", err)
	}

	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatalf("error creating Docker client: %v", err)
	}

	out, err := dockerClient.ImagePull(ctx, "otel/opentelemetry-collector-contrib:latest", image.PullOptions{})
	if err != nil {
		t.Fatalf("error pulling OTel collector image: %v", err)
	}
	defer out.Close()

	// Consume all output to ensure image pull completes.
	if _, err := io.Copy(io.Discard, out); err != nil {
		t.Fatalf("error reading image pull output: %v", err)
	}

	t.Logf("Spinning up OTel collector with exports directory: '%s'...", otelExportsDir)

	config := &container.Config{
		Image:        "otel/opentelemetry-collector-contrib:latest",
		ExposedPorts: exposedPorts,
	}

	hostConfig := &container.HostConfig{
		PortBindings: portMap,
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeBind,
				Source: otelExportsDir,
				Target: "/file-exporter",
				BindOptions: &mount.BindOptions{
					CreateMountpoint: true,
				},
				ReadOnly: false,
			},
			{
				Type:   mount.TypeBind,
				Source: otelConfigFile,
				Target: "/etc/otelcol-contrib/config.yaml",
			},
		},
	}

	resp, err := dockerClient.ContainerCreate(ctx, config, hostConfig, nil, nil, "")
	if err != nil {
		t.Fatalf("error creating otel collector container: %v", err)
	}

	err = dockerClient.ContainerStart(ctx, resp.ID, container.StartOptions{})
	if err != nil {
		t.Fatalf("error starting otel collector container: %v", err)
	}

	t.Logf("OTel collector container started with ID: %s", resp.ID)

	// Check permissions
	info, err := os.Stat(otelExportsDir)
	if err != nil {
		t.Logf("Warning: failed to stat export directory: %v", err)
	} else {
		t.Logf("Export directory permissions: %v", info.Mode())
	}

	t.Cleanup(func() {
		timeoutSeconds := 10
		err := dockerClient.ContainerStop(ctx, resp.ID, container.StopOptions{Timeout: &timeoutSeconds})
		if err != nil {
			t.Logf("error stopping otel collector container: %v", err)
		}
		if err = dockerClient.ContainerRemove(ctx, resp.ID, container.RemoveOptions{}); err != nil {
			t.Logf("error removing otel collector container: %v", err)
		}

		if err := os.RemoveAll(otelExportsDir); err != nil {
			t.Logf("error removing OTel export directory: %v", err)
		}
	})

	return resp.ID
}

func createTempWritableDir(t *testing.T, dirName string) string {
	dir, err := os.MkdirTemp("", fmt.Sprintf("%s-*", dirName))
	if err != nil {
		t.Fatalf("error creating temp directory: %v", err)
	}
	if err := os.Chmod(dir, 0777); err != nil {
		t.Fatalf("error setting directory permissions: %v", err)
	}

	return dir
}
