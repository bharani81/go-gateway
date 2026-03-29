package ratelimit

import (
	"context"

	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// buildRedisLimiter is the production constructor that wires go-redis as the backend.
// It pre-loads the Lua script SHA via SCRIPT LOAD so every subsequent call uses
// EVALSHA (faster than EVAL — no script transmission overhead).
func buildRedisLimiter(cfg RedisConfig, windowMs, limit int64, fallback Limiter) *RedisLimiter {
	opts := &goredis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		PoolSize:     cfg.PoolSize,
		MinIdleConns: cfg.MinIdleConns,
	}
	if cfg.DialTimeout > 0 {
		opts.DialTimeout = cfg.DialTimeout
	}
	if cfg.ReadTimeout > 0 {
		opts.ReadTimeout = cfg.ReadTimeout
	}
	if cfg.WriteTimeout > 0 {
		opts.WriteTimeout = cfg.WriteTimeout
	}

	client := goredis.NewClient(opts)
	log := zap.NewNop() // replaced by caller if needed

	// Pre-load the Lua script so we can use EVALSHA (avoids re-sending script on each call).
	script := goredis.NewScript(luaSlidingWindow)

	evalFn := func(ctx context.Context, key string) ([]int64, error) {
		result, err := script.Run(ctx, client, []string{key}, windowMs, limit).Int64Slice()
		return result, err
	}

	closeFn := func() error {
		return client.Close()
	}

	return newRedisLimiter(cfg.Addr, windowMs, limit, evalFn, closeFn, fallback, log)
}
