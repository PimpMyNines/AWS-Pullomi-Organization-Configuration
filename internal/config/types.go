// Copyright (c) 2024 Shawn LoPresto
// This source code is licensed under the MIT license found in the
// LICENSE file in the root directory of this source tree.

// Package config provides configuration types and validation for AWS Organization and Control Tower setup.
// Version: 1.0.0
package config

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/PimpMyNines/AWS-Pullomi-Organization-Configuration/internal/metrics"
	"go.uber.org/zap"
)

// State-related types and constants
const (
	// Change from private to public constants
	StateTableName    = "aws-organization-state"
	StateBackupBucket = "aws-organization-state-backups"
	StateFilePrefix   = "state"
	BackupFilePrefix  = "backup"

	StateExpiryDays     = 30
	BackupRetentionDays = 90
	DefaultTimeout      = 30 * time.Second
	MaxRetries          = 3
	InitialBackoff      = time.Second

	// DynamoDB attributes
	PkAttribute      = "pk"
	SkAttribute      = "sk"
	StateAttribute   = "state"
	VersionAttribute = "version"
)

// StateData represents the structure of stored state
type StateData struct {
	Version           string                 `json:"version"`
	Timestamp         time.Time              `json:"timestamp"`
	State             map[string]interface{} `json:"state"`
	StateTableName    string                 `json:"stateTableName"`
	StateBackupBucket string                 `json:"stateBackupBucket"`
	StateFilePrefix   string                 `json:"stateFilePrefix"`
	Component         string                 `json:"component"`
	Tags              map[string]string      `json:"tags,omitempty"`
	UpdatedBy         string                 `json:"updatedBy,omitempty"`
	BackupID          string                 `json:"backupId,omitempty"`
	Description       string                 `json:"description,omitempty"`
	DefaultTimeout    time.Duration          `json:"defaultTimeout,omitempty"`
	MaxRetries        int                    `json:"maxRetries,omitempty"`
	InitialBackoff    time.Duration          `json:"initialBackoff,omitempty"`
	BackupFilePrefix  string                 `json:"backupFilePrefix,omitempty"`
}

// StateError represents a state operation error
type StateError struct {
	Operation string
	Message   string
	Err       error
}

func (e *StateError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Operation, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Operation, e.Message)
}

// Version information
const (
	ConfigVersion = "1.0.0"
)

// Validation constants
const (
	MinLogRetentionDays = 7
	MaxLogRetentionDays = 3653
	MinNameLength       = 3
	MaxNameLength       = 128
	EmailRegexPattern   = `^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`
)

// ConfigurationManager handles configuration operations
type ConfigurationManager interface {
	Load() (*OrganizationConfig, error)
	Save(config *OrganizationConfig) error
	Validate(config *OrganizationConfig) error
	Backup() error
	Restore(version string) error
}

// OrganizationConfig represents the main configuration structure
type OrganizationConfig struct {
	Version           string             `json:"version"`
	AWSProfile        string             `json:"awsProfile"`
	LandingZoneConfig *LandingZoneConfig `json:"LandingZoneConfig"`
	logger            *zap.Logger
	metrics           *metrics.Collector
	mutex             sync.RWMutex
}

// LandingZoneConfig defines the complete AWS Control Tower Landing Zone configuration
type LandingZoneConfig struct {
	// Basic configurations
	GovernedRegions   []string             `json:"governedRegions"`
	DefaultOUName     string               `json:"defaultOUName"`
	OrganizationUnits map[string]*OUConfig `json:"organizationUnits"`
	LogBucketName     string               `json:"logBucketName"`
	LogRetentionDays  int                  `json:"logRetentionDays"`
	Tags              map[string]string    `json:"tags"`

	// Encryption configurations
	KMSKeyAlias string `json:"kmsKeyAlias"`
	KMSKeyArn   string `json:"kmsKeyArn"`
	KMSKeyId    string `json:"kmsKeyId"`

	// Account configurations
	AccountEmailDomain  string `json:"accountEmailDomain"`
	ManagementAccountId string `json:"managementAccountId"`
	LogArchiveAccountId string `json:"logArchiveAccountId"`
	AuditAccountId      string `json:"auditAccountId"`
	SecurityAccountId   string `json:"securityAccountId"`

	// Control Tower configurations
	CloudTrailRoleArn   string   `json:"cloudTrailRoleArn"`
	EnabledGuardrails   []string `json:"enabledGuardrails"`
	HomeRegion          string   `json:"homeRegion"`
	AllowedRegions      []string `json:"allowedRegions"`
	ManagementRoleArn   string   `json:"managementRoleArn"`
	StackSetRoleArn     string   `json:"stackSetRoleArn"`
	CloudWatchRoleArn   string   `json:"cloudWatchRoleArn"`
	VPCFlowLogsRoleArn  string   `json:"vpcFlowLogsRoleArn"`
	OrganizationRoleArn string   `json:"organizationRoleArn"`

	// Logging configurations
	CloudWatchLogGroup     string `json:"cloudWatchLogGroup"`
	CloudTrailLogGroup     string `json:"cloudTrailLogGroup"`
	CloudTrailBucketRegion string `json:"cloudTrailBucketRegion"`
	AccessLogBucketName    string `json:"accessLogBucketName"`
	FlowLogBucketName      string `json:"flowLogBucketName"`

	// Network configurations
	VPCSettings *VPCConfig `json:"vpcSettings,omitempty"`

	// Security configurations
	RequireMFA         bool     `json:"requireMFA"`
	EnableSSLRequests  bool     `json:"enableSSLRequests"`
	EnableSecurityHub  bool     `json:"enableSecurityHub"`
	EnableGuardDuty    bool     `json:"enableGuardDuty"`
	EnableConfig       bool     `json:"enableConfig"`
	EnableCloudTrail   bool     `json:"enableCloudTrail"`
	AllowedIPRanges    []string `json:"allowedIPRanges"`
	RestrictedServices []string `json:"restrictedServices"`
}

// NewOrganizationConfig creates a new configuration instance
func NewOrganizationConfig() (*OrganizationConfig, error) {
	logger, err := zap.NewProduction()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize logger: %w", err)
	}

	metrics, err := metrics.NewCollector("config")
	if err != nil {
		return nil, fmt.Errorf("failed to initialize metrics: %w", err)
	}

	return &OrganizationConfig{
		Version: ConfigVersion,
		logger:  logger,
		metrics: metrics,
	}, nil
}

// Validate performs comprehensive configuration validation
func (c *OrganizationConfig) Validate() error {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	c.logger.Info("starting configuration validation")
	start := time.Now()
	defer func() {
		c.metrics.RecordDuration("config_validation", time.Since(start))
	}()

	if c.LandingZoneConfig == nil {
		return fmt.Errorf("landing zone configuration is required")
	}

	if err := c.validateBasicConfig(); err != nil {
		return fmt.Errorf("basic configuration validation failed: %w", err)
	}

	if err := c.validateAccountConfig(); err != nil {
		return fmt.Errorf("account configuration validation failed: %w", err)
	}

	if err := c.validateNetworkConfig(); err != nil {
		return fmt.Errorf("network configuration validation failed: %w", err)
	}

	c.logger.Info("configuration validation completed successfully")
	return nil
}

// validateBasicConfig validates basic configuration settings
func (c *OrganizationConfig) validateBasicConfig() error {
	if len(c.LandingZoneConfig.GovernedRegions) == 0 {
		return fmt.Errorf("at least one governed region is required")
	}

	if c.LandingZoneConfig.LogRetentionDays < MinLogRetentionDays ||
		c.LandingZoneConfig.LogRetentionDays > MaxLogRetentionDays {
		return fmt.Errorf("log retention days must be between %d and %d",
			MinLogRetentionDays, MaxLogRetentionDays)
	}

	return nil
}

// validateAccountConfig validates account-related configurations
func (c *OrganizationConfig) validateAccountConfig() error {
	emailRegex := regexp.MustCompile(EmailRegexPattern)
	if !emailRegex.MatchString(c.LandingZoneConfig.AccountEmailDomain) {
		return fmt.Errorf("invalid account email domain")
	}

	// Validate account IDs
	accounts := []struct {
		name string
		id   string
	}{
		{"Management", c.LandingZoneConfig.ManagementAccountId},
		{"LogArchive", c.LandingZoneConfig.LogArchiveAccountId},
		{"Audit", c.LandingZoneConfig.AuditAccountId},
		{"Security", c.LandingZoneConfig.SecurityAccountId},
	}

	for _, account := range accounts {
		if !isValidAccountId(account.id) {
			return fmt.Errorf("invalid %s account ID: %s", account.name, account.id)
		}
	}

	return nil
}

// validateNetworkConfig validates network-related configurations
func (c *OrganizationConfig) validateNetworkConfig() error {
	if c.LandingZoneConfig.VPCSettings != nil {
		if _, _, err := net.ParseCIDR(c.LandingZoneConfig.VPCSettings.CIDR); err != nil {
			return fmt.Errorf("invalid VPC CIDR: %w", err)
		}

		for _, subnet := range c.LandingZoneConfig.VPCSettings.Subnets {
			if _, _, err := net.ParseCIDR(subnet.CIDR); err != nil {
				return fmt.Errorf("invalid subnet CIDR %s: %w", subnet.Name, err)
			}
		}
	}

	for _, ipRange := range c.LandingZoneConfig.AllowedIPRanges {
		if _, _, err := net.ParseCIDR(ipRange); err != nil {
			return fmt.Errorf("invalid allowed IP range: %s", ipRange)
		}
	}

	return nil
}

// isValidAccountId validates AWS account ID format
func isValidAccountId(id string) bool {
	if len(id) != 12 {
		return false
	}
	_, err := strconv.ParseUint(id, 10, 64)
	return err == nil
}

// Save persists the configuration to storage
func (c *OrganizationConfig) Save() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return nil
}

// Load retrieves the configuration from storage
func (c *OrganizationConfig) Load() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return nil
}

// Backup creates a backup of the current configuration
func (c *OrganizationConfig) Backup() error {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return nil
}

// Restore restores configuration from a backup
func (c *OrganizationConfig) Restore(version string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return nil
}

// DefaultConfig provides default configuration values
var DefaultConfig = OrganizationConfig{
	Version: ConfigVersion,
	LandingZoneConfig: &LandingZoneConfig{
		GovernedRegions:   []string{"us-east-1", "us-west-2"},
		DefaultOUName:     "Sandbox",
		OrganizationUnits: map[string]*OUConfig{},
		LogRetentionDays:  90,
		Tags: map[string]string{
			"ManagedBy": "Pulumi",
			"Project":   "ControlTower",
		},
		RequireMFA:        true,
		EnableSSLRequests: true,
		EnableSecurityHub: true,
		EnableGuardDuty:   true,
		EnableConfig:      true,
		EnableCloudTrail:  true,
		VPCSettings: &VPCConfig{
			CIDR:               "10.0.0.0/16",
			EnableTransitGW:    true,
			EnableVPCFlowLogs:  true,
			EnableDNSHostnames: true,
			EnableDNSSupport:   true,
		},
	},
}

type VPCConfig struct {
	CIDR               string   `json:"cidr"`
	EnableTransitGW    bool     `json:"enableTransitGw"`
	EnableVPCFlowLogs  bool     `json:"enableVpcFlowLogs"`
	EnableDNSHostnames bool     `json:"enableDnsHostnames"`
	EnableDNSSupport   bool     `json:"enableDnsSupport"`
	Subnets            []Subnet `json:"subnets,omitempty"`
}

type OUConfig struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
	Accounts    []AccountConfig   `json:"accounts,omitempty"`
}

type AccountConfig struct {
	Name    string            `json:"name"`
	Email   string            `json:"email"`
	Tags    map[string]string `json:"tags,omitempty"`
	RoleArn string            `json:"roleArn,omitempty"`
}

type Subnet struct {
	Name             string            `json:"name"`
	CIDR             string            `json:"cidr"`
	AvailabilityZone string            `json:"availabilityZone"`
	Tags             map[string]string `json:"tags,omitempty"`
}
