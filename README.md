# WiNotification

> Windows notification forwarder with system tray control

**Author:** Hadi Cahyadi \<cumulus13@gmail.com\>  
**Homepage:** <https://github.com/cumulus13/WiNotification>  
**Language:** Go 1.21+  
**Platform:** Windows 10 1709 (Fall Creators Update) or later

---

## What it does

WiNotification runs silently in the system tray and captures **every notification** that appears in the Windows Action Center (the panel you open from the clock area in the taskbar). Each captured notification is fanned-out in parallel to any combination of the following backends:

| Backend | Protocol | Notes |
|---------|----------|-------|
| **Growl** | GNTP | Compatible with Growl for Windows, Snarl, etc. |
| **ntfy** | HTTPS | Works with ntfy.sh or self-hosted server |
| **RabbitMQ** | AMQP 0-9-1 | Publishes to a configurable exchange |
| **ZeroMQ** | TCP (PUB/PUSH) | Bind on any port for subscribers |
| **Redis** | TCP | SET with TTL + optional PUBLISH to channel |
| **Database** | SQL | SQLite / MySQL / PostgreSQL / SQL Server via GORM |
| **Toast** | WinRT | Re-broadcast filtered notifications as new toasts |

All backends are individually enabled/disabled in `config.toml`.

---

## Quick start

### 1. Prerequisites

| Tool | Minimum version |
|------|----------------|
| Go | 1.21 |
| CGO-capable C compiler | MinGW-w64 (for ZeroMQ) |
| Windows | 10 1709 / 11 |

Install MinGW-w64 via [winget](https://winget.run/):
```powershell
winget install -e --id msys2.msys2
# Then in MSYS2: pacman -S mingw-w64-x86_64-gcc
```

### 2. Clone & fetch dependencies

```powershell
git clone https://github.com/cumulus13/WiNotification
cd WiNotification
go mod tidy
```

### 3. Grant notification access (once)

Windows requires explicit user consent before a third-party app can read
the notification list:

```powershell
go run ./cmd/winotification --request-access
```

Accept the system prompt that appears. This only needs to be done once per user account.

### 4. Configure

Edit **`config.toml`** — enable the backends you want and fill in
credentials. The file is heavily commented; every option is explained inline.

### 5. Build & run

```powershell
# Debug build (console window visible — good for testing)
.\scripts\build.ps1

# Release build (no console window, smaller binary)
.\scripts\build.ps1 -Release

# Run
.\dist\WiNotification.exe
```

---

## System tray menu

Right-click the tray icon (🔔) to see:

| Item | Action |
|------|--------|
| **● Running** / **⏸ Paused** / **⏹ Stopped** | Current status (disabled label) |
| **⏸ Pause** | Suspend forwarding; notifications are still captured but not dispatched |
| **▶ Resume** | Resume forwarding |
| **⏹ Stop** | Stop the capture loop entirely (app stays in tray) |
| **▶ Start** | Restart the capture loop |
| **ℹ About** | Show version info |
| **✕ Quit** | Exit gracefully, closing all backend connections |

---

## Configuration reference

All settings live in `config.toml` (TOML format).  
Environment variable overrides use the prefix `WINOTIF_` with dots replaced by `_`, e.g.:

```
WINOTIF_NTFY_TOKEN=mytoken ./WiNotification.exe
```

### `[general]`

| Key | Default | Description |
|-----|---------|-------------|
| `log_level` | `"info"` | `debug` / `info` / `warn` / `error` |
| `log_file` | `"winotification.log"` | Path for log file (empty = stdout only) |
| `icon_path` | `"icons/icon.ico"` | Tray icon (ICO format) |
| `capture_interval_ms` | `500` | Polling interval in milliseconds |
| `filter_apps` | `[]` | Allow-list of app names (empty = all) |
| `ignore_apps` | `["Microsoft.WindowsStore"]` | Block-list of app names |

### `[growl]`

```toml
[growl]
enabled  = true
host     = "127.0.0.1"
port     = 23053
password = ""
app_name = "WiNotification"
icon     = "icons/icon.png"
```

### `[ntfy]`

```toml
[ntfy]
enabled    = true
server_url = "https://ntfy.sh"
topic      = "winotification"
token      = ""          # Bearer token for protected topics
icon_url   = "https://raw.githubusercontent.com/cumulus13/WiNotification/main/icons/icon.png"
priority   = "default"   # min | low | default | high | urgent
```

### `[rabbitmq]`

```toml
[rabbitmq]
enabled       = true
url           = "amqp://guest:guest@localhost:5672/"
exchange      = "winotification"
exchange_type = "fanout"
routing_key   = ""
durable       = true
```

### `[zeromq]`

```toml
[zeromq]
enabled     = true
socket_type = "pub"          # pub | push
bind        = "tcp://*:5556"
```

Subscribers connect with `SUB` / `PULL` socket and filter on topic `winotification `.

### `[redis]`

```toml
[redis]
enabled        = true
host           = "127.0.0.1"
port           = 6379
password       = ""
db             = 0
key_prefix     = "winotif:"
ttl            = 86400       # seconds; 0 = no expiry
pubsub_channel = "winotification"
publish        = true
```

### `[database]`

```toml
[database]
enabled     = true
type        = "sqlite"       # sqlite | mysql | postgres | sqlserver
sqlite_path = "winotification.db"

# For other engines:
host     = "localhost"
port     = 5432
username = "winotif"
password = "secret"
dbname   = "winotification"
params   = ""                # extra DSN params
```

### `[toast]`

```toml
[toast]
enabled  = true
app_id   = "WiNotification"
duration = "short"           # short | long
audio    = "ms-winsoundevent:Notification.Default"
```

---

## Notification JSON schema

All forwarders receive the same JSON-serialisable struct:

```json
{
  "id":         "550e8400-e29b-41d4-a716-446655440000",
  "app_name":   "Microsoft.Teams",
  "title":      "Hadi Cahyadi",
  "body":       "Hey, are you free now?",
  "tag":        "",
  "group":      "",
  "sequence":   42,
  "arrived_at": "2025-04-01T08:30:00Z"
}
```

---

## Project layout

```
WiNotification/
├── cmd/
│   └── winotification/
│       └── main_windows.go      # entry point
├── internal/
│   ├── capture/
│   │   ├── notification.go      # shared Notification model
│   │   └── engine_windows.go    # WinRT polling engine
│   ├── config/
│   │   └── config.go            # Viper-based config loader
│   ├── forwarder/
│   │   ├── forwarder.go         # Forwarder interface + Dispatcher
│   │   ├── growl.go
│   │   ├── ntfy.go
│   │   ├── rabbitmq.go
│   │   ├── zeromq.go
│   │   ├── redis.go
│   │   ├── database.go
│   │   └── toast_windows.go
│   ├── logger/
│   │   └── logger.go
│   └── systray/
│       └── manager_windows.go
├── icons/                       # place icon.ico and icon.png here
├── scripts/
│   └── build.ps1
├── config.toml                  # main configuration file
├── go.mod
├── Makefile
└── README.md
```

---

## Adding your own icon

Drop two files into the `icons/` folder:

- `icon.ico` — 32×32 or 256×256 ICO for the system tray
- `icon.png` — PNG for Growl and ntfy

Free tools: [IcoFX](https://icofx.ro/), [ConvertICO](https://convertico.com/).

---

## Troubleshooting

| Symptom | Fix |
|---------|-----|
| No notifications captured | Run `--request-access` and accept the dialog |
| Growl: "connection refused" | Ensure Growl / Snarl is running and GNTP is enabled |
| ZeroMQ build error | Install MinGW-w64 and set `CGO_ENABLED=1` |
| ntfy messages not received | Check `topic` and `token` in config; verify server URL |
| App crashes on start | Check `winotification.log` for the error |

---

## License

MIT © 2026 Hadi Cahyadi

## 👤 Author
        
[Hadi Cahyadi](mailto:cumulus13@gmail.com)
    

[![Buy Me a Coffee](https://www.buymeacoffee.com/assets/img/custom_images/orange_img.png)](https://www.buymeacoffee.com/cumulus13)

[![Donate via Ko-fi](https://ko-fi.com/img/githubbutton_sm.svg)](https://ko-fi.com/cumulus13)
 
[Support me on Patreon](https://www.patreon.com/cumulus13)