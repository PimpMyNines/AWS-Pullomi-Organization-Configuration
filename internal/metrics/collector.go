// Copyright (c) 2024 Shawn LoPresto
// This source code is licensed under the MIT license found in the
// LICENSE file in the root directory of this source tree.

// Package metrics provides metrics collection and monitoring functionality.
// Version: 1.0.0
package metrics

import (
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/zap"
)

// MetricType represents the type of metric
type MetricType int

const (
	// Metric types
	Counter MetricType = iota
	Gauge
	Histogram
	Summary

	// Default metric configurations
	defaultNamespace = "aws_organization"
	defaultSubsystem = "operations"
)

// Collector handles metrics collection and reporting
type Collector struct {
	logger     *zap.Logger
	namespace  string
	subsystem  string
	registry   *prometheus.Registry
	counters   map[string]prometheus.Counter
	gauges     map[string]prometheus.Gauge
	histograms map[string]prometheus.Histogram
	summaries  map[string]prometheus.Summary
	mutex      sync.RWMutex
}

// NewCollector creates a new metrics collector
func NewCollector(component string) (*Collector, error) {
	logger, err := zap.NewProduction()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize logger: %w", err)
	}

	registry := prometheus.NewRegistry()

	return &Collector{
		logger:     logger,
		namespace:  defaultNamespace,
		subsystem:  component,
		registry:   registry,
		counters:   make(map[string]prometheus.Counter),
		gauges:     make(map[string]prometheus.Gauge),
		histograms: make(map[string]prometheus.Histogram),
		summaries:  make(map[string]prometheus.Summary),
	}, nil
}

// IncrementCounter increments a counter metric
func (c *Collector) IncrementCounter(name string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	counter, exists := c.counters[name]
	if !exists {
		counter = promauto.NewCounter(prometheus.CounterOpts{
			Namespace: c.namespace,
			Subsystem: c.subsystem,
			Name:      name,
		})
		c.counters[name] = counter
	}

	counter.Inc()
	c.logger.Debug("counter incremented", zap.String("metric", name))
}

// SetGauge sets a gauge metric
func (c *Collector) SetGauge(name string, value float64) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	gauge, exists := c.gauges[name]
	if !exists {
		gauge = promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: c.namespace,
			Subsystem: c.subsystem,
			Name:      name,
		})
		c.gauges[name] = gauge
	}

	gauge.Set(value)
	c.logger.Debug("gauge set",
		zap.String("metric", name),
		zap.Float64("value", value))
}

// RecordDuration records a duration metric
func (c *Collector) RecordDuration(name string, duration time.Duration) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	histogram, exists := c.histograms[name]
	if !exists {
		histogram = promauto.NewHistogram(prometheus.HistogramOpts{
			Namespace: c.namespace,
			Subsystem: c.subsystem,
			Name:      name,
			Buckets:   prometheus.ExponentialBuckets(0.001, 2, 15), // From 1ms to ~16s
		})
		c.histograms[name] = histogram
	}

	histogram.Observe(duration.Seconds())
	c.logger.Debug("duration recorded",
		zap.String("metric", name),
		zap.Duration("duration", duration))
}

// RecordValue records a value metric
func (c *Collector) RecordValue(name string, value float64) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	summary, exists := c.summaries[name]
	if !exists {
		summary = promauto.NewSummary(prometheus.SummaryOpts{
			Namespace:  c.namespace,
			Subsystem:  c.subsystem,
			Name:       name,
			Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
		})
		c.summaries[name] = summary
	}

	summary.Observe(value)
	c.logger.Debug("value recorded",
		zap.String("metric", name),
		zap.Float64("value", value))
}

// Close performs cleanup of the metrics collector
func (c *Collector) Close() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// Unregister all metrics
	for _, counter := range c.counters {
		c.registry.Unregister(counter)
	}
	for _, gauge := range c.gauges {
		c.registry.Unregister(gauge)
	}
	for _, histogram := range c.histograms {
		c.registry.Unregister(histogram)
	}
	for _, summary := range c.summaries {
		c.registry.Unregister(summary)
	}

	c.logger.Info("metrics collector closed")
	return nil
}

// GetRegistry returns the Prometheus registry
func (c *Collector) GetRegistry() *prometheus.Registry {
	return c.registry
}

// WithLabels returns a new collector with additional labels
func (c *Collector) WithLabels(labels prometheus.Labels) *Collector {
	return &Collector{
		logger:     c.logger,
		namespace:  c.namespace,
		subsystem:  c.subsystem,
		registry:   c.registry,
		counters:   make(map[string]prometheus.Counter),
		gauges:     make(map[string]prometheus.Gauge),
		histograms: make(map[string]prometheus.Histogram),
		summaries:  make(map[string]prometheus.Summary),
	}
}
