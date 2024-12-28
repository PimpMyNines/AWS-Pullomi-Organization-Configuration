// Copyright (c) 2024 Shawn LoPresto
// This source code is licensed under the MIT license found in the
// LICENSE file in the root directory of this source tree.

// Package logging provides centralized logging functionality for the application.
// Version: 1.0.0
package logging

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

const (
	// Default logging configurations
	defaultLogPath      = "/var/log/aws-organization"
	defaultMaxSize      = 100 // megabytes
	defaultMaxBackups   = 5
	defaultMaxAge       = 30 // days
	defaultLogFileName  = "aws-organization.log"
	defaultErrorLogName = "error.log"
)

var (
	// Global logger instance
	globalLogger *zap.Logger
	once         sync.Once
)

// LoggerConfig represents the configuration for the logger
type LoggerConfig struct {
	LogPath       string
	MaxSize       int // megabytes
	MaxBackups    int
	MaxAge        int // days
	Compress      bool
	Development   bool
	EnableConsole bool
}

// NewLogger creates or returns the singleton logger instance
func NewLogger(component string) (*zap.Logger, error) {
	var err error
	once.Do(func() {
		globalLogger, err = initLogger(component, getDefaultConfig())
	})

	if err != nil {
		return nil, fmt.Errorf("failed to initialize logger: %w", err)
	}

	return globalLogger.Named(component), nil
}

// getDefaultConfig returns the default logging configuration
func getDefaultConfig() *LoggerConfig {
	return &LoggerConfig{
		LogPath:       defaultLogPath,
		MaxSize:       defaultMaxSize,
		MaxBackups:    defaultMaxBackups,
		MaxAge:        defaultMaxAge,
		Compress:      true,
		Development:   false,
		EnableConsole: true,
	}
}

// initLogger initializes the logger with the given configuration
func initLogger(component string, config *LoggerConfig) (*zap.Logger, error) {
	// Create log directory if it doesn't exist
	if err := os.MkdirAll(config.LogPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	// Configure main log file
	mainLog := &lumberjack.Logger{
		Filename:   filepath.Join(config.LogPath, defaultLogFileName),
		MaxSize:    config.MaxSize,
		MaxBackups: config.MaxBackups,
		MaxAge:     config.MaxAge,
		Compress:   config.Compress,
	}

	// Configure error log file
	errorLog := &lumberjack.Logger{
		Filename:   filepath.Join(config.LogPath, defaultErrorLogName),
		MaxSize:    config.MaxSize,
		MaxBackups: config.MaxBackups,
		MaxAge:     config.MaxAge,
		Compress:   config.Compress,
	}

	// Create encoder configuration
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "timestamp",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "message",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	// Create cores
	cores := []zapcore.Core{
		zapcore.NewCore(
			zapcore.NewJSONEncoder(encoderConfig),
			zapcore.AddSync(mainLog),
			zapcore.InfoLevel,
		),
		zapcore.NewCore(
			zapcore.NewJSONEncoder(encoderConfig),
			zapcore.AddSync(errorLog),
			zapcore.ErrorLevel,
		),
	}

	// Add console logging if enabled
	if config.EnableConsole {
		cores = append(cores, zapcore.NewCore(
			zapcore.NewConsoleEncoder(encoderConfig),
			zapcore.AddSync(os.Stdout),
			zapcore.InfoLevel,
		))
	}

	// Create options
	opts := []zap.Option{
		zap.AddCaller(),
		zap.AddStacktrace(zapcore.ErrorLevel),
		zap.Fields(
			zap.String("component", component),
			zap.String("version", "1.0.0"),
		),
	}

	// Create logger
	core := zapcore.NewTee(cores...)
	logger := zap.New(core, opts...)

	return logger, nil
}

// WithContext adds context fields to the logger
func WithContext(logger *zap.Logger, fields map[string]interface{}) *zap.Logger {
	if len(fields) == 0 {
		return logger
	}

	zapFields := make([]zap.Field, 0, len(fields))
	for k, v := range fields {
		zapFields = append(zapFields, zap.Any(k, v))
	}

	return logger.With(zapFields...)
}

// Sync flushes any buffered log entries
func Sync() error {
	if globalLogger != nil {
		return globalLogger.Sync()
	}
	return nil
}

// LoggerMiddleware provides a middleware for logging HTTP requests
func LoggerMiddleware(logger *zap.Logger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			defer func() {
				logger.Info("http request",
					zap.String("method", r.Method),
					zap.String("path", r.URL.Path),
					zap.Int("status", ww.Status()),
					zap.Duration("duration", time.Since(start)),
					zap.String("remote_addr", r.RemoteAddr),
					zap.String("user_agent", r.UserAgent()),
				)
			}()

			next.ServeHTTP(ww, r)
		})
	}
}

// RecoveryMiddleware provides a middleware for recovering from panics
func RecoveryMiddleware(logger *zap.Logger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					logger.Error("panic recovered",
						zap.Any("error", err),
						zap.String("stack", string(debug.Stack())),
					)
					http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}
