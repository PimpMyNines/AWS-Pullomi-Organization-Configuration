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
	awsOrg "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/organizations"
	awsssm "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ssm"
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
	_, err := awsssm.NewParameter(ctx, fmt.Sprintf(ssmAccountPathFmt, config.Name), &awsssm.ParameterArgs{
		Type: pulumi.String("SecureString"),
		Value: pulumi.All(account.ID(), account.Arn).ApplyT(func(args []interface{}) (string, error) {
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
func CreateDefaultAccounts(ctx *pulumi.Context, securityOUID pulumi.StringInput, cfg *config.OrganizationConfig) error {
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

// BackupInfo represents the structure of a backup
type BackupInfo struct {
	ID        string                  `json:"id"`
	Timestamp time.Time               `json:"timestamp"`
	Accounts  map[string]*AccountInfo `json:"accounts"`
}

// Backup creates a backup of account configurations
func (am *AccountManager) Backup(ctx context.Context) error {
	am.mutex.RLock()
	defer am.mutex.RUnlock()

	backupInfo := BackupInfo{
		ID:        fmt.Sprintf("backup-%s", time.Now().Format("20060102-150405")),
		Timestamp: time.Now(),
		Accounts:  am.accounts,
	}

	backupData, err := json.Marshal(backupInfo)
	if err != nil {
		return fmt.Errorf("failed to marshal backup data: %w", err)
	}

	pulumiCtx := getPulumiContextFromContext(ctx)
	if pulumiCtx == nil {
		return fmt.Errorf("pulumi context not found in context")
	}

	_, err = awsssm.NewParameter(pulumiCtx,
		fmt.Sprintf("backup-%s", backupInfo.ID),
		&awsssm.ParameterArgs{
			Name:        pulumi.String(fmt.Sprintf("/organization/backups/%s", backupInfo.ID)),
			Type:        pulumi.String("SecureString"),
			Value:       pulumi.String(string(backupData)),
			Description: pulumi.String(fmt.Sprintf("Account configuration backup created at %s", backupInfo.Timestamp)),
		},
		pulumi.DeleteBeforeReplace(true),
	)
	if err != nil {
		return fmt.Errorf("failed to store backup in SSM: %w", err)
	}

	am.logger.Info("backup created successfully",
		zap.String("backupID", backupInfo.ID),
		zap.Time("timestamp", backupInfo.Timestamp),
		zap.Int("accountCount", len(backupInfo.Accounts)))

	am.metrics.IncrementCounter("backups_created")
	return nil
}

// Restore restores account configurations from a backup
func (am *AccountManager) Restore(ctx context.Context, backupID string) error {
	am.mutex.Lock()
	defer am.mutex.Unlock()

	pulumiCtx := getPulumiContextFromContext(ctx)
	if pulumiCtx == nil {
		return fmt.Errorf("pulumi context not found in context")
	}

	// Changed this line to use pulumiCtx instead of ctx
	paramValue, err := awsssm.LookupParameter(pulumiCtx, &awsssm.LookupParameterArgs{
		Name:           fmt.Sprintf("/organization/backups/%s", backupID),
		WithDecryption: pulumi.BoolRef(true),
	})
	if err != nil {
		return fmt.Errorf("failed to retrieve backup %s: %w", backupID, err)
	}

	var backupInfo BackupInfo
	if err := json.Unmarshal([]byte(paramValue.Value), &backupInfo); err != nil {
		return fmt.Errorf("failed to unmarshal backup data: %w", err)
	}

	if backupInfo.Accounts == nil {
		return fmt.Errorf("invalid backup data: accounts map is nil")
	}

	am.accounts = make(map[string]*AccountInfo)
	for id, account := range backupInfo.Accounts {
		am.accounts[id] = account
	}

	am.logger.Info("restore completed successfully",
		zap.String("backupID", backupID),
		zap.Time("backupTimestamp", backupInfo.Timestamp),
		zap.Int("restoredAccounts", len(backupInfo.Accounts)))

	am.metrics.IncrementCounter("backups_restored")
	return nil
}

// Helper function to get Pulumi context from context.Context
func getPulumiContextFromContext(ctx context.Context) *pulumi.Context {
	if ctx == nil {
		return nil
	}
	if pulumiCtx, ok := ctx.Value("pulumi.Context").(*pulumi.Context); ok {
		return pulumiCtx
	}
	return nil
}

// Add this to your AccountManager struct
func (am *AccountManager) WithPulumiContext(ctx *pulumi.Context) context.Context {
	return context.WithValue(context.Background(), "pulumi.Context", ctx)
}
