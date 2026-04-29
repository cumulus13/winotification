package forwarder

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cumulus13/WiNotification/internal/capture"
	"github.com/cumulus13/WiNotification/internal/config"
	redis "github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

// RedisForwarder stores notifications in Redis (with optional TTL) and
// optionally publishes them to a pub/sub channel.
type RedisForwarder struct {
	cfg    config.RedisConfig
	log    *logrus.Logger
	client *redis.Client
}

// NewRedisForwarder connects to Redis and returns a ready forwarder.
func NewRedisForwarder(cfg config.RedisConfig, log *logrus.Logger) (*RedisForwarder, error) {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping %s: %w", addr, err)
	}

	log.Infof("[redis] connected to %s db=%d", addr, cfg.DB)
	return &RedisForwarder{cfg: cfg, log: log, client: client}, nil
}

func (r *RedisForwarder) Name() string { return "redis" }

func (r *RedisForwarder) Forward(ctx context.Context, n *capture.Notification) error {
	data, err := json.Marshal(n)
	if err != nil {
		return err
	}

	key := r.cfg.KeyPrefix + n.ID
	ttl := time.Duration(r.cfg.TTL) * time.Second

	// SET with TTL
	if err := r.client.Set(ctx, key, data, ttl).Err(); err != nil {
		return fmt.Errorf("redis set: %w", err)
	}

	// Optional PUBLISH
	if r.cfg.Publish && r.cfg.PubsubChannel != "" {
		if err := r.client.Publish(ctx, r.cfg.PubsubChannel, string(data)).Err(); err != nil {
			r.log.WithError(err).Warn("[redis] publish failed")
		}
	}
	return nil
}

func (r *RedisForwarder) Close() error {
	return r.client.Close()
}
