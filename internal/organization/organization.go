// Copyright (c) 2024 Shawn LoPresto
// This source code is licensed under the MIT license found in the
// LICENSE file in the root directory of this source tree.

// Package organization provides functionality for managing AWS Organizations.
// Version: 1.0.0
package organization

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/PimpMyNines/AWS-Pullomi-Organization-Configuration/internal/config"
	"github.com/PimpMyNines/AWS-Pullomi-Organization-Configuration/internal/metrics"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/organizations"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ssm"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

// RetryConfig defines the configuration for retry operations
type RetryConfig struct {
	MaxAttempts int
	Delay       time.Duration
}

// OrganizationService defines the interface for organization operations
type OrganizationService interface {
	CreateOU(ctx *pulumi.Context, name string, parentId pulumi.StringInput, tags pulumi.StringMap) (*organizations.OrganizationalUnit, error)
	CreateOUHierarchy(ctx *pulumi.Context, parent pulumi.StringInput, ouConfig *config.OUConfig, tags pulumi.StringMap) (*organizations.OrganizationalUnit, map[string]*organizations.OrganizationalUnit, error)
	Backup(ctx context.Context) error
	Restore(ctx context.Context, backupId string) error
}

// Organization represents an AWS Organization
type Organization struct {
	logger        *zap.Logger
	metrics       *metrics.Collector
	limiter       *rate.Limiter
	stateMutex    sync.RWMutex
	backupMutex   sync.Mutex
	org           *organizations.Organization
	securityOU    *organizations.OrganizationalUnit
	defaultOU     *organizations.OrganizationalUnit
	additionalOUs map[string]*organizations.OrganizationalUnit
	rootId        pulumi.StringOutput
	cleanup       []func() error
}

const (
	// SSM parameter paths
	ssmOrgInfoPath   = "/organization/info"
	ssmOUInfoPathFmt = "/organization/ou/%s"

	// Feature sets and policy types
	featureSetAll = "ALL"
	policyTypeSCP = "SERVICE_CONTROL_POLICY"
	policyTypeTag = "TAG_POLICY"

	// Retry configurations
	maxRetryAttempts = 3
	baseDelay        = time.Second * 2
	maxDelay         = time.Second * 30

	// Rate limiting
	rateLimit = 10
	rateBurst = 20
)

// NewOrganization creates a new AWS Organization with the specified configuration
func NewOrganization(ctx *pulumi.Context, cfg *config.OrganizationConfig) (*Organization, error) {
	logger, err := zap.NewProduction()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize logger: %w", err)
	}

	metrics, err := metrics.NewCollector("organization")
	if err != nil {
		return nil, fmt.Errorf("failed to initialize metrics: %w", err)
	}

	org := &Organization{
		logger:        logger,
		metrics:       metrics,
		limiter:       rate.NewLimiter(rate.Limit(rateLimit), rateBurst),
		additionalOUs: make(map[string]*organizations.OrganizationalUnit),
	}

	if err := org.initialize(ctx, cfg); err != nil {
		return nil, err
	}

	return org, nil
}

// initialize sets up the organization and its components
func (o *Organization) initialize(ctx *pulumi.Context, cfg *config.OrganizationConfig) error {
	start := time.Now()
	defer func() {
		o.metrics.RecordDuration("organization_initialization", time.Since(start))
	}()

	o.logger.Info("initializing organization")

	if err := o.validateConfig(cfg); err != nil {
		return err
	}

	if err := o.createOrganization(ctx, cfg); err != nil {
		return err
	}

	if err := o.createOUs(ctx, cfg); err != nil {
		return err
	}

	if err := o.storeOrganizationInfo(ctx, cfg); err != nil {
		return err
	}

	return nil
}

// validateConfig validates the organization configuration
func (o *Organization) validateConfig(cfg *config.OrganizationConfig) error {
	if cfg == nil || cfg.LandingZoneConfig == nil {
		return fmt.Errorf("invalid organization configuration: config cannot be nil")
	}

	o.logger.Info("configuration validated successfully")
	return nil
}

// createOrganization creates the AWS Organization
func (o *Organization) createOrganization(ctx *pulumi.Context, cfg *config.OrganizationConfig) error {
	if err := o.limiter.Wait(context.Background()); err != nil {
		return fmt.Errorf("rate limit exceeded: %w", err)
	}

	org, err := organizations.NewOrganization(ctx, "aws-org", &organizations.OrganizationArgs{
		FeatureSet: pulumi.String(featureSetAll),
		EnabledPolicyTypes: pulumi.StringArray{
			pulumi.String(policyTypeSCP),
			pulumi.String(policyTypeTag),
		},
	})
	if err != nil {
		o.logger.Error("failed to create organization", zap.Error(err))
		return fmt.Errorf("failed to create organization: %w", err)
	}

	o.org = org
	o.rootId = org.Roots.Index(pulumi.Int(0)).Id()

	o.logger.Info("organization created successfully")
	o.metrics.IncrementCounter("organization_created")

	return nil
}

// createOUs creates the organizational units
func (o *Organization) createOUs(ctx *pulumi.Context, cfg *config.OrganizationConfig) error {
	var err error

	// Create Security OU
	o.securityOU, err = o.createOU(ctx, "Security", o.rootId, pulumi.ToStringMap(cfg.LandingZoneConfig.Tags))
	if err != nil {
		return err
	}

	// Create Default OU
	o.defaultOU, err = o.createOU(ctx, cfg.LandingZoneConfig.DefaultOUName, o.rootId, pulumi.ToStringMap(cfg.LandingZoneConfig.Tags))
	if err != nil {
		return err
	}

	// Create additional OUs
	return o.createAdditionalOUs(ctx, cfg)
}

// createOU creates an organizational unit with retry logic
func (o *Organization) createOU(ctx *pulumi.Context, name string, parentId pulumi.StringInput, tags pulumi.StringMap) (*organizations.OrganizationalUnit, error) {
	if err := o.limiter.Wait(context.Background()); err != nil {
		return nil, fmt.Errorf("rate limit exceeded: %w", err)
	}

	var ou *organizations.OrganizationalUnit
	operation := func() error {
		var err error
		ou, err = organizations.NewOrganizationalUnit(ctx, name, &organizations.OrganizationalUnitArgs{
			Name:     pulumi.String(name),
			ParentId: parentId,
			Tags:     tags,
		})
		return err
	}

	if err := RetryWithBackoff(operation, RetryConfig{
		MaxAttempts: maxRetryAttempts,
		Delay:       baseDelay,
	}); err != nil {
		o.logger.Error("failed to create OU", zap.String("name", name), zap.Error(err))
		return nil, fmt.Errorf("failed to create OU %s: %w", name, err)
	}

	o.logger.Info("created OU successfully", zap.String("name", name))
	o.metrics.IncrementCounter("ou_created")

	return ou, nil
}

// Backup creates a backup of the organization state
func (o *Organization) Backup(ctx context.Context) error {
	o.backupMutex.Lock()
	defer o.backupMutex.Unlock()

	// Implementation for backup logic
	return nil
}

// Restore restores the organization state from a backup
func (o *Organization) Restore(ctx context.Context, backupId string) error {
	o.backupMutex.Lock()
	defer o.backupMutex.Unlock()

	// Implementation for restore logic
	return nil
}

// RetryWithBackoff implements exponential backoff retry logic
func RetryWithBackoff(operation func() error, config RetryConfig) error {
	var lastErr error
	for attempt := 1; attempt <= config.MaxAttempts; attempt++ {
		if err := operation(); err == nil {
			return nil
		} else {
			lastErr = err
			if attempt < config.MaxAttempts {
				delay := time.Duration(float64(config.Delay) * float64(attempt))
				if delay > maxDelay {
					delay = maxDelay
				}
				time.Sleep(delay)
			}
		}
	}
	return fmt.Errorf("operation failed after %d attempts: %w", config.MaxAttempts, lastErr)
}

// Cleanup performs cleanup operations
func (o *Organization) Cleanup() error {
	o.logger.Info("starting cleanup")
	var lastErr error
	for _, cleanup := range o.cleanup {
		if err := cleanup(); err != nil {
			lastErr = err
			o.logger.Error("cleanup operation failed", zap.Error(err))
		}
	}
	return lastErr
}
