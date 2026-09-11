package repository

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// One sorted set per room, plus one index of rooms:
//
//	presence:room:<room>   member = "<user_id>:<instance_id>"   score = expiry epoch millis
//	presence:rooms         member = "<room>"                    score = expiry epoch millis
//
// Redis has no per-member TTL, so expiry is logical: writes stamp an expiry
// score, and the read path prunes anything already past it. The trade-off is
// that expiry becomes our job on read instead of Redis's. The key-level EXPIRE
// (refreshed on every write) is what reclaims rooms nobody ever reads again.
//
// The member is scoped to the chat-service instance that reported it (month 3).
// With one instance, "<user_id>" alone was enough. With N, a user whose socket
// on instance A closes gets an offline from A - and if the member were just the
// user id, that ZREM would also erase the online that instance B holds for the
// same user's other socket. Per-instance members make A's offline touch only
// A's entry; the read path collapses the instances back into one user id.
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

func (r *PresenceRepository) SetOnline(room string, userID int64, instanceID string) error {
	ctx := context.Background()
	key := roomKey(room)
	expiry := float64(r.expiryMillis())

	pipe := r.redisClient.TxPipeline()
	pipe.ZAdd(ctx, key, redis.Z{Score: expiry, Member: member(userID, instanceID)})
	pipe.Expire(ctx, key, 2*r.ttl)
	r.touchRoomIndex(ctx, pipe, room, expiry)

	_, err := pipe.Exec(ctx)

	return err
}

func (r *PresenceRepository) SetOffline(room string, userID int64, instanceID string) error {
	ctx := context.Background()
	key := roomKey(room)

	pipe := r.redisClient.TxPipeline()
	pipe.ZRem(ctx, key, member(userID, instanceID))
	// A no-op if ZRem emptied (and therefore deleted) the key.
	pipe.Expire(ctx, key, 2*r.ttl)
	remaining := pipe.ZCard(ctx, key)

	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}

	// Last one out drops the room from the index, so /rooms does not list an
	// empty room for a TTL after everyone left. Racing a concurrent online is
	// harmless: that online re-adds the room in its own transaction, and the
	// next heartbeat would anyway.
	if remaining.Val() == 0 {
		return r.redisClient.ZRem(ctx, roomsIndexKey, room).Err()
	}

	return nil
}

// Refresh re-stamps every user this instance currently has connected, in one
// round trip. It is idempotent, and each instance only ever writes its own
// members, so N instances heartbeating the same room never fight.
func (r *PresenceRepository) Refresh(room string, instanceID string, userIDs []int64) error {
	if len(userIDs) == 0 {
		return nil
	}

	ctx := context.Background()
	key := roomKey(room)
	expiry := float64(r.expiryMillis())

	members := make([]redis.Z, 0, len(userIDs))
	for _, userID := range userIDs {
		members = append(members, redis.Z{Score: expiry, Member: member(userID, instanceID)})
	}

	pipe := r.redisClient.TxPipeline()
	pipe.ZAdd(ctx, key, members...)
	pipe.Expire(ctx, key, 2*r.ttl)
	r.touchRoomIndex(ctx, pipe, room, expiry)

	_, err := pipe.Exec(ctx)

	return err
}

func (r *PresenceRepository) ListOnline(room string) ([]int64, error) {
	ctx := context.Background()
	now := time.Now().UnixMilli()

	pipe := r.redisClient.TxPipeline()
	members := r.pruneAndRead(ctx, pipe, roomKey(room), now)

	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}

	raw, err := members.Result()
	if err != nil {
		return nil, err
	}

	return collapseUsers(raw)
}

// ListRooms is every room with someone online, with a per-room user count.
// Two round trips: one to prune and read the index, one to prune and read
// every listed room. That second pipeline is one command pair per room, not
// one query per room - a hundred rooms is still a single network exchange.
func (r *PresenceRepository) ListRooms() ([]RoomCount, error) {
	ctx := context.Background()
	now := time.Now().UnixMilli()

	pipe := r.redisClient.TxPipeline()
	roomsCmd := r.pruneAndRead(ctx, pipe, roomsIndexKey, now)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}

	rooms, err := roomsCmd.Result()
	if err != nil {
		return nil, err
	}

	// Non-nil empty slice: no rooms must marshal to [] rather than null.
	result := make([]RoomCount, 0, len(rooms))
	if len(rooms) == 0 {
		return result, nil
	}

	pipe = r.redisClient.TxPipeline()
	perRoom := make([]*redis.StringSliceCmd, 0, len(rooms))
	for _, room := range rooms {
		perRoom = append(perRoom, r.pruneAndRead(ctx, pipe, roomKey(room), now))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}

	for i, room := range rooms {
		raw, err := perRoom[i].Result()
		if err != nil {
			return nil, err
		}

		users, err := collapseUsers(raw)
		if err != nil {
			return nil, err
		}

		// The index can lag the room by one write (an offline that raced the
		// index removal); an empty room is simply not a room anyone is in.
		if len(users) == 0 {
			continue
		}

		result = append(result, RoomCount{Name: room, Count: len(users)})
	}

	slices.SortFunc(result, func(a, b RoomCount) int { return strings.Compare(a.Name, b.Name) })

	return result, nil
}

// pruneAndRead queues the two commands every read path needs inside the
// caller's MULTI/EXEC: drop expired members, then return the live ones. Being in
// one transaction is what stops a concurrent heartbeat slipping between them.
func (r *PresenceRepository) pruneAndRead(ctx context.Context, pipe redis.Pipeliner, key string, now int64) *redis.StringSliceCmd {
	pipe.ZRemRangeByScore(ctx, key, "-inf", fmt.Sprintf("(%d", now))
	return pipe.ZRangeByScore(ctx, key, &redis.ZRangeBy{
		Min: strconv.FormatInt(now, 10),
		Max: "+inf",
	})
}

// touchRoomIndex queues the index write that every online/refresh carries so
// the room list is maintained as a by-product of presence writes, not by a
// SCAN over the keyspace on read.
func (r *PresenceRepository) touchRoomIndex(ctx context.Context, pipe redis.Pipeliner, room string, expiry float64) {
	pipe.ZAdd(ctx, roomsIndexKey, redis.Z{Score: expiry, Member: room})
	pipe.Expire(ctx, roomsIndexKey, 2*r.ttl)
}

// collapseUsers turns "<user_id>:<instance_id>" members back into one sorted,
// deduplicated user id list - a user connected through two instances is one
// person as far as the sidebar is concerned.
func collapseUsers(raw []string) ([]int64, error) {
	seen := make(map[int64]bool, len(raw))
	// Non-nil empty slice: an empty room must marshal to [] rather than null.
	userIDs := make([]int64, 0, len(raw))

	for _, m := range raw {
		idPart, _, found := strings.Cut(m, ":")
		if !found {
			return nil, fmt.Errorf("malformed presence member %q: missing instance id", m)
		}

		userID, err := strconv.ParseInt(idPart, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("malformed presence member %q: %w", m, err)
		}

		if seen[userID] {
			continue
		}
		seen[userID] = true
		userIDs = append(userIDs, userID)
	}

	slices.Sort(userIDs)

	return userIDs, nil
}

func (r *PresenceRepository) expiryMillis() int64 {
	return time.Now().Add(r.ttl).UnixMilli()
}

func member(userID int64, instanceID string) string {
	return strconv.FormatInt(userID, 10) + ":" + instanceID
}

func roomKey(room string) string {
	return fmt.Sprintf("presence:room:%s", room)
}

const roomsIndexKey = "presence:rooms"
