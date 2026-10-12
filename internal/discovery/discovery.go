// Package discovery finds other termchat peers on the LAN with mDNS (the
// protocol behind Bonjour/Avahi), with no server involved.
//
// Each peer advertises the service _termchat._tcp. The instance name is its
// random session id, and the TXT record holds id, v (protocol major version)
// and state (available or busy). The session layer reads Browser.Peers to
// pick someone to dial, and calls Advertiser.SetState as its state changes.
//
// Privacy: the advertisement never contains the machine's hostname or the
// user's nick. The host name in the SRV record is derived from the random id,
// and the nick only travels inside the encrypted hello.
//
// This is a deliberately small mDNS implementation (RFC 6762/6763), not a
// general one: it answers questions about _termchat._tcp and nothing else.
// Off-the-shelf Go mDNS libraries bind port 5353 without SO_REUSEPORT, which
// fails on macOS because mDNSResponder already holds that port.
package discovery

import (
	"net"
	"strconv"
	"strings"

	"github.com/sw4p/termchat/internal/protocol"
)

// Service is the mDNS service type every termchat peer advertises.
const Service = "_termchat._tcp"

// State is what a peer advertises about itself.
type State string

const (
	Available State = "available" // looking for someone: dial me
	Busy      State = "busy"      // chatting or idle: I'll answer busy
)

// Peer is another termchat instance found on the network.
type Peer struct {
	ID    string
	State State
	Addrs []string // "host:port" to dial, most likely to work first
}

func txtRecord(id string, state State) []string {
	return []string{
		"id=" + id,
		"v=" + strconv.Itoa(protocol.Version),
		"state=" + string(state),
	}
}

// entry is one service instance as collected from mDNS answers, before
// validation.
type entry struct {
	Instance string   // the first label of the instance name: should be the id
	Port     int      // from SRV
	Text     []string // from TXT
	IPs      []net.IP // the responder's source address, then its A records
}

// parseEntry turns an mDNS answer into a Peer. It rejects anything that isn't
// a well-formed termchat advertisement for our protocol version.
func parseEntry(e entry) (Peer, bool) {
	txt := map[string]string{}
	for _, kv := range e.Text {
		if k, v, ok := strings.Cut(kv, "="); ok {
			txt[k] = v
		}
	}
	id := txt["id"]
	if !protocol.ValidID(id) || e.Instance != id {
		return Peer{}, false
	}
	if txt["v"] != strconv.Itoa(protocol.Version) {
		return Peer{}, false
	}
	state := State(txt["state"])
	if state != Available && state != Busy {
		return Peer{}, false
	}
	if e.Port <= 0 || e.Port > 65535 {
		return Peer{}, false
	}

	port := strconv.Itoa(e.Port)
	var addrs []string
	seen := map[string]bool{}
	for _, ip := range e.IPs {
		ip4 := ip.To4()
		if ip4 == nil || ip4.IsUnspecified() || ip4.IsMulticast() {
			continue // IPv4 only for now
		}
		a := net.JoinHostPort(ip4.String(), port)
		if !seen[a] {
			seen[a] = true
			addrs = append(addrs, a)
		}
	}
	if len(addrs) == 0 {
		return Peer{}, false
	}
	return Peer{ID: id, State: state, Addrs: addrs}, true
}

// localAddrs returns the IPv4 addresses peers can reach us on: every
// interface that is up and does multicast, except loopback and point-to-point
// links (VPN tunnels). With no such interface (offline), it returns 127.0.0.1
// so instances on the same machine can still find each other.
func localAddrs() []net.IP {
	var ips []net.IP
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		const want = net.FlagUp | net.FlagRunning | net.FlagMulticast
		if iface.Flags&want != want || iface.Flags&(net.FlagLoopback|net.FlagPointToPoint) != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok {
				if ip4 := ipnet.IP.To4(); ip4 != nil && !ip4.IsLinkLocalUnicast() {
					ips = append(ips, ip4)
				}
			}
		}
	}
	if len(ips) == 0 {
		return []net.IP{net.IPv4(127, 0, 0, 1).To4()}
	}
	return ips
}
