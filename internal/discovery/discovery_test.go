package discovery

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/sw4p/termchat/internal/protocol"
)

const (
	idA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	idB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	idC = "cccccccccccccccccccccccccccccccc"
)

func ent(instance string, port int, text []string, ips ...string) entry {
	e := entry{Instance: instance, Port: port, Text: text}
	for _, s := range ips {
		e.IPs = append(e.IPs, net.ParseIP(s))
	}
	return e
}

func TestParseEntry(t *testing.T) {
	good := txtRecord(idA, Available)
	tests := []struct {
		name string
		e    entry
		want *Peer // nil means rejected
	}{
		{"valid", ent(idA, 5000, good, "192.168.1.5"),
			&Peer{ID: idA, State: Available, Addrs: []string{"192.168.1.5:5000"}}},
		{"busy", ent(idA, 5000, txtRecord(idA, Busy), "10.0.0.2"),
			&Peer{ID: idA, State: Busy, Addrs: []string{"10.0.0.2:5000"}}},
		{"order kept, duplicates and ipv6 dropped", ent(idA, 5000, good, "192.168.1.5", "fe80::1", "10.0.0.2", "192.168.1.5"),
			&Peer{ID: idA, State: Available, Addrs: []string{"192.168.1.5:5000", "10.0.0.2:5000"}}},
		{"loopback allowed (same machine)", ent(idA, 5000, good, "127.0.0.1"),
			&Peer{ID: idA, State: Available, Addrs: []string{"127.0.0.1:5000"}}},
		{"extra txt fields ignored", ent(idA, 5000, append(good, "future=yes"), "192.168.1.5"),
			&Peer{ID: idA, State: Available, Addrs: []string{"192.168.1.5:5000"}}},
		{"instance isn't the id", ent("sams-macbook", 5000, good, "192.168.1.5"), nil},
		{"id mismatch", ent(idB, 5000, good, "192.168.1.5"), nil},
		{"bad id", ent("xyz", 5000, []string{"id=xyz", "v=1", "state=available"}, "192.168.1.5"), nil},
		{"other major version", ent(idA, 5000, []string{"id=" + idA, "v=2", "state=available"}, "192.168.1.5"), nil},
		{"missing version", ent(idA, 5000, []string{"id=" + idA, "state=available"}, "192.168.1.5"), nil},
		{"unknown state", ent(idA, 5000, []string{"id=" + idA, "v=1", "state=chatting"}, "192.168.1.5"), nil},
		{"no port", ent(idA, 0, good, "192.168.1.5"), nil},
		{"no addresses", ent(idA, 5000, good), nil},
		{"only ipv6", ent(idA, 5000, good, "2001:db8::5"), nil},
		{"unspecified address", ent(idA, 5000, good, "0.0.0.0"), nil},
		{"multicast address", ent(idA, 5000, good, "224.0.0.251"), nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseEntry(tc.e)
			if tc.want == nil {
				if ok {
					t.Errorf("accepted %+v", got)
				}
				return
			}
			if !ok {
				t.Fatalf("rejected a valid entry")
			}
			if !samePeer(got, *tc.want) {
				t.Errorf("got %+v, want %+v", got, *tc.want)
			}
		})
	}
}

func testAdvertiser(id string, port int, state State, ips ...string) *Advertiser {
	a := &Advertiser{id: id, port: port, state: state}
	for _, s := range ips {
		a.ips = append(a.ips, net.ParseIP(s).To4())
	}
	return a
}

func query() *dns.Msg {
	q := &dns.Msg{}
	q.SetQuestion(serviceName, dns.TypePTR)
	return q
}

// roundTrip packs an advertiser's response, unpacks it as a browser would,
// and returns the peers the browser finds.
func roundTrip(t *testing.T, src string, advs ...*Advertiser) []Peer {
	t.Helper()
	c := newCollector()
	for _, a := range advs {
		b, err := a.response(query(), true).Pack()
		if err != nil {
			t.Fatal(err)
		}
		var m dns.Msg
		if err := m.Unpack(b); err != nil {
			t.Fatal(err)
		}
		c.add(&m, net.ParseIP(src))
	}
	ps := c.peers()
	slices.SortFunc(ps, func(a, b Peer) int { return cmp.Compare(a.ID, b.ID) })
	return ps
}

func TestResponseRoundTrip(t *testing.T) {
	got := roundTrip(t, "192.168.1.5",
		testAdvertiser(idA, 40001, Available, "192.168.1.5", "10.8.0.3"),
		testAdvertiser(idB, 40002, Busy, "192.168.1.5"),
	)
	want := []Peer{
		{ID: idA, State: Available, Addrs: []string{"192.168.1.5:40001", "10.8.0.3:40001"}},
		{ID: idB, State: Busy, Addrs: []string{"192.168.1.5:40002"}},
	}
	if len(got) != len(want) || !samePeer(got[0], want[0]) || !samePeer(got[1], want[1]) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestResponseSourceAddressFirst(t *testing.T) {
	// The advertiser lists its LAN address, but the answer arrived over
	// loopback (same machine). The source address goes first.
	got := roundTrip(t, "127.0.0.1", testAdvertiser(idA, 40001, Available, "192.168.1.5"))
	if len(got) != 1 || !slices.Equal(got[0].Addrs, []string{"127.0.0.1:40001", "192.168.1.5:40001"}) {
		t.Errorf("got %+v", got)
	}
}

func TestSetStateChangesResponse(t *testing.T) {
	a := testAdvertiser(idA, 40001, Available, "192.168.1.5")
	a.SetState(Busy)
	if got := roundTrip(t, "192.168.1.5", a); len(got) != 1 || got[0].State != Busy {
		t.Errorf("after SetState(Busy): %+v", got)
	}
}

func TestResponseShape(t *testing.T) {
	a := testAdvertiser(idA, 40001, Available, "192.168.1.5")
	q := query()
	q.Id = 4242

	legacy := a.response(q, true)
	if legacy.Id != 4242 || len(legacy.Question) != 1 {
		t.Errorf("legacy unicast reply must echo id and question: id=%d question=%v", legacy.Id, legacy.Question)
	}
	for _, rr := range append(legacy.Answer, legacy.Extra...) {
		if rr.Header().Ttl != ttlUnicast {
			t.Errorf("%v: TTL %d, want %d", rr, rr.Header().Ttl, ttlUnicast)
		}
	}

	multi := a.response(q, false)
	if multi.Id != 0 || len(multi.Question) != 0 {
		t.Errorf("multicast reply must not echo id or question: id=%d question=%v", multi.Id, multi.Question)
	}
	for _, rr := range append(multi.Answer, multi.Extra...) {
		if strings.Contains(strings.ToLower(rr.String()), "macbook") {
			t.Errorf("hostname leaked: %v", rr)
		}
	}
	if !multi.Response || !multi.Authoritative {
		t.Error("reply must be an authoritative response")
	}
}

func TestAsksForUs(t *testing.T) {
	for _, tc := range []struct {
		name  string
		qtype uint16
		want  bool
	}{
		{serviceName, dns.TypePTR, true},
		{"_TermChat._TCP.local.", dns.TypePTR, true},
		{serviceName, dns.TypeANY, true},
		{serviceName, dns.TypeA, false},
		{"_http._tcp.local.", dns.TypePTR, false},
	} {
		q := &dns.Msg{}
		q.SetQuestion(tc.name, tc.qtype)
		if got := asksForUs(q); got != tc.want {
			t.Errorf("asksForUs(%s %s) = %v, want %v", tc.name, dns.TypeToString[tc.qtype], got, tc.want)
		}
	}
}

func TestCollectorIgnoresForeignAndHostileRecords(t *testing.T) {
	m := &dns.Msg{}
	m.Response = true
	hdr := func(name string, t uint16) dns.RR_Header {
		return dns.RR_Header{Name: name, Rrtype: t, Class: dns.ClassINET, Ttl: 120}
	}
	m.Answer = []dns.RR{
		&dns.PTR{Hdr: hdr("_http._tcp.local.", dns.TypePTR), Ptr: idA + "._http._tcp.local."},
		&dns.PTR{Hdr: hdr(serviceName, dns.TypePTR), Ptr: "a.b." + serviceName},
	}
	for i := 0; i < maxPeersPerRound+50; i++ {
		id := fmt.Sprintf("%032x", i)
		m.Answer = append(m.Answer, &dns.PTR{Hdr: hdr(serviceName, dns.TypePTR), Ptr: id + "." + serviceName})
	}
	c := newCollector()
	c.add(m, net.ParseIP("192.168.1.5"))
	if len(c.instances) != maxPeersPerRound {
		t.Errorf("collected %d instances, want the cap of %d", len(c.instances), maxPeersPerRound)
	}
	for name := range c.instances {
		if !strings.HasSuffix(name, "."+serviceName) || strings.Count(name, ".") != strings.Count(serviceName, ".")+1 {
			t.Errorf("collected foreign instance %q", name)
		}
	}
}

func TestLocalAddrs(t *testing.T) {
	ips := localAddrs()
	if len(ips) == 0 {
		t.Fatal("no addresses, not even the loopback fallback")
	}
	for _, ip := range ips {
		if ip.To4() == nil || ip.IsLinkLocalUnicast() || (ip.IsLoopback() && len(ips) > 1) {
			t.Errorf("unusable address %s in %v", ip, ips)
		}
	}
	t.Logf("would advertise %v", ips)
}

func TestTXTRecordHasNoPersonalData(t *testing.T) {
	for _, kv := range txtRecord(idA, Available) {
		if kv != "id="+idA && kv != "v=1" && kv != "state=available" {
			t.Errorf("unexpected TXT field %q", kv)
		}
	}
}

// fakeRounds returns a round function that plays back rounds in order, then
// repeats the last one.
func fakeRounds(rounds ...[]Peer) func(context.Context) ([]Peer, error) {
	var mu sync.Mutex
	i := 0
	return func(context.Context) ([]Peer, error) {
		mu.Lock()
		defer mu.Unlock()
		r := rounds[min(i, len(rounds)-1)]
		i++
		return r, nil
	}
}

func peer(id string, st State) Peer {
	return Peer{ID: id, State: st, Addrs: []string{"192.168.1.9:4000"}}
}

func ids(ps []Peer) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.ID+"/"+string(p.State))
	}
	return out
}

func notified(b *Browser) bool {
	select {
	case <-b.Updates():
		return true
	default:
		return false
	}
}

func TestBrowserMerge(t *testing.T) {
	b := newBrowser(idA, nil, 0)

	b.merge([]Peer{peer(idA, Available), peer(idC, Available), peer(idB, Busy)})
	if got, want := ids(b.Peers()), []string{idB + "/busy", idC + "/available"}; !slices.Equal(got, want) {
		t.Errorf("after round 1: %v, want %v (sorted, self excluded)", got, want)
	}
	if !notified(b) {
		t.Error("no update after peers appeared")
	}

	b.merge([]Peer{peer(idC, Available), peer(idB, Busy)})
	if notified(b) {
		t.Error("update sent although nothing changed")
	}

	b.merge([]Peer{peer(idC, Available), peer(idB, Available)})
	if !notified(b) {
		t.Error("no update after a state change")
	}

	b.merge([]Peer{peer(idC, Available)}) // B missed one round
	if got := ids(b.Peers()); len(got) != 2 {
		t.Errorf("peer dropped after a single missed round: %v", got)
	}
	if notified(b) {
		t.Error("update sent for a single missed round")
	}

	b.merge([]Peer{peer(idC, Available), peer(idB, Available)}) // B is back: the count resets
	b.merge([]Peer{peer(idC, Available)})
	if got := ids(b.Peers()); len(got) != 2 {
		t.Errorf("missed count didn't reset when the peer came back: %v", got)
	}

	b.merge([]Peer{peer(idC, Available)}) // second miss in a row
	if got, want := ids(b.Peers()), []string{idC + "/available"}; !slices.Equal(got, want) {
		t.Errorf("after two missed rounds: %v, want %v", got, want)
	}
	if !notified(b) {
		t.Error("no update after a peer was dropped")
	}
}

func TestBrowserPeersIsACopy(t *testing.T) {
	b := newBrowser(idA, nil, 0)
	b.merge([]Peer{peer(idB, Available)})
	ps := b.Peers()
	ps[0].State = Busy
	ps[0].Addrs[0] = "tampered"
	if p := b.Peers()[0]; p.State != Available || p.Addrs[0] == "tampered" {
		t.Errorf("caller's changes leaked into the browser: %+v", p)
	}
}

func TestBrowserRun(t *testing.T) {
	b := newBrowser(idA, fakeRounds(
		[]Peer{peer(idB, Available)},
		[]Peer{peer(idB, Busy), peer(idC, Available)},
	), 5*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- b.Run(ctx) }()

	deadline := time.After(2 * time.Second)
	want := []string{idB + "/busy", idC + "/available"}
	for !slices.Equal(ids(b.Peers()), want) {
		select {
		case <-b.Updates():
		case <-deadline:
			t.Fatalf("peers = %v, want %v", ids(b.Peers()), want)
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Run after cancel = %v, want nil", err)
	}
}

func TestBrowserRunFirstRoundError(t *testing.T) {
	boom := errors.New("no multicast interface")
	b := newBrowser(idA, func(context.Context) ([]Peer, error) { return nil, boom }, time.Millisecond)
	if err := b.Run(context.Background()); !errors.Is(err, boom) {
		t.Errorf("Run = %v, want %v", err, boom)
	}
}

func TestBrowserLaterErrorsAgePeersOut(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	round := func(context.Context) ([]Peer, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return []Peer{peer(idB, Available)}, nil
		}
		return nil, errors.New("network went away")
	}
	b := newBrowser(idA, round, time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-b.Updates():
			if len(b.Peers()) == 0 {
				return
			}
		case <-deadline:
			t.Fatalf("peer never aged out: %v", ids(b.Peers()))
		}
	}
}

// TestRealMDNS advertises two instances and finds them with a real browse
// over the machine's network interfaces. It needs multicast, so it only runs
// when TERMCHAT_MDNS_TEST=1.
func TestRealMDNS(t *testing.T) {
	if os.Getenv("TERMCHAT_MDNS_TEST") != "1" {
		t.Skip("set TERMCHAT_MDNS_TEST=1 to run against the real network")
	}
	self, other := protocol.NewID(), protocol.NewID()
	advSelf, err := Advertise(self, 40001, Available)
	if err != nil {
		t.Fatal(err)
	}
	defer advSelf.Close()
	advOther, err := Advertise(other, 40002, Available)
	if err != nil {
		t.Fatal(err)
	}
	defer advOther.Close()

	b := NewBrowser(self)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	waitFor := func(what string, cond func([]Peer) bool) {
		t.Helper()
		deadline := time.After(15 * time.Second)
		for !cond(b.Peers()) {
			select {
			case <-b.Updates():
			case <-deadline:
				t.Fatalf("timed out waiting for %s; peers: %+v", what, b.Peers())
			}
		}
	}
	find := func(ps []Peer) *Peer {
		for i := range ps {
			if ps[i].ID == other {
				return &ps[i]
			}
			if ps[i].ID == self {
				t.Fatalf("browser listed itself")
			}
		}
		return nil
	}

	waitFor("the other peer to appear", func(ps []Peer) bool {
		p := find(ps)
		return p != nil && p.State == Available
	})
	t.Logf("found: %+v", *find(b.Peers()))

	advOther.SetState(Busy)
	waitFor("the other peer to turn busy", func(ps []Peer) bool {
		p := find(ps)
		return p != nil && p.State == Busy
	})

	advOther.Close()
	waitFor("the other peer to disappear", func(ps []Peer) bool { return find(ps) == nil })
}
