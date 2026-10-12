package discovery

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/net/ipv4"
)

const (
	mdnsPort = 5353
	// serviceName is the DNS name browsers ask about.
	serviceName = Service + ".local."

	ttlMulticast = 120 // seconds, for answers to regular mDNS queries
	ttlUnicast   = 10  // RFC 6762 §6.7: at most 10s in "legacy unicast" replies

	maxPeersPerRound = 256 // don't let a noisy network grow our map without bound
)

var mdnsGroup = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: mdnsPort}

// multicastIfaces lists interfaces we can send and receive mDNS on. Loopback
// is included, so instances on one offline machine can still see each other.
func multicastIfaces() []net.Interface {
	var out []net.Interface
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		const want = net.FlagUp | net.FlagMulticast
		if iface.Flags&want == want && iface.Flags&net.FlagPointToPoint == 0 {
			out = append(out, iface)
		}
	}
	return out
}

// ─── Advertiser: answers questions about us ─────────────────────────────────

// Advertiser announces this instance on the network by answering mDNS
// queries for _termchat._tcp.
type Advertiser struct {
	conn *ipv4.PacketConn

	mu    sync.Mutex
	id    string
	port  int
	state State
	ips   []net.IP
}

// Advertise starts answering for this instance: id is our session id, port is
// our TCP listener's port. Call Close to stop.
func Advertise(id string, port int, state State) (*Advertiser, error) {
	lc := net.ListenConfig{Control: reusePort}
	pc, err := lc.ListenPacket(context.Background(), "udp4", ":5353")
	if err != nil {
		return nil, err
	}
	conn := ipv4.NewPacketConn(pc)
	conn.SetControlMessage(ipv4.FlagInterface, true)
	joined := 0
	for _, iface := range multicastIfaces() {
		if conn.JoinGroup(&iface, mdnsGroup) == nil {
			joined++
		}
	}
	if joined == 0 {
		conn.Close()
		return nil, errors.New("discovery: no network interface supports multicast")
	}

	a := &Advertiser{conn: conn, id: id, port: port, state: state, ips: localAddrs()}
	go a.serve()
	return a, nil
}

// SetState changes the advertised state. Browsers see it on their next round.
func (a *Advertiser) SetState(state State) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = state
}

// Close stops advertising. It's safe to call more than once.
func (a *Advertiser) Close() {
	a.conn.Close()
}

func (a *Advertiser) serve() {
	buf := make([]byte, 9000)
	for {
		n, cm, src, err := a.conn.ReadFrom(buf)
		if err != nil {
			return // closed
		}
		var q dns.Msg
		if q.Unpack(buf[:n]) != nil || q.Response || q.Opcode != dns.OpcodeQuery || !asksForUs(&q) {
			continue
		}
		from, ok := src.(*net.UDPAddr)
		if !ok {
			continue
		}
		if from.Port != mdnsPort {
			// A one-shot query from an ordinary port, like our Browser's:
			// answer directly to the sender (RFC 6762 §6.7).
			if b, err := a.response(&q, true).Pack(); err == nil {
				a.conn.WriteTo(b, nil, from)
			}
			continue
		}
		// A regular mDNS query (e.g. `dns-sd -B _termchat._tcp`): answer to
		// the group, on the interface it came in on.
		if b, err := a.response(&q, false).Pack(); err == nil {
			var wcm *ipv4.ControlMessage
			if cm != nil {
				wcm = &ipv4.ControlMessage{IfIndex: cm.IfIndex}
			}
			a.conn.WriteTo(b, wcm, mdnsGroup)
		}
	}
}

func asksForUs(q *dns.Msg) bool {
	for _, question := range q.Question {
		if strings.EqualFold(question.Name, serviceName) &&
			(question.Qtype == dns.TypePTR || question.Qtype == dns.TypeANY) {
			return true
		}
	}
	return false
}

// response builds our answer: PTR (service → instance) in the answer
// section, and SRV, TXT and A records in the additional section, so a single
// packet has everything needed to dial us.
func (a *Advertiser) response(q *dns.Msg, legacyUnicast bool) *dns.Msg {
	a.mu.Lock()
	id, port, state, ips := a.id, a.port, a.state, a.ips
	a.mu.Unlock()

	ttl := uint32(ttlMulticast)
	resp := &dns.Msg{}
	resp.Response = true
	resp.Authoritative = true
	if legacyUnicast {
		// A legacy unicast reply must echo the id and question, like
		// ordinary DNS.
		ttl = ttlUnicast
		resp.Id = q.Id
		resp.Question = q.Question
	}

	instance := id + "." + serviceName
	host := "termchat-" + id[:12] + ".local."
	hdr := func(name string, t uint16) dns.RR_Header {
		return dns.RR_Header{Name: name, Rrtype: t, Class: dns.ClassINET, Ttl: ttl}
	}
	resp.Answer = []dns.RR{&dns.PTR{Hdr: hdr(serviceName, dns.TypePTR), Ptr: instance}}
	resp.Extra = []dns.RR{
		&dns.SRV{Hdr: hdr(instance, dns.TypeSRV), Port: uint16(port), Target: host},
		&dns.TXT{Hdr: hdr(instance, dns.TypeTXT), Txt: txtRecord(id, state)},
	}
	for _, ip := range ips {
		resp.Extra = append(resp.Extra, &dns.A{Hdr: hdr(host, dns.TypeA), A: ip})
	}
	return resp
}

// ─── Browsing: one query, collect the answers ───────────────────────────────

// mdnsRound sends one query for _termchat._tcp from an ordinary UDP port, so
// responders reply straight to us, and collects answers for roundTimeout.
func mdnsRound(ctx context.Context) ([]Peer, error) {
	pc, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return nil, err
	}
	defer pc.Close()
	conn := ipv4.NewPacketConn(pc)
	conn.SetMulticastTTL(255) // RFC 6762 §11
	conn.SetMulticastLoopback(true)

	q := &dns.Msg{}
	q.SetQuestion(serviceName, dns.TypePTR)
	q.RecursionDesired = false
	b, err := q.Pack()
	if err != nil {
		return nil, err
	}
	sent := 0
	for _, iface := range multicastIfaces() {
		if conn.SetMulticastInterface(&iface) != nil {
			continue
		}
		if _, err := conn.WriteTo(b, nil, mdnsGroup); err == nil {
			sent++
		}
	}
	if sent == 0 {
		return nil, errors.New("discovery: couldn't send an mDNS query on any interface")
	}

	stop := context.AfterFunc(ctx, func() { pc.Close() })
	defer stop()
	pc.SetReadDeadline(time.Now().Add(roundTimeout))

	c := newCollector()
	buf := make([]byte, 9000)
	for {
		n, src, err := pc.ReadFrom(buf)
		if err != nil {
			break // the deadline, or ctx was cancelled
		}
		var m dns.Msg
		if m.Unpack(buf[:n]) != nil || !m.Response {
			continue
		}
		if from, ok := src.(*net.UDPAddr); ok {
			c.add(&m, from.IP)
		}
	}
	return c.peers(), nil
}

// collector assembles service instances from the records in any number of
// responses. Names are compared case-insensitively, as DNS requires.
type collector struct {
	instances map[string]*collected // key: lowercase instance FQDN
	hosts     map[string][]net.IP   // key: lowercase host FQDN
}

type collected struct {
	label  string
	port   int
	target string
	text   []string
	src    net.IP
}

func newCollector() *collector {
	return &collector{instances: map[string]*collected{}, hosts: map[string][]net.IP{}}
}

// instance returns the entry for an instance name under our service, creating
// it if there's room. It returns nil for names that aren't ours.
func (c *collector) instance(name string, src net.IP) *collected {
	name = strings.ToLower(name)
	label, ok := strings.CutSuffix(name, "."+serviceName)
	if !ok || strings.Contains(label, ".") {
		return nil
	}
	if e, ok := c.instances[name]; ok {
		return e
	}
	if len(c.instances) >= maxPeersPerRound {
		return nil
	}
	e := &collected{label: label, src: src}
	c.instances[name] = e
	return e
}

func (c *collector) add(m *dns.Msg, src net.IP) {
	rrs := append(append(append([]dns.RR{}, m.Answer...), m.Ns...), m.Extra...)
	for _, rr := range rrs {
		switch rr := rr.(type) {
		case *dns.PTR:
			if strings.EqualFold(rr.Hdr.Name, serviceName) {
				c.instance(rr.Ptr, src)
			}
		case *dns.SRV:
			if e := c.instance(rr.Hdr.Name, src); e != nil {
				e.port, e.target = int(rr.Port), strings.ToLower(rr.Target)
			}
		case *dns.TXT:
			if e := c.instance(rr.Hdr.Name, src); e != nil {
				e.text = rr.Txt
			}
		case *dns.A:
			host := strings.ToLower(rr.Hdr.Name)
			if len(c.hosts[host]) < 16 {
				c.hosts[host] = append(c.hosts[host], rr.A)
			}
		}
	}
}

func (c *collector) peers() []Peer {
	var out []Peer
	for _, e := range c.instances {
		// The address the answer came from is known to work, so try it first.
		ips := append([]net.IP{e.src}, c.hosts[e.target]...)
		if p, ok := parseEntry(entry{Instance: e.label, Port: e.port, Text: e.text, IPs: ips}); ok {
			out = append(out, p)
		}
	}
	return out
}
