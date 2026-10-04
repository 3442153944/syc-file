package database

import (
	"context"
	"fmt"
	"github.com/redis/go-redis/v9"
	"syc-file/config"
)

func InitRedis(cfg config.RedisConfig) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		DB:       cfg.DB,
		Password: cfg.Password,
	})

	// 测试连接
	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, err
	}

	return client, nil
}
