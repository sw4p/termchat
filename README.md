# termchat

Chat with a random stranger on your local network, from the terminal.

- **No server, no account.** termchat finds other people on the same Wi-Fi or LAN by itself.
- **Anonymous.** You get a random nick like `quiet-otter`. Your hostname and real name are never sent.
- **End-to-end encrypted.** Every chat gets fresh keys, and nothing is saved.
- **Text only, one to one.** Skip to someone new with `/next`, or go invisible with `/invisible`.

```
hi, quiet-otter. looking for someone on your network… (/help for commands)
─── new chat with brave-lynx ───
! Strangers may send links or commands. termchat never asks you to run anything.
brave-lynx: hey! anyone else stuck in this meeting?
you: haha yes
[brave-lynx is typing…] >
```

## Install

**macOS (Homebrew):**

```sh
brew install sw4p/termchat/termchat
```

**Linux or macOS (install script):** the script downloads the latest release, checks its SHA-256 checksum, and installs to `/usr/local/bin`. If that directory isn't writable, it installs to `~/.local/bin` instead.

```sh
curl -fsSL https://github.com/sw4p/termchat/releases/latest/download/install.sh | sh
```

**Debian/Ubuntu:** download the `.deb` from the [releases page](https://github.com/sw4p/termchat/releases), then:

```sh
sudo apt install ./termchat_*_amd64.deb
```

For Fedora and other RPM-based systems, there's also a `.rpm` on the releases page.

## Use

```sh
termchat                       # find someone on your network
termchat --nick sam            # choose your nick
termchat --no-bell             # don't beep on new messages
```

termchat looks for other people running it on the same network and connects you to one of them at random. If nobody is around yet, it waits and connects you as soon as someone shows up.

| Command | What it does |
|---|---|
| `/next` | leave this chat and find someone new |
| `/leave` | leave this chat and stop looking |
| `/find` | start looking again |
| `/invisible` | stop being findable (also leaves the current chat) |
| `/visible` | become findable again |
| `/nick <name>` | change your nick (takes effect in your next chat) |
| `/who` | how many people are available right now |
| `/help` | list the commands |
| `/quit` | leave and exit (Ctrl-C and Ctrl-D work too) |

To send a message that starts with `/`, double it: `//shrug` sends `/shrug`.

Messages are limited to 160 characters. Editing keys:
- **Backspace** deletes the last character.
- **Ctrl-W** deletes the last word.
- **Ctrl-U** clears the line.

You're never matched with the same person twice in one session.

### Manual mode

If automatic discovery doesn't work on your network (some office and guest networks block it), connect directly:

```sh
termchat --listen :9000                  # on one machine
termchat --connect 192.168.1.23:9000     # on the other
```

## Firewalls and networks

termchat uses **mDNS** (the protocol behind AirDrop, AirPlay and printer discovery) on **UDP port 5353** to find people, and **TCP** on a random port to chat.

- **macOS:** the first time you run termchat, macOS may ask whether to allow incoming connections. Click **Allow**.
- **Linux:** allow UDP 5353 and incoming TCP on your LAN. With `ufw`, run `sudo ufw allow 5353/udp`, and allow termchat's TCP port (or use manual mode with a fixed port).
- **VPNs:** some VPNs block local-network traffic. If nobody shows up, try with the VPN off.
- **Guest and office Wi-Fi** often isolates devices from each other. Discovery then finds nobody, and termchat prints a hint after a minute.

## Privacy and security

**Nothing is stored.** No history, no logs and no files. When you quit, the chat is gone, except for whatever is still in your terminal's scrollback.

**What others on your network can see:**
- **Discovery:** anyone on the network can see that someone is running termchat. They see a random session id, your IP address and port, and whether you're available or busy. They never see your hostname or nick.
- **Your nick:** it's only sent inside the encrypted connection, to the person you're matched with.

**Encryption:** each connection starts with a fresh X25519 key exchange. Every message is then sealed with ChaCha20-Poly1305, using separate keys for each direction. Messages that are tampered with, replayed or reordered are detected, and the connection is dropped. Someone *passively* capturing Wi-Fi traffic sees only ciphertext.

**What it doesn't protect against:** termchat is anonymous, so there's no way to verify *who* you're talking to. An attacker actively intercepting traffic on your local network (a man-in-the-middle) could sit between you and your partner and read the chat. Don't share anything you wouldn't say to a stranger in the same room, because that's who you're talking to.

**Hostile peers:**
- **Escape sequences:** termchat strips terminal escape sequences and control characters from everything it receives. A stranger can't clear your screen, change your window title, or write to your clipboard.
- **Nothing runs:** termchat never runs, opens or saves anything it receives. It contains no code to do so.
- **Size limits:** messages are capped at 160 characters and frames at 1 KB. Malformed input is dropped.
- **Connection flooding:** connections must finish the handshake within 5 seconds, and only 4 unfinished connections are allowed at once.

**Links:** strangers may send links or commands. termchat never asks you to run anything, so don't.

**Install script:** it downloads over HTTPS and verifies the release's SHA-256 checksum before installing.

## Development

```sh
go test -race ./...                        # all tests, with the race detector
go run ./cmd/termchat                      # run it
TERMCHAT_MDNS_TEST=1 go test ./internal/discovery   # includes a real mDNS round trip
goreleaser release --snapshot --clean      # build release artifacts into dist/
```

Releases are built by GitHub Actions when a `v*` tag is pushed.

## License

termchat is licensed under the [GNU Affero General Public License v3.0](LICENSE) (AGPL-3.0-only). See [CONTRIBUTING.md](CONTRIBUTING.md) before sending a pull request.
