# termchat

Chat with a random individual on your local network, from the terminal. Messages are limited to 160 characters.

- **No server, no account.** termchat finds other people on the same Wi-Fi or LAN by itself.
- **Anonymous.** You get a random nick like `quiet-otter`. Your hostname and real name are never sent.
- **End-to-end encrypted.** Every chat gets fresh keys, and nothing is saved.
- **Text only, one to one.** Skip to someone new with `/next`, or go invisible with `/invisible`.

```
hi, quiet-otter. looking for someone on your network… (/help for commands)
─── new chat with brave-lynx ───
* Strangers may send links or commands. termchat never asks you to run anything.
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

To install a specific version or to a different directory, set `TERMCHAT_VERSION` (e.g. `0.1.0`) or `TERMCHAT_INSTALL_DIR`:

```sh
curl -fsSL https://github.com/sw4p/termchat/releases/latest/download/install.sh | TERMCHAT_INSTALL_DIR="$HOME/bin" sh
```

**Debian/Ubuntu:** download the `.deb` from the [releases page](https://github.com/sw4p/termchat/releases/latest), then:

```sh
sudo apt install ./termchat_*_amd64.deb
```

**Fedora and other RPM-based systems:** download the `.rpm` from the releases page, then:

```sh
sudo dnf install ./termchat_*_amd64.rpm
```

**Manual download:** each release has a `.tar.gz` for every platform (`darwin` is macOS; `arm64` is Apple Silicon and ARM Linux, `amd64` is Intel/AMD). Download the archive and `checksums.txt`, then:

```sh
shasum -a 256 -c checksums.txt --ignore-missing   # or: sha256sum -c checksums.txt --ignore-missing
tar -xzf termchat_*.tar.gz termchat
sudo mv termchat /usr/local/bin/
```

Homebrew and the install script handle this for you.

## Update and uninstall

| Installed with | Update | Uninstall |
|---|---|---|
| Homebrew | `brew upgrade termchat` | `brew uninstall termchat` |
| Install script | run the script again | `rm "$(command -v termchat)"` |
| `.deb` | install the new `.deb` | `sudo apt remove termchat` |
| `.rpm` | install the new `.rpm` | `sudo dnf remove termchat` |

termchat stores no settings or history, so there's nothing else to clean up.

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

### Manual mode

If automatic discovery doesn't work on your network (some office and guest networks block it), connect directly:

```sh
termchat --listen :9000                  # on one machine
termchat --connect 192.168.1.23:9000     # on the other
```

## Privacy and security

**Nothing is stored.** No history, no logs and no files. When you quit, the chat is gone, except for whatever is still in your terminal's scrollback.

**Links:** strangers may send links or commands. termchat never asks you to run anything, so don't.

**Install script:** it downloads over HTTPS and verifies the release's SHA-256 checksum before installing.

## About this repository

This repository hosts termchat's releases: prebuilt binaries for macOS and Linux (amd64 and arm64), `.deb` and `.rpm` packages, `checksums.txt` and the install script.
