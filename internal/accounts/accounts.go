// Copyright (c) 2024 Shawn LoPresto
// This source code is licensed under the MIT license found in the
// LICENSE file in the root directory of this source tree.

// Package accounts provides functionality for managing AWS Organization accounts.
// Version: 1.0.0
package accounts

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/PimpMyNines/AWS-Pullomi-Organization-Configuration/internal/config"
	"github.com/PimpMyNines/AWS-Pullomi-Organization-Configuration/internal/metrics"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	awsOrg "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/organizations"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ssm"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

// Constants for account management
const (
	// SSM parameter path format for account information
	ssmAccountPathFmt = "/organization/accounts/%s"

	// Default role name for account access
	defaultAccessRoleName = "OrganizationAccountAccessRole"

	// Account status values
	statusActive    = "ACTIVE"
	statusSuspended = "SUSPENDED"

	// Validation constants
	emailRegexPattern = `^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`
	maxAccountNameLen = 50
	minAccountNameLen = 3

	// Rate limiting
	rateLimit = 5
	rateBurst = 10

	// Retry configuration
	maxRetryAttempts = 3
	baseRetryDelay   = time.Second * 2
	maxRetryDelay    = time.Second * 30
)

// AccountService defines the interface for account operations
type AccountService interface {
	CreateAccount(ctx *pulumi.Context, config *AccountConfig) (*awsOrg.Account, error)
	SuspendAccount(ctx *pulumi.Context, accountID string) error
	ResumeAccount(ctx *pulumi.Context, accountID string) error
	MoveAccount(ctx *pulumi.Context, accountID string, targetOUID string) error
	GetAccountStatus(ctx *pulumi.Context, accountID string) (string, error)
	ListAccounts(ctx *pulumi.Context) ([]*AccountInfo, error)
	Backup(ctx context.Context) error
	Restore(ctx context.Context, backupID string) error
}

// AccountConfig represents the configuration for account creation
type AccountConfig struct {
	Name       string             `json:"name"`
	Email      string             `json:"email"`
	ParentOUID pulumi.StringInput `json:"parentOuId"`
	Tags       map[string]string  `json:"tags"`
}

// AccountInfo represents account information
type AccountInfo struct {
	ID     string            `json:"id"`
	ARN    string            `json:"arn"`
	Name   string            `json:"name"`
	Email  string            `json:"email"`
	Status string            `json:"status"`
	Tags   map[string]string `json:"tags"`
}

// AccountManager handles AWS account operations
type AccountManager struct {
	logger   *zap.Logger
	metrics  *metrics.Collector
	limiter  *rate.Limiter
	mutex    sync.RWMutex
	accounts map[string]*AccountInfo
	emailRE  *regexp.Regexp
}

// NewAccountManager creates a new account manager instance
func NewAccountManager(ctx context.Context) (*AccountManager, error) {
	logger, err := zap.NewProduction()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize logger: %w", err)
	}

	metrics, err := metrics.NewCollector("accounts")
	if err != nil {
		return nil, fmt.Errorf("failed to initialize metrics: %w", err)
	}

	emailRE, err := regexp.Compile(emailRegexPattern)
	if err != nil {
		return nil, fmt.Errorf("failed to compile email regex: %w", err)
	}

	return &AccountManager{
		logger:   logger,
		metrics:  metrics,
		limiter:  rate.NewLimiter(rate.Limit(rateLimit), rateBurst),
		accounts: make(map[string]*AccountInfo),
		emailRE:  emailRE,
	}, nil
}

// CreateAccount creates a new AWS account with retry logic
func (am *AccountManager) CreateAccount(ctx *pulumi.Context, accountConfig *AccountConfig) (*awsOrg.Account, error) {
	start := time.Now()
	defer func() {
		am.metrics.RecordDuration("account_creation", time.Since(start))
	}()

	am.logger.Info("creating account",
		zap.String("name", accountConfig.Name),
		zap.String("email", accountConfig.Email))

	if err := am.validateAccountConfig(accountConfig); err != nil {
		return nil, err
	}

	var account *awsOrg.Account
	operation := func() error {
		if err := am.limiter.Wait(ctx.Context()); err != nil {
			return fmt.Errorf("rate limit exceeded: %w", err)
		}

		var err error
		account, err = awsOrg.NewAccount(ctx, accountConfig.Name, &awsOrg.AccountArgs{
			Email:    pulumi.String(accountConfig.Email),
			Name:     pulumi.String(accountConfig.Name),
			ParentId: accountConfig.ParentOUID,
			RoleName: pulumi.String(defaultAccessRoleName),
			Tags:     pulumi.ToStringMap(accountConfig.Tags),
		})
		return err
	}

	if err := retryWithBackoff(operation, maxRetryAttempts, baseRetryDelay); err != nil {
		am.logger.Error("failed to create account",
			zap.String("name", accountConfig.Name),
			zap.Error(err))
		return nil, fmt.Errorf("failed to create account %s: %w", accountConfig.Name, err)
	}

	// Store account information in SSM Parameter Store
	if err := am.storeAccountInfo(ctx, account, accountConfig); err != nil {
		return nil, err
	}

	am.logger.Info("account created successfully",
		zap.String("name", accountConfig.Name))
	am.metrics.IncrementCounter("accounts_created")

	return account, nil
}

// validateAccountConfig validates account configuration
func (am *AccountManager) validateAccountConfig(config *AccountConfig) error {
	if len(config.Name) < minAccountNameLen || len(config.Name) > maxAccountNameLen {
		return fmt.Errorf("account name must be between %d and %d characters", minAccountNameLen, maxAccountNameLen)
	}

	if !am.emailRE.MatchString(config.Email) {
		return fmt.Errorf("invalid email format: %s", config.Email)
	}

	if config.ParentOUID == nil {
		return fmt.Errorf("parent OU ID is required")
	}

	return nil
}

// storeAccountInfo stores account information in SSM Parameter Store
func (am *AccountManager) storeAccountInfo(ctx *pulumi.Context, account *awsOrg.Account, config *AccountConfig) error {
	_, err := ssm.NewParameter(ctx, fmt.Sprintf(ssmAccountPathFmt, config.Name), &ssm.ParameterArgs{
		Type: pulumi.String("SecureString"),
		Value: pulumi.All(account.Id, account.Arn).ApplyT(func(args []interface{}) (string, error) {
			info := AccountInfo{
				ID:     args[0].(string),
				ARN:    args[1].(string),
				Name:   config.Name,
				Email:  config.Email,
				Status: statusActive,
				Tags:   config.Tags,
			}
			value, err := json.Marshal(info)
			if err != nil {
				return "", fmt.Errorf("failed to marshal account info: %w", err)
			}
			return string(value), nil
		}).(pulumi.StringOutput),
		Description: pulumi.Sprintf("Information for Account: %s", config.Name),
		Tags:        pulumi.ToStringMap(config.Tags),
	})

	return err
}

// CreateDefaultAccounts creates the default accounts required for AWS Control Tower
func CreateDefaultAccounts(ctx *pulumi.Context, securityOUID pulumi.StringInput, cfg *config.LandingZoneConfig) error {
	am, err := NewAccountManager(ctx.Context())
	if err != nil {
		return err
	}

	defaultAccounts := []AccountConfig{
		{
			Name:       "AFT-Management",
			Email:      fmt.Sprintf("aft-management@%s", cfg.LandingZoneConfig.AccountEmailDomain),
			ParentOUID: securityOUID,
			Tags:       cfg.LandingZoneConfig.Tags,
		},
		{
			Name:       "AFT-Networking",
			Email:      fmt.Sprintf("aft-networking@%s", cfg.LandingZoneConfig.AccountEmailDomain),
			ParentOUID: securityOUID,
			Tags:       cfg.LandingZoneConfig.Tags,
		},
	}

	for _, accountCfg := range defaultAccounts {
		if _, err := am.CreateAccount(ctx, &accountCfg); err != nil {
			return fmt.Errorf("failed to create account %s: %w", accountCfg.Name, err)
		}
	}

	return nil
}

// retryWithBackoff implements exponential backoff retry logic
func retryWithBackoff(operation func() error, maxAttempts int, baseDelay time.Duration) error {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := operation(); err == nil {
			return nil
		} else {
			lastErr = err
			if attempt < maxAttempts {
				delay := time.Duration(float64(baseDelay) * float64(attempt))
				if delay > maxRetryDelay {
					delay = maxRetryDelay
				}
				time.Sleep(delay)
			}
		}
	}
	return fmt.Errorf("operation failed after %d attempts: %w", maxAttempts, lastErr)
}

// Backup creates a backup of account configurations
func (am *AccountManager) Backup(ctx context.Context) error {
	am.mutex.RLock()
	defer am.mutex.RUnlock()

	// Implementation for backup functionality
	return nil
}

// Restore restores account configurations from a backup
func (am *AccountManager) Restore(ctx context.Context, backupID string) error {
	am.mutex.Lock()
	defer am.mutex.Unlock()

	// Implementation for restore functionality
	return nil
}
