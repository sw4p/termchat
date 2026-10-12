package discovery

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	// roundTimeout is how long one browse round listens for answers.
	// Responders answer within about 120ms, so this leaves plenty of room.
	roundTimeout = 1500 * time.Millisecond
	// roundInterval is the pause between browse rounds.
	roundInterval = 3 * time.Second
	// maxMissed is how many rounds in a row a peer can be absent from before
	// we drop it. One lost UDP packet shouldn't make a peer vanish.
	maxMissed = 2
)

// Browser keeps a live list of the termchat peers on the network.
//
// It works in rounds: every few seconds it sends one mDNS query and treats
// the answers as a snapshot. New peers are added, changed ones updated, and
// peers missing from maxMissed rounds in a row are dropped. Polling keeps it
// simple: no cache of announcements, no need to catch "goodbye" packets, and
// a state change (available → busy) shows up within one round.
type Browser struct {
	self     string
	round    func(ctx context.Context) ([]Peer, error)
	interval time.Duration

	mu      sync.Mutex
	peers   map[string]*tracked
	updates chan struct{}
}

type tracked struct {
	peer   Peer
	missed int
}

// NewBrowser returns a Browser that ignores the peer with id self (us).
// Call Run to start it.
func NewBrowser(self string) *Browser {
	return newBrowser(self, mdnsRound, roundInterval)
}

func newBrowser(self string, round func(context.Context) ([]Peer, error), interval time.Duration) *Browser {
	return &Browser{
		self:     self,
		round:    round,
		interval: interval,
		peers:    map[string]*tracked{},
		updates:  make(chan struct{}, 1),
	}
}

// Run browses until ctx is cancelled. It returns an error only if the very
// first round fails (for example, no network interface supports multicast).
// Later failures count as empty rounds, so peers age out.
func (b *Browser) Run(ctx context.Context) error {
	for first := true; ; first = false {
		found, err := b.round(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil && first {
			return err
		}
		b.merge(found)

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(b.interval):
		}
	}
}

// Peers returns every known peer except ourselves, in any state, sorted by
// id. The slice is a copy the caller may keep.
func (b *Browser) Peers() []Peer {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Peer, 0, len(b.peers))
	for _, t := range b.peers {
		p := t.peer
		p.Addrs = slices.Clone(p.Addrs)
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b Peer) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// Updates receives a value whenever the peer list changes. Several changes
// in a row may be merged into one notification, so call Peers to see what's
// there now.
func (b *Browser) Updates() <-chan struct{} { return b.updates }

// merge folds one round's results into the peer list.
func (b *Browser) merge(found []Peer) {
	b.mu.Lock()
	defer b.mu.Unlock()

	changed := false
	seen := map[string]bool{}
	for _, p := range found {
		if p.ID == b.self {
			continue
		}
		seen[p.ID] = true
		if old, ok := b.peers[p.ID]; !ok || !samePeer(old.peer, p) {
			changed = true
		}
		b.peers[p.ID] = &tracked{peer: p}
	}
	for id, t := range b.peers {
		if seen[id] {
			continue
		}
		t.missed++
		if t.missed >= maxMissed {
			delete(b.peers, id)
			changed = true
		}
	}
	if changed {
		select {
		case b.updates <- struct{}{}:
		default: // a notification is already pending
		}
	}
}

func samePeer(a, b Peer) bool {
	return a.ID == b.ID && a.State == b.State && slices.Equal(a.Addrs, b.Addrs)
}
