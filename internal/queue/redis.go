package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/sumith2425/sentinel-judge/internal/models"
)

const (
	SubmissionQueueKey = "queue:submissions"
	StatusKeyPrefix    = "status:"
	ChannelKeyPrefix   = "updates:"
)

type RedisClient struct {
	client *redis.Client
}

func NewRedisClient(addr, password string, db int) (*RedisClient, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, err
	}

	return &RedisClient{client: rdb}, nil
}

func (r *RedisClient) Close() error {
	return r.client.Close()
}

func (r *RedisClient) AllowRateLimit(ctx context.Context, key string, limit int64, window time.Duration) (bool, error) {
	pipe := r.client.TxPipeline()
	incr := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, window)

	_, err := pipe.Exec(ctx)
	if err != nil {
		return false, err
	}

	count := incr.Val()
	if count > limit {
		return false, nil
	}
	return true, nil
}

func (r *RedisClient) PushSubmission(ctx context.Context, sub *models.Submission) error {
	payload, err := json.Marshal(sub)
	if err != nil {
		return err
	}
	return r.client.LPush(ctx, SubmissionQueueKey, payload).Err()
}

func (r *RedisClient) PopSubmission(ctx context.Context, timeout time.Duration) (*models.Submission, error) {
	res, err := r.client.BRPop(ctx, timeout, SubmissionQueueKey).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var sub models.Submission
	if err := json.Unmarshal([]byte(res[1]), &sub); err != nil {
		return nil, err
	}
	return &sub, nil
}

func (r *RedisClient) SetLiveStatus(ctx context.Context, update *models.LiveStatusUpdate) error {
	key := fmt.Sprintf("%s%s", StatusKeyPrefix, update.SubmissionID.String())
	payload, err := json.Marshal(update)
	if err != nil {
		return err
	}

	pipe := r.client.Pipeline()
	pipe.Set(ctx, key, payload, 2*time.Hour)

	channel := fmt.Sprintf("%s%s", ChannelKeyPrefix, update.SubmissionID.String())
	pipe.Publish(ctx, channel, payload)

	_, err = pipe.Exec(ctx)
	return err
}

func (r *RedisClient) GetLiveStatus(ctx context.Context, id uuid.UUID) (*models.LiveStatusUpdate, error) {
	key := fmt.Sprintf("%s%s", StatusKeyPrefix, id.String())
	data, err := r.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var update models.LiveStatusUpdate
	if err := json.Unmarshal(data, &update); err != nil {
		return nil, err
	}
	return &update, nil
}