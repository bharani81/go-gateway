// Package observability sets up the structured logger used throughout the gateway.
package observability

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// NewLogger creates a production-ready zap logger with JSON output.
//
// JSON format is chosen over console format because it is machine-parsable by
// log aggregation systems (Loki, Datadog, CloudWatch, ELK) out of the box.
//
// logging is configured to:
//   - Write to stdout (container-friendly — no log file rotation needed)
//   - Include caller information for debugging
//   - Use RFC3339Nano timestamps matching the access log format in the design doc
//   - Emit level field as string ("info", "error") rather than integer
func NewLogger(level string) (*zap.Logger, error) {
	var zapLevel zapcore.Level
	if err := zapLevel.UnmarshalText([]byte(level)); err != nil {
		zapLevel = zapcore.InfoLevel
	}

	cfg := zap.Config{
		Level:            zap.NewAtomicLevelAt(zapLevel),
		Development:      false,
		Encoding:         "json",
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
		EncoderConfig: zapcore.EncoderConfig{
			TimeKey:        "ts",
			LevelKey:       "level",
			NameKey:        "logger",
			CallerKey:      "caller",
			MessageKey:     "msg",
			StacktraceKey:  "stacktrace",
			LineEnding:     zapcore.DefaultLineEnding,
			EncodeLevel:    zapcore.LowercaseLevelEncoder, // "info", "error"
			EncodeTime:     zapcore.RFC3339NanoTimeEncoder,
			EncodeDuration: zapcore.MillisDurationEncoder,
			EncodeCaller:   zapcore.ShortCallerEncoder,
		},
	}

	return cfg.Build()
}
