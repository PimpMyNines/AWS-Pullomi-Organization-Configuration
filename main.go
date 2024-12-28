// Copyright (c) 2024 Shawn LoPresto
// This source code is licensed under the MIT license found in the
// LICENSE file in the root directory of this source tree.

// Package main provides the entry point for the AWS Organization and Control Tower configuration tool.
// Version: 1.0.0
package main

import (
	"context"
	"os"
	"time"

	"github.com/PimpMyNines/AWS-Pullomi-Organization-Configuration/internal/config"
	"github.com/PimpMyNines/AWS-Pullomi-Organization-Configuration/internal/controltower"
	"github.com/PimpMyNines/AWS-Pullomi-Organization-Configuration/internal/logging"
	"github.com/PimpMyNines/AWS-Pullomi-Organization-Configuration/internal/metrics"
	"github.com/PimpMyNines/AWS-Pullomi-Organization-Configuration/internal/organization"
	"github.com/PimpMyNines/AWS-Pullomi-Organization-Configuration/internal/state"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

const (
	// ApplicationVersion represents the current version of the application
	ApplicationVersion = "1.0.0"
	// DefaultTimeout represents the default timeout for operations
	DefaultTimeout = 30 * time.Minute
	// MaxConcurrentOperations represents the maximum number of concurrent AWS operations
	MaxConcurrentOperations = 10
	// RateLimitRPS represents the maximum rate of AWS API calls per second
	RateLimitRPS = 10
)

// main is the entry point of the application
func main() {
	// Initialize logger
	logger, err := logging.NewLogger("main")
	if err != nil {
		panic("failed to initialize logger: " + err.Error())
	}
	defer logger.Sync()

	// Initialize metrics collector
	metrics, err := metrics.NewCollector("aws-organization-config")
	if err != nil {
		logger.Fatal("failed to initialize metrics collector", zap.Error(err))
	}
	defer metrics.Close()

	// Initialize rate limiter for AWS API calls
	limiter := rate.NewLimiter(rate.Limit(RateLimitRPS), MaxConcurrentOperations)

	// Initialize state manager
	stateManager, err := state.NewManager("aws-organization-state")
	if err != nil {
		logger.Fatal("failed to initialize state manager", zap.Error(err))
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()

	// Run Pulumi program
	err = pulumi.Run(func(ctx *pulumi.Context) error {
		// Start timing the execution
		start := time.Now()
		defer func() {
			metrics.RecordDuration("total_execution_time", time.Since(start))
		}()

		// Load and validate configuration
		cfg, err := loadAndValidateConfig(ctx, logger)
		if err != nil {
			return pulumi.Error(err)
		}

		// Create organization with retry logic
		org, err := createOrganizationWithRetry(ctx, cfg, logger, limiter)
		if err != nil {
			return pulumi.Error(err)
		}

		// Ensure cleanup on error
		defer func() {
			if err != nil {
				if cleanupErr := org.Cleanup(); cleanupErr != nil {
					logger.Error("cleanup failed",
						zap.Error(cleanupErr),
						zap.String("operation", "cleanup"))
				}
			}
		}()

		// Setup landing zone with retry logic
		if err := setupLandingZoneWithRetry(ctx, org, cfg, logger, limiter); err != nil {
			return pulumi.Error(err)
		}

		// Save state
		if err := stateManager.Save(ctx, org); err != nil {
			logger.Error("failed to save state", zap.Error(err))
			return pulumi.Error(err)
		}

		return nil
	})

	if err != nil {
		logger.Fatal("deployment failed", zap.Error(err))
		os.Exit(1)
	}
}

// loadAndValidateConfig loads and validates the configuration
func loadAndValidateConfig(ctx *pulumi.Context, logger *zap.Logger) (*config.OrganizationConfig, error) {
	logger.Info("loading configuration")

	cfg := config.DefaultConfig
	if err := cfg.Validate(); err != nil {
		logger.Error("invalid configuration", zap.Error(err))
		return nil, err
	}

	logger.Info("configuration validated successfully")
	return &cfg, nil
}

// createOrganizationWithRetry creates an AWS organization with retry logic
func createOrganizationWithRetry(ctx *pulumi.Context, cfg *config.OrganizationConfig,
	logger *zap.Logger, limiter *rate.Limiter) (*organization.Organization, error) {

	var org *organization.Organization
	var err error

	operation := func() error {
		if err := limiter.Wait(ctx); err != nil {
			return err
		}

		org, err = organization.NewOrganization(ctx, cfg)
		return err
	}

	retryConfig := organization.RetryConfig{
		MaxAttempts: 3,
		Delay:       time.Second * 5,
	}

	if err := organization.RetryWithBackoff(operation, retryConfig); err != nil {
		logger.Error("failed to create organization after retries",
			zap.Error(err),
			zap.Int("maxAttempts", retryConfig.MaxAttempts))
		return nil, err
	}

	logger.Info("organization created successfully")
	return org, nil
}

// setupLandingZoneWithRetry sets up the AWS Control Tower landing zone with retry logic
func setupLandingZoneWithRetry(ctx *pulumi.Context, org *organization.Organization,
	cfg *config.OrganizationConfig, logger *zap.Logger, limiter *rate.Limiter) error {

	operation := func() error {
		if err := limiter.Wait(ctx); err != nil {
			return err
		}

		return controltower.SetupLandingZone(ctx, org, cfg.LandingZoneConfig)
	}

	retryConfig := organization.RetryConfig{
		MaxAttempts: 3,
		Delay:       time.Second * 5,
	}

	if err := organization.RetryWithBackoff(operation, retryConfig); err != nil {
		logger.Error("failed to setup landing zone after retries",
			zap.Error(err),
			zap.Int("maxAttempts", retryConfig.MaxAttempts))
		return err
	}

	logger.Info("landing zone setup completed successfully")
	return nil
}
