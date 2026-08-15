package repository

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// One sorted set per room:
//
//	presence:room:<room>   member = "<user_id>"   score = expiry epoch millis
//
// Redis has no per-member TTL, so expiry is logical: writes stamp an expiry
// score, and the read path prunes anything already past it. The trade-off is
// that expiry becomes our job on read instead of Redis's. The key-level EXPIRE
// (refreshed on every write) is what reclaims rooms nobody ever reads again.
type PresenceRepository struct {
	redisClient *redis.Client
	ttl         time.Duration
}

// ttl is a constructor argument rather than a hard-coded field so the
// verification script can shrink it and watch entries expire in seconds.
func NewPresenceRepository(redisClient *redis.Client, ttl time.Duration) *PresenceRepository {
	return &PresenceRepository{
		redisClient: redisClient,
		ttl:         ttl,
	}
}

func (r *PresenceRepository) SetOnline(room string, userID int64) error {
	ctx := context.Background()
	key := roomKey(room)

	pipe := r.redisClient.TxPipeline()
	pipe.ZAdd(ctx, key, redis.Z{
		Score:  float64(r.expiryMillis()),
		Member: strconv.FormatInt(userID, 10),
	})
	pipe.Expire(ctx, key, 2*r.ttl)

	_, err := pipe.Exec(ctx)

	return err
}

func (r *PresenceRepository) SetOffline(room string, userID int64) error {
	ctx := context.Background()
	key := roomKey(room)

	pipe := r.redisClient.TxPipeline()
	pipe.ZRem(ctx, key, strconv.FormatInt(userID, 10))
	// A no-op if ZRem emptied (and therefore deleted) the key.
	pipe.Expire(ctx, key, 2*r.ttl)

	_, err := pipe.Exec(ctx)

	return err
}

// Refresh re-stamps every currently connected user in one round trip. It is
// idempotent, which is what makes the heartbeat safe to run from several
// chat-service instances at once (month 3).
func (r *PresenceRepository) Refresh(room string, userIDs []int64) error {
	if len(userIDs) == 0 {
		return nil
	}

	ctx := context.Background()
	key := roomKey(room)
	expiry := float64(r.expiryMillis())

	members := make([]redis.Z, 0, len(userIDs))
	for _, userID := range userIDs {
		members = append(members, redis.Z{
			Score:  expiry,
			Member: strconv.FormatInt(userID, 10),
		})
	}

	pipe := r.redisClient.TxPipeline()
	pipe.ZAdd(ctx, key, members...)
	pipe.Expire(ctx, key, 2*r.ttl)

	_, err := pipe.Exec(ctx)

	return err
}

func (r *PresenceRepository) ListOnline(room string) ([]int64, error) {
	ctx := context.Background()
	key := roomKey(room)
	now := time.Now().UnixMilli()

	// Prune and read inside one MULTI/EXEC so a concurrent heartbeat cannot
	// slip between the two commands.
	pipe := r.redisClient.TxPipeline()
	pipe.ZRemRangeByScore(ctx, key, "-inf", fmt.Sprintf("(%d", now))
	members := pipe.ZRangeByScore(ctx, key, &redis.ZRangeBy{
		Min: strconv.FormatInt(now, 10),
		Max: "+inf",
	})

	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}

	raw, err := members.Result()
	if err != nil {
		return nil, err
	}

	// Non-nil empty slice: an empty room must marshal to [] rather than null.
	userIDs := make([]int64, 0, len(raw))
	for _, member := range raw {
		userID, err := strconv.ParseInt(member, 10, 64)
		if err != nil {
			return nil, err
		}
		userIDs = append(userIDs, userID)
	}

	slices.Sort(userIDs)

	return userIDs, nil
}

func (r *PresenceRepository) expiryMillis() int64 {
	return time.Now().Add(r.ttl).UnixMilli()
}

func roomKey(room string) string {
	return fmt.Sprintf("presence:room:%s", room)
}
