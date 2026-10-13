// Command termchat pairs you with a random stranger on your local network
// for a one-to-one, end-to-end encrypted terminal chat.
//
//	termchat                          find someone on the LAN automatically
//	termchat --listen :9000           manual: wait for a connection
//	termchat --connect host:9000      manual: connect to someone
package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"os/signal"
	"syscall"

	"github.com/sw4p/termchat/internal/discovery"
	"github.com/sw4p/termchat/internal/nick"
	"github.com/sw4p/termchat/internal/protocol"
	"github.com/sw4p/termchat/internal/session"
	"github.com/sw4p/termchat/internal/ui"
)

// version is set at build time by GoReleaser:
//
//	go build -ldflags "-X main.version=1.2.3" ./cmd/termchat
var version = "dev"

func main() {
	// os.Exit skips deferred calls, so all the real work (including the
	// deferred terminal restore) happens in run, and main only exits.
	os.Exit(run())
}

func run() int {
	nickFlag := flag.String("nick", "", "nickname to chat as (default: random)")
	listenAddr := flag.String("listen", "", "manual mode: wait for connections on `addr`, e.g. :9000")
	connectAddr := flag.String("connect", "", "manual mode: connect to a peer listening at `host:port`")
	noBell := flag.Bool("no-bell", false, "don't ring the terminal bell when a message arrives")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("termchat", version)
		return 0
	}

	name := *nickFlag
	if name == "" {
		r := rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
		name = nick.Random(r)
	} else if !nick.Valid(name) {
		fmt.Fprintf(os.Stderr, "termchat: invalid nick %q: use 1-%d letters, digits, '-' or '_'\n", name, nick.MaxLen)
		return 2
	}
	if *listenAddr != "" && *connectAddr != "" {
		fmt.Fprintln(os.Stderr, "termchat: use only one of --listen and --connect")
		return 2
	}

	con, err := ui.Open(protocol.MaxMessageRunes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "termchat: %v\n", err)
		return 1
	}
	// Restore the terminal however run ends, even on a panic.
	defer con.Close()

	// In raw mode Ctrl-C arrives as a key, not a signal, but `kill` or a
	// closed terminal window still sends one. Treat those like Ctrl-C:
	// leave the chat politely, then restore the terminal on the way out.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)
	go func() {
		if _, ok := <-sigs; ok {
			con.Interrupt()
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	id := protocol.NewID()
	cfg := session.Config{ID: id, Nick: name, NoBell: *noBell, Timeouts: session.DefaultTimeouts, Out: con}
	switch {
	case *listenAddr != "":
		cfg.ListenAddr = *listenAddr
		con.Hello(name, fmt.Sprintf("hi, %s. waiting for someone to connect on %s (/help)", name, *listenAddr))
	case *connectAddr != "":
		cfg.Directory = session.StaticPeer(*connectAddr)
		cfg.Verbose = true
		con.Hello(name, fmt.Sprintf("hi, %s. connecting to %s … (/help)", name, *connectAddr))
	default:
		browser := discovery.NewBrowser(id)
		go func() {
			if err := browser.Run(ctx); err != nil {
				con.Println("* LAN discovery isn't working: " + err.Error())
			}
		}()
		cfg.ListenAddr = ":0"
		cfg.Directory = browser
		cfg.Advertise = func(port int, st discovery.State) (session.Advertiser, error) {
			adv, err := discovery.Advertise(id, port, st)
			if err != nil {
				return nil, err // not adv: a nil *Advertiser in an interface isn't nil
			}
			return adv, nil
		}
		con.Hello(name, fmt.Sprintf("hi, %s. looking for someone… (/help)", name))
	}

	m := session.New(cfg)
	if err := m.Start(ctx); err != nil {
		con.Close() // before printing, so the error isn't drawn in raw mode
		fmt.Fprintf(os.Stderr, "termchat: %v\n", err)
		return 1
	}
	runCommands(ctx, con, m)
	m.Stop()
	return 0
}
