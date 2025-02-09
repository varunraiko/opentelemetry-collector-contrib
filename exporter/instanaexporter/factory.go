// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package instanaexporter // import "github.com/open-telemetry/opentelemetry-collector-contrib/exporter/instanaexporter"

import (
	"context"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/confighttp"
	"go.opentelemetry.io/collector/config/configopaque"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
)

const (
	// The value of "type" key in configuration.
	typeStr = "instana"
	// The stability level of the exporter.
	stability = component.StabilityLevelAlpha
)

// NewFactory creates an Instana exporter factory
func NewFactory() exporter.Factory {
	return exporter.NewFactory(
		typeStr,
		createDefaultConfig,
		exporter.WithTraces(createTracesExporter, stability),
	)
}

// createDefaultConfig creates the default exporter configuration
func createDefaultConfig() component.Config {
	return &Config{
		HTTPClientSettings: confighttp.HTTPClientSettings{
			Endpoint:        "",
			Timeout:         30 * time.Second,
			Headers:         map[string]configopaque.String{},
			WriteBufferSize: 512 * 1024,
		},
	}
}

// createTracesExporter creates a trace exporter based on this configuration
func createTracesExporter(ctx context.Context, set exporter.CreateSettings, config component.Config) (exporter.Traces, error) {
	cfg := config.(*Config)

	ctx, cancel := context.WithCancel(ctx)

	instanaExporter := newInstanaExporter(cfg, set)

	return exporterhelper.NewTracesExporter(
		ctx,
		set,
		config,
		instanaExporter.pushConvertedTraces,
		exporterhelper.WithCapabilities(consumer.Capabilities{MutatesData: false}),
		exporterhelper.WithStart(instanaExporter.start),
		// Disable Timeout/RetryOnFailure and SendingQueue
		exporterhelper.WithTimeout(exporterhelper.TimeoutSettings{Timeout: 0}),
		exporterhelper.WithRetry(exporterhelper.RetrySettings{Enabled: false}),
		exporterhelper.WithQueue(exporterhelper.QueueSettings{Enabled: false}),
		exporterhelper.WithShutdown(func(context.Context) error {
			cancel()
			return nil
		}),
	)
}
