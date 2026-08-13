// The session store against a real Redis.
//
// Real rather than faked, for the same reason the database abstraction layer is
// tested against a real postgres: what this package does is almost entirely what
// Redis does with it -- a TTL that expires, a key that is gone after a delete, a
// value that round-trips as JSON -- and a fake would be a second implementation of
// exactly the part under test.
//
// Skipped without TEST_REDIS_URL, so `make server-test` passes over it and
// `make db-test` runs it against the redis in the test stack.
package session

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// testStore returns a store on a key prefix of its own, so tests cannot see each
// other's sessions and a leftover key from a previous run cannot be mistaken for one
// of these.
func testStore(t *testing.T, extendTTL time.Duration) *RedisStore {
	t.Helper()
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL not set (run via make db-test)")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("parse TEST_REDIS_URL: %v", err)
	}
	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("ping redis: %v", err)
	}
	prefix := "test:" + t.Name() + ":"
	t.Cleanup(func() {
		keys, err := client.Keys(context.Background(), prefix+"*").Result()
		if err == nil && len(keys) > 0 {
			_ = client.Del(context.Background(), keys...).Err()
		}
	})
	return NewRedisStore(client, prefix, extendTTL)
}

func userSession() *Data {
	return &Data{
		Kind: SessionKindUser, UserID: "user-1", Email: "u@example.com",
		GoogleSub: "sub|1", Role: "admin",
	}
}

// A created session comes back with everything it was given, and with the three
// times the store stamps on it.
func TestRedisStore_CreateAndGet(t *testing.T) {
	store := testStore(t, 0)
	ctx := context.Background()
	before := time.Now()

	id, err := store.Create(ctx, userSession(), time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id == "" {
		t.Fatal("Create returned no session id")
	}

	got, err := store.Get(ctx, id, 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nothing for a session just created")
	}
	if got.UserID != "user-1" || got.Email != "u@example.com" ||
		got.GoogleSub != "sub|1" || got.Role != "admin" || got.Kind != SessionKindUser {
		t.Errorf("session: got %+v", got)
	}
	if got.CreatedAt.Before(before) {
		t.Errorf("created at: got %v, want no earlier than %v", got.CreatedAt, before)
	}
	if !got.LastSeenAt.Equal(got.CreatedAt) {
		t.Errorf("last seen at: got %v, want the creation time %v", got.LastSeenAt, got.CreatedAt)
	}
	if want := got.CreatedAt.Add(time.Hour); !got.ExpiresAt.Equal(want) {
		t.Errorf("expires at: got %v, want %v", got.ExpiresAt, want)
	}
}

// Two sessions never share an id, which is the whole of what makes the id worth
// holding: it is the only thing a caller presents.
func TestRedisStore_CreateIssuesDistinctIDs(t *testing.T) {
	store := testStore(t, 0)
	ctx := context.Background()
	seen := map[string]bool{}
	for range 20 {
		id, err := store.Create(ctx, userSession(), time.Hour)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if seen[id] {
			t.Fatalf("Create issued %q twice", id)
		}
		seen[id] = true
	}
}

// An id nobody was given is not a session, and says so by returning nothing rather
// than by failing: a stale cookie is an ordinary thing for a request to carry.
func TestRedisStore_GetUnknownID(t *testing.T) {
	store := testStore(t, 0)
	got, err := store.Get(context.Background(), "no-such-session", 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Errorf("session: got %+v, want nothing", got)
	}
}

// The key carries the TTL the session was created with, so Redis drops it even if
// nothing ever asks for it again.
func TestRedisStore_CreateSetsTheTTL(t *testing.T) {
	store := testStore(t, 0)
	ctx := context.Background()
	id, err := store.Create(ctx, userSession(), time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	ttl, err := store.client.TTL(ctx, store.key(id)).Result()
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}
	if ttl <= 0 || ttl > time.Hour {
		t.Errorf("ttl: got %v, want a positive value no greater than an hour", ttl)
	}
}

// An expired session is gone whichever way it went: the key may have been dropped by
// Redis, or the stored ExpiresAt may have passed while the key survived. The second
// is what the check inside Get is for, and it is reachable because the sliding
// extension writes an expiry the key's TTL does not have to agree with.
func TestRedisStore_ExpiredSessionIsGone(t *testing.T) {
	store := testStore(t, 0)
	ctx := context.Background()
	id, err := store.Create(ctx, userSession(), 50*time.Millisecond)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	got, err := store.Get(ctx, id, 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Errorf("session: got %+v, want nothing once it has expired", got)
	}
}

// A user session in use is extended, which is what keeps somebody working from being
// logged out on a fixed schedule.
func TestRedisStore_GetSlidesAUserSession(t *testing.T) {
	store := testStore(t, 0)
	ctx := context.Background()
	id, err := store.Create(ctx, userSession(), time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	created, err := store.Get(ctx, id, 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	got, err := store.Get(ctx, id, 24*time.Hour)
	if err != nil {
		t.Fatalf("Get with sliding: %v", err)
	}
	if !got.ExpiresAt.After(created.ExpiresAt) {
		t.Errorf("expires at: got %v, want later than the original %v", got.ExpiresAt, created.ExpiresAt)
	}
	// And it is the stored session that moved, not just the copy returned.
	again, err := store.Get(ctx, id, 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !again.ExpiresAt.After(created.ExpiresAt) {
		t.Errorf("stored expires at: got %v, want the extension to have been written", again.ExpiresAt)
	}
	ttl, err := store.client.TTL(ctx, store.key(id)).Result()
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}
	if ttl <= time.Hour {
		t.Errorf("ttl: got %v, want the key's own expiry extended past the original hour", ttl)
	}
}

// A window shorter than what the session already has does not shorten it. Sliding is
// there to extend a session in use, and a short window arriving late must not cut one
// that was created with a longer life.
func TestRedisStore_GetDoesNotShortenASession(t *testing.T) {
	store := testStore(t, 0)
	ctx := context.Background()
	id, err := store.Create(ctx, userSession(), 24*time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	created, err := store.Get(ctx, id, 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	got, err := store.Get(ctx, id, time.Minute)
	if err != nil {
		t.Fatalf("Get with a short window: %v", err)
	}
	if !got.ExpiresAt.Equal(created.ExpiresAt) {
		t.Errorf("expires at: got %v, want it left at %v", got.ExpiresAt, created.ExpiresAt)
	}
}

// A machine session is not extended by use. A service account holds a token for as
// long as it was issued for, and sliding it would make the lifetime unbounded.
func TestRedisStore_GetDoesNotSlideAMachineSession(t *testing.T) {
	store := testStore(t, 0)
	ctx := context.Background()
	id, err := store.Create(ctx, &Data{Kind: SessionKindServiceAccount, ServiceAccountID: "svc-1"}, time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	created, err := store.Get(ctx, id, 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	got, err := store.Get(ctx, id, 24*time.Hour)
	if err != nil {
		t.Fatalf("Get with sliding: %v", err)
	}
	if !got.ExpiresAt.Equal(created.ExpiresAt) {
		t.Errorf("expires at: got %v, want a machine session left at %v", got.ExpiresAt, created.ExpiresAt)
	}
	if got.ServiceAccountID != "svc-1" {
		t.Errorf("service account: got %q, want svc-1", got.ServiceAccountID)
	}
}

// Delete ends the session, which is what logging out relies on.
func TestRedisStore_Delete(t *testing.T) {
	store := testStore(t, 0)
	ctx := context.Background()
	id, err := store.Create(ctx, userSession(), time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, err := store.Get(ctx, id, 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Errorf("session: got %+v, want nothing after a delete", got)
	}
}

// Deleting a session that is not there is not an error. A logout arriving twice, or
// after the session has already expired, is ordinary.
func TestRedisStore_DeleteUnknownID(t *testing.T) {
	store := testStore(t, 0)
	if err := store.Delete(context.Background(), "no-such-session"); err != nil {
		t.Errorf("Delete of an unknown session: %v", err)
	}
}

// The key prefix keeps sessions apart from everything else in the same Redis, and
// there is a default because an instance that names none still must not collide with
// the telemetry counters beside it.
func TestNewRedisStore_KeyPrefix(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL not set (run via make db-test)")
	}
	tests := []struct {
		name  string
		given string
		want  string
	}{
		{"a prefix of its own", "myapp:sess:", "myapp:sess:abc"},
		{"none given takes the default", "", "portfoliodb:session:abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewRedisStore(nil, tt.given, time.Hour)
			if got := store.key("abc"); got != tt.want {
				t.Errorf("key: got %q, want %q", got, tt.want)
			}
			if store.extendTTL != time.Hour {
				t.Errorf("extend ttl: got %v, want an hour", store.extendTTL)
			}
		})
	}
}
