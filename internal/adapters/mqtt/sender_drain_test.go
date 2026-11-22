package mqtt

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/eclipse/paho.mqtt.golang/packets"

	"github.com/IQVO/amr-fleet-control/internal/application/vda"
)

// fakeStore is a pahomqtt.Store whose keys the test controls.
type fakeStore struct {
	mu   sync.Mutex
	keys map[string]bool
}

func (s *fakeStore) Open()  {}
func (s *fakeStore) Close() {}
func (s *fakeStore) Reset() {}

func (s *fakeStore) Put(key string, _ packets.ControlPacket) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[key] = true
}

func (s *fakeStore) Get(string) packets.ControlPacket { return nil }

func (s *fakeStore) All() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.keys))
	for k := range s.keys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (s *fakeStore) Del(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.keys, key)
}

// carry (Task 11 review): ruling P3-R10 across a reconnect. paho marks the connection open
// before it resumes the publishes it stored ("o.<mid>" keys), so a command handed to it then could
// overtake a resumed one. The sender waits until the store holds no outbound key; an inbound key
// ("i.") never holds it.
func TestTheSenderWaitsUntilPahosOutboundStoreIsDrained(t *testing.T) {
	store := &fakeStore{keys: map[string]bool{"o.7": true, "i.3": true}}
	l := quietLink()
	l.openPoll = time.Millisecond
	l.open = openAndDrained(func() bool { return true }, store)
	w := &wire{}
	l.pub = w.pub(func(string, float64) time.Duration { return 0 })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.Start(ctx)
	_ = l.SendOrder(ctx, vda.Order{Robot: r1, OrderID: "leg-1"})
	time.Sleep(100 * time.Millisecond)
	w.mu.Lock()
	early := append([]wired(nil), w.msgs...)
	w.mu.Unlock()
	if len(early) != 0 {
		t.Fatalf("published %v while paho still held the unacknowledged publish o.7", early)
	}
	store.Del("o.7")
	got := w.await(t, 1)
	if len(got) != 1 || got[0] != (wired{"vda5050/v3/EMU/R1/order", 0}) {
		t.Fatalf("wire = %v, want the order with headerId 0 once o.7 was acknowledged", got)
	}
}

// carry (Task 11 review): the link keeps the store clientOptions gives paho, so the sender reads
// the very store paho resumes from.
func TestTheLinkKeepsTheClientsStore(t *testing.T) {
	l := quietLink()
	o := clientOptions("tcp://127.0.0.1:1883", "amr-fleet-control", l.onConnect)
	l.session(o)
	if l.store == nil || l.store != o.Store {
		t.Fatalf("link store = %v, client store = %v, want the same store", l.store, o.Store)
	}
}
