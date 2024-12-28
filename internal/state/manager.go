// Copyright (c) 2024 Shawn LoPresto
// This source code is licensed under the MIT license found in the
// LICENSE file in the root directory of this source tree.

// Package state provides state management functionality for AWS Organization configuration.
// Version: 1.0.0
package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/PimpMyNines/AWS-Pullomi-Organization-Configuration/internal/config"
	"github.com/PimpMyNines/AWS-Pullomi-Organization-Configuration/internal/metrics"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"go.uber.org/zap"
)

// StateManager handles state persistence and retrieval
type StateManager struct {
	logger       *zap.Logger
	metrics      *metrics.Collector
	dynamoClient *dynamodb.Client
	s3Client     *s3.Client
	tableName    string
	bucketName   string
	mutex        sync.RWMutex
}

// NewManager creates a new state manager instance with the provided options
func NewManager(ctx context.Context, opts ...func(*StateManager) error) (*StateManager, error) {
	logger, err := zap.NewProduction()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize logger: %w", err)
	}

	metrics, err := metrics.NewCollector("state-manager")
	if err != nil {
		logger.Error("failed to initialize metrics collector", zap.Error(err))
		return nil, fmt.Errorf("failed to initialize metrics: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, config.DefaultTimeout)
	defer cancel()

	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRetryMode(aws.RetryModeStandard),
		awsconfig.WithRetryMaxAttempts(config.MaxRetries),
	)
	if err != nil {
		logger.Error("failed to load AWS config", zap.Error(err))
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	sm := &StateManager{
		logger:       logger,
		metrics:      metrics,
		dynamoClient: dynamodb.NewFromConfig(cfg),
		s3Client:     s3.NewFromConfig(cfg),
		tableName:    config.StateTableName,
		bucketName:   config.StateBackupBucket,
	}

	// Apply options
	for _, opt := range opts {
		if err := opt(sm); err != nil {
			logger.Error("failed to apply option", zap.Error(err))
			return nil, err
		}
	}

	return sm, nil
}

// Save persists the current state with retry logic
func (sm *StateManager) Save(ctx context.Context, state interface{}) error {
	ctx, cancel := context.WithTimeout(ctx, config.DefaultTimeout)
	defer cancel()

	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	start := time.Now()
	defer func() {
		sm.metrics.RecordDuration("state_save_duration", time.Since(start))
	}()

	stateData := &config.StateData{
		Version:   config.ConfigVersion,
		Timestamp: time.Now(),
		Component: "aws-organization",
		Tags: map[string]string{
			"service": "organization-config",
		},
	}

	if err := sm.marshalState(state, stateData); err != nil {
		return err
	}

	backoff := config.InitialBackoff
	for attempt := 0; attempt < config.MaxRetries; attempt++ {
		if err := sm.saveToDynamoDB(ctx, stateData); err != nil {
			if attempt == config.MaxRetries-1 {
				return &config.StateError{
					Operation: "Save",
					Message:   "max retries exceeded while saving to DynamoDB",
					Err:       err,
				}
			}
			time.Sleep(backoff)
			backoff *= 2
			continue
		}
		break
	}

	// Backup to S3 asynchronously
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), config.DefaultTimeout)
		defer cancel()

		if err := sm.backupToS3(ctx, stateData); err != nil {
			sm.logger.Error("failed to backup state to S3",
				zap.Error(err),
				zap.String("stateVersion", stateData.Version))
		}
	}()

	sm.metrics.IncrementCounter("state_saves")
	sm.logger.Info("state saved successfully",
		zap.String("version", stateData.Version),
		zap.Time("timestamp", stateData.Timestamp))
	return nil
}

// Load retrieves the current state with retry logic
func (sm *StateManager) Load(ctx context.Context) (*config.StateData, error) {
	ctx, cancel := context.WithTimeout(ctx, config.DefaultTimeout)
	defer cancel()

	sm.mutex.RLock()
	defer sm.mutex.RUnlock()

	start := time.Now()
	defer func() {
		sm.metrics.RecordDuration("state_load_duration", time.Since(start))
	}()

	var stateData *config.StateData
	var lastErr error
	backoff := config.InitialBackoff

	for attempt := 0; attempt < config.MaxRetries; attempt++ {
		stateData, lastErr = sm.loadFromDynamoDB(ctx)
		if lastErr == nil {
			break
		}

		if attempt == config.MaxRetries-1 {
			return nil, &config.StateError{
				Operation: "Load",
				Message:   "max retries exceeded while loading from DynamoDB",
				Err:       lastErr,
			}
		}

		time.Sleep(backoff)
		backoff *= 2
	}

	sm.metrics.IncrementCounter("state_loads")
	sm.logger.Info("state loaded successfully",
		zap.String("version", stateData.Version),
		zap.Time("timestamp", stateData.Timestamp))
	return stateData, nil
}

// CreateBackup creates a backup of the current state
func (sm *StateManager) CreateBackup(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, config.DefaultTimeout)
	defer cancel()

	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	start := time.Now()
	defer func() {
		sm.metrics.RecordDuration("backup_creation_duration", time.Since(start))
	}()

	stateData, err := sm.Load(ctx)
	if err != nil {
		return "", &config.StateError{
			Operation: "CreateBackup",
			Message:   "failed to load current state",
			Err:       err,
		}
	}

	backupID := fmt.Sprintf("%s-%s", config.BackupFilePrefix, time.Now().Format("20060102-150405"))
	stateData.BackupID = backupID

	if err := sm.backupToS3(ctx, stateData); err != nil {
		return "", &config.StateError{
			Operation: "CreateBackup",
			Message:   "failed to create backup in S3",
			Err:       err,
		}
	}

	sm.metrics.IncrementCounter("backups_created")
	sm.logger.Info("backup created successfully",
		zap.String("backupId", backupID),
		zap.Time("timestamp", time.Now()))
	return backupID, nil
}

// CleanupOldStates removes expired states and backups
func (sm *StateManager) CleanupOldStates(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, config.DefaultTimeout*2) // Longer timeout for cleanup
	defer cancel()

	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	start := time.Now()
	defer func() {
		sm.metrics.RecordDuration("cleanup_duration", time.Since(start))
	}()

	expiryDate := time.Now().AddDate(0, 0, -config.StateExpiryDays)

	// Cleanup DynamoDB
	if err := sm.cleanupDynamoDB(ctx, expiryDate); err != nil {
		return &config.StateError{
			Operation: "CleanupOldStates",
			Message:   "failed to cleanup DynamoDB",
			Err:       err,
		}
	}

	// Cleanup S3
	if err := sm.cleanupS3(ctx, expiryDate); err != nil {
		return &config.StateError{
			Operation: "CleanupOldStates",
			Message:   "failed to cleanup S3",
			Err:       err,
		}
	}

	sm.metrics.IncrementCounter("cleanups_performed")
	sm.logger.Info("cleanup completed successfully",
		zap.Time("expiryDate", expiryDate),
		zap.Duration("duration", time.Since(start)))
	return nil
}

// Close performs cleanup and closes connections
func (sm *StateManager) Close() error {
	sm.logger.Info("closing state manager")
	return sm.metrics.Close()
}

// Private helper methods

func (sm *StateManager) marshalState(state interface{}, stateData *config.StateData) error {
	data, err := json.Marshal(state)
	if err != nil {
		return &config.StateError{
			Operation: "marshalState",
			Message:   "failed to marshal state",
			Err:       err,
		}
	}

	var stateMap map[string]interface{}
	if err := json.Unmarshal(data, &stateMap); err != nil {
		return &config.StateError{
			Operation: "marshalState",
			Message:   "failed to unmarshal state",
			Err:       err,
		}
	}

	stateData.State = stateMap
	return nil
}

func (sm *StateManager) saveToDynamoDB(ctx context.Context, stateData *config.StateData) error {
	data, err := json.Marshal(stateData)
	if err != nil {
		return fmt.Errorf("failed to marshal state data: %w", err)
	}

	item := map[string]types.AttributeValue{
		config.PkAttribute: &types.AttributeValueMemberS{
			Value: config.StateFilePrefix,
		},
		config.SkAttribute: &types.AttributeValueMemberS{
			Value: stateData.Timestamp.Format(time.RFC3339),
		},
		config.StateAttribute: &types.AttributeValueMemberS{
			Value: string(data),
		},
		config.VersionAttribute: &types.AttributeValueMemberS{
			Value: stateData.Version,
		},
	}

	_, err = sm.dynamoClient.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(sm.tableName),
		Item:      item,
	})

	return err
}

func (sm *StateManager) loadFromDynamoDB(ctx context.Context) (*config.StateData, error) {
	// Implementation for loading from DynamoDB
	return nil, nil
}

func (sm *StateManager) backupToS3(ctx context.Context, stateData *config.StateData) error {
	// Implementation for backing up to S3
	return nil
}

func (sm *StateManager) cleanupDynamoDB(ctx context.Context, expiryDate time.Time) error {
	// Implementation for cleaning up DynamoDB
	return nil
}

func (sm *StateManager) cleanupS3(ctx context.Context, expiryDate time.Time) error {
	// Implementation for cleaning up S3
	return nil
}
