package loadbalancer

import (
	"fmt"
	"hash/fnv"
	"net/http"
	"sort"
)

// virtualNodes is how many points each instance owns on the ring. With one
// point per instance, two instances could split the ring 90/10; spreading each
// instance across many points evens that out to within a few percent.
const virtualNodes = 150

// ErrBackendDown is a sticky miss: the instance this key hashes to is
// unhealthy and no other instance is allowed to take its place.
type ErrBackendDown struct {
	Key     string
	Backend *Backend
}

func (e *ErrBackendDown) Error() string {
	return fmt.Sprintf("instance %s for key %q is down", e.Backend, e.Key)
}

type ringPoint struct {
	hash    uint64
	backend *Backend
}

// ConsistentHash maps a request key (the room name) to one instance. The same
// key always resolves to the same instance, which is the sticky-session
// property /ws needs, and it needs no shared state between the instances to
// get it - the gateway alone decides.
//
// The ring is built over ALL configured instances, not the healthy ones, and
// is never rebuilt. That is deliberate. If the ring shrank when an instance
// went down, its rooms would move to a neighbour; when it came back they would
// move again, and clients that connected in between would be split across two
// instances of the same room - the exact bug the strategy exists to prevent.
// So a room whose instance is down fails closed with ErrBackendDown until the
// instance returns. The "real" fix - a Redis pub/sub backplane so any instance
// can serve any room - is a different lesson and is not built here.
type ConsistentHash struct {
	ring []ringPoint
	key  func(r *http.Request) string
}

// NewConsistentHash builds the ring from the pool's full membership. keyFunc
// extracts the sticky key from a request.
func NewConsistentHash(pool *Pool, keyFunc func(r *http.Request) string) *ConsistentHash {
	backends := pool.Backends()
	ring := make([]ringPoint, 0, len(backends)*virtualNodes)

	for _, b := range backends {
		for i := range virtualNodes {
			ring = append(ring, ringPoint{
				hash:    hashKey(fmt.Sprintf("%s#%d", b.URL.Host, i)),
				backend: b,
			})
		}
	}

	sort.Slice(ring, func(i, j int) bool { return ring[i].hash < ring[j].hash })

	return &ConsistentHash{ring: ring, key: keyFunc}
}

func (s *ConsistentHash) Name() string { return "consistent-hash" }

func (s *ConsistentHash) Pick(r *http.Request) (*Backend, error) {
	key := s.key(r)
	b := s.lookup(key)

	if !b.Healthy() {
		return nil, &ErrBackendDown{Key: key, Backend: b}
	}

	return b, nil
}

// lookup walks clockwise from the key's hash to the first ring point.
func (s *ConsistentHash) lookup(key string) *Backend {
	h := hashKey(key)

	index := sort.Search(len(s.ring), func(i int) bool { return s.ring[i].hash >= h })
	if index == len(s.ring) {
		index = 0 // wrapped past the last point
	}

	return s.ring[index].backend
}

// FNV-1a with a finalizer mix. Not cryptographic, and does not need to be: the
// key is a room name chosen by the client, and the only thing at stake is which
// instance it lands on - an attacker who "chooses" an instance gains nothing
// they could not get by picking a room that already lives there.
//
// The mix step is not optional. Virtual node labels differ only in a short
// numeric suffix ("a:1#0", "a:1#1", ...), and raw FNV-1a of near-identical
// inputs produces near-identical high bits, so the points cluster instead of
// spreading around the ring - measured as a 70/20/10 split across three
// instances before this was added. The MurmurHash3 finalizer scrambles every
// bit of the output on any change to the input, which is what a ring needs.
func hashKey(key string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return mix64(h.Sum64())
}

func mix64(x uint64) uint64 {
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	x *= 0xc4ceb9fe1a85ec53
	x ^= x >> 33
	return x
}

// RoomKey is the key function for /ws: the room query param, defaulting to
// "general" exactly as chat-service does, so an omitted room and an explicit
// room=general hash to the same instance.
func RoomKey(r *http.Request) string {
	room := r.URL.Query().Get("room")
	if room == "" {
		return "general"
	}
	return room
}
