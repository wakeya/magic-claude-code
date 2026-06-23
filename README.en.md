# Magic Claude Code

[简体中文](README.md) | English

Use the full Claude Code client experience with MCC Proxy — route Claude Code through cost-effective third-party models and cut costs by 80%+.

## 💡 Why MCC Proxy

Official Claude Code subscriptions are expensive, while Chinese open-source large models offer excellent value. MCC Proxy lets you **keep the full Claude Code experience while calling cost-effective third-party models**, reducing costs by 80%+:

| Official Model | Replace With | Use Case |
|----------------|-------------|----------|
| claude-opus | **GLM-5.2** / **MiniMax-M3** | Complex coding, deep reasoning |
| claude-sonnet | **kimi-k2.7-code** / **deepseek-v4-pro** | Daily coding, code generation |
| claude-haiku | **mimo-v2.5-pro** / **agnes-2.0-flash** | Fast response, sub-agent tasks |

Switching is transparent to Claude Code via automatic model mapping. Hardcoded requests (telemetry, feature flags, etc.) are also intercepted at the network layer, preventing optimization features from being disabled when a third-party API is in use.

## 🎬 Demo

![MCC Proxy Demo](images/visual_en.gif)

## ✨ Features

- Transparently proxies all Claude Code API requests
- **Multi-provider management**: configure multiple API providers and switch freely
- **Model mapping**: automatically maps request model names to cost-effective provider-supported models (GLM-5.2, MiniMax-M3, kimi-k2.7-code, deepseek-v4-pro, mimo-v2.5-pro, agnes-2.0-flash, etc.) for major cost savings
- Auto-generated CA certificate (10-year validity)
- Frontend configuration page for providers and model mappings
- Password-protected configuration page
- Single-container Docker deployment
- Hot-reload configuration without restart
- **Auto bootstrap**: automatically attempts hosts modification, CA trust installation, and environment persistence on startup

## 🔗 Connection Modes

The system supports three connection modes with automatic priority-based fallback:

| Priority | Mode | Entry | Intercepts hardcoded `api.anthropic.com`? | Privilege Required |
|----------|------|-------|-----|------|
| 1 | **Transparent Mode** | hosts + 443 TLS | ✅ Yes | Host modification and CA trust |
| 2 | **Tunnel Mode** | HTTPS_PROXY + CONNECT MITM | ⚠ Mostly yes | No hosts change; runtime must trust CA |
| 3 | **Gateway Mode** | ANTHROPIC_BASE_URL | ❌ No | No hosts/CA/443 needed |

### Auto Bootstrap

On startup, the proxy automatically attempts the following in priority order:

1. Ensure the CA certificate exists
2. Attempt to modify hosts (`127.0.0.1 api.anthropic.com`)
3. Attempt to install/trust the CA into the system certificate store
4. Attempt to persist the executable directory as the MCC root

**Use administrator privileges on first run** to maximize the chance of automatic bootstrap success. If privileges are insufficient, the system will:
- Log the exact failure reason
- Print a description of the missing capability
- Output the startup command for the fallback mode
- Not block proxy startup

### Log Language Rule

- Chinese systems (`zh*` locale) → Chinese logs and guidance
- Other languages → English logs and guidance
- Override manually with the `MCC_LANG` environment variable

### When Fallback Occurs

- **Transparent Mode fails** (hosts modification or CA installation fails) → automatically falls back to Tunnel Mode
- **Tunnel Mode unavailable** (cannot set proxy environment variables) → falls back to Gateway Mode
- Gateway Mode only covers clients that honor `ANTHROPIC_BASE_URL`; it cannot intercept hardcoded requests

### ⚖️ Mode Tradeoffs

**Transparent Mode / Tunnel Mode** (recommended) — advantages:

- **Full interception**: all `api.anthropic.com` requests (including BigQuery metrics, 1P event logs, GrowthBook feature flags) are intercepted at the network/proxy layer — no direct-connect leakage
- **Zero startup latency**: GrowthBook, metric checks, etc. are answered locally and instantly by the proxy — no more 5–10s timeout waits
- **Full functionality**: GrowthBook loads normally; memory search (coral_fern), sub-agent slimming (slim_subagent_claudemd), tool optimizations (birch_trellis, etc.) all take effect per configuration
- **TLS encrypted**: port 443 provides standard HTTPS
- **Hardcoded coverage**: intercepts direct requests that bypass `ANTHROPIC_BASE_URL`

**Gateway Mode** (lowest-privilege fallback) — limitations:

- **Telemetry direct-connect**: BigQuery metrics, 1P event logs, etc. bypass the proxy and reach Anthropic directly — cannot be intercepted
- **Startup stall**: GrowthBook initialization connects directly to `api.anthropic.com`, timing out for 5–10s on China-mainland networks; failed requests persistently retry, creating a repeated timeout loop
- **Feature degradation**: when GrowthBook fails to load, memory system, sub-agent optimizations, and tool interactions (Bash permission tree, clipboard images, extended thinking, etc.) cannot be enabled per server configuration
- **MCP/WebFetch affected**: the MCP official registry and WebFetch domain safety checks connect directly to `api.anthropic.com`; timeouts affect usage
- **Local HTTP plaintext only**, no TLS encryption — not suitable for cross-network use
- **Each client needs its own** `ANTHROPIC_BASE_URL` configuration; no global transparent interception

> To cover all traffic (including telemetry and feature flags), use **Transparent Mode** or **Tunnel Mode**.

> The mode entry in the top header shows detailed explanations of all three modes.

## 📦 Release

Releases are built automatically by CI triggered by `v*` tags. GitHub/GitLab generate and upload cross-platform binary assets; Gitee/GitCode upload attachments via the Release API. For the developer commit and release workflow see [AGENTS.md](AGENTS.md).

## 🚀 Quick Start

### 1. Deploy with Docker

#### Option A: Using docker build (recommended)

```bash
# Clone the project
git clone <repo-url>
cd magic-claude-code

# Build the image
docker build -t magic-claude-code .

# Run the container
docker run -d \
  --name mcc \
  -p 443:443 \
  -p 8442:8442 \
  -v ./data:/app/data \
  -v "${CLAUDE_PROJECTS_DIR:-$HOME/.claude/projects}:/claude-projects:ro" \
  -e ADMIN_PASSWORD=admin123 \
  -e CLAUDE_PROJECTS_DIR=/claude-projects \
  --cap-add NET_BIND_SERVICE \
  --restart=unless-stopped \
  magic-claude-code

# Check logs for setup hints
docker logs mcc
```

#### Option B: Using docker-compose

```bash
# Clone the project
git clone <repo-url>
cd magic-claude-code

# Install docker-compose-plugin (if not installed)
sudo apt-get install docker-compose-plugin

# Start the service
docker compose up -d

# Check logs for setup hints
docker logs mcc
```

### 2. Build Test and Verify

```bash
# 1. Test building the image
docker compose build

# 2. List the built image
docker images | grep magic-claude-code

# 3. Start the service
docker compose up -d

# 4. View service logs
docker compose logs -f

# 5. Verify the service is running
curl -k https://localhost:8442

# 6. Check container status
docker compose ps
```

### 3. Rebuild and Redeploy

After code updates, rebuild the image and restart the container:

```bash
# One-shot rebuild and redeploy
docker compose up -d --build

# View startup logs
docker compose logs -f
```

### 4. Run as a Binary (non-Docker)

Binary mode suits environments where Docker is not desired. The service listens on fixed ports:

- `443`: proxy entry, receives `https://api.anthropic.com` requests
- `8442`: configuration page

> **💡 Quick setup**: Release asset bundles include a host setup script that performs hosts mapping + CA trust installation in one step (with China-mirror fallback — automatically switches to Alibaba/Tsinghua mirrors when overseas apt sources fail). When building from source, the scripts live under `scripts/`.
>
> ```bash
> # Linux / macOS (sudo required)
> sudo ./setup-host.sh
>
> # Windows (admin PowerShell)
> .\setup-host.ps1
> ```
>
> Selective configuration is supported: `setup-host.sh hosts` (hosts only), `setup-host.sh trust` (CA only).
> Normally `sudo ./mcc` performs the same configuration automatically on startup; the scripts are only needed when auto-configuration fails or for standalone operations.

#### macOS / Linux

Build from source:

```bash
# Clone the project
git clone <repo-url>
cd magic-claude-code

# Build frontend assets
npm --prefix internal/frontend ci
npm --prefix internal/frontend run build

# Build the binary
make build
```

Start the service:

```bash
# On Linux, grant the capability to bind port 443
sudo setcap 'cap_net_bind_service=+ep' ./bin/mcc

# Start (set the admin password explicitly)
./bin/mcc -data ./data -password "your-admin-password"
```

If `setcap` is unavailable, start with administrator privileges:

```bash
sudo ./bin/mcc -data ./data -password "your-admin-password"
```

Add the hosts mapping:

```bash
echo "127.0.0.1 api.anthropic.com" | sudo tee -a /etc/hosts
```

Configure Node.js to use the proxy CA:

```bash
echo 'export NODE_EXTRA_CA_CERTS=/absolute/path/to/magic-claude-code/data/ca.crt' >> ~/.bashrc
source ~/.bashrc
```

#### Windows

Windows can use `mcc.exe` directly. To build from source on Windows:

```powershell
npm --prefix internal/frontend ci
npm --prefix internal/frontend run build
go build -o mcc.exe ./cmd/server
```

You can also cross-compile for Windows from macOS/Linux:

```bash
GOOS=windows GOARCH=amd64 go build -o bin/mcc-windows-amd64/mcc.exe ./cmd/server
```

Recommended directory layout:

```text
C:\mcc\
  mcc.exe
  data\
```

Start with an admin PowerShell:

```powershell
cd C:\mcc
.\mcc.exe -data .\data -password "your-admin-password"
```

If you run `.\mcc.exe` directly, the data directory defaults to `.\data` under the current directory. When no password is specified via `-password` or `ADMIN_PASSWORD`, the program generates a random admin password and prints it once to startup output; when running in the background, read the `randomly generated admin password` from stdout logs.

Add the hosts mapping:

```powershell
Add-Content -Path "$env:WINDIR\System32\drivers\etc\hosts" -Value "`n127.0.0.1 api.anthropic.com"
```

Import the CA certificate into the current user's trusted root:

```powershell
Import-Certificate -FilePath "C:\mcc\data\ca.crt" -CertStoreLocation Cert:\CurrentUser\Root
```

To apply to all users, import into the local machine trusted root with an admin PowerShell:

```powershell
Import-Certificate -FilePath "C:\mcc\data\ca.crt" -CertStoreLocation Cert:\LocalMachine\Root
```

Configure Node.js to use the proxy CA:

```powershell
setx NODE_EXTRA_CA_CERTS "C:\mcc\data\ca.crt"
```

Close and reopen the terminal, then start Claude Code.

If port 443 or 8442 is occupied, check the occupying process:

```powershell
netstat -ano | findstr ":443"
netstat -ano | findstr ":8442"
```

If the hosts modification does not take effect, flush DNS:

```powershell
ipconfig /flushdns
```

#### Binary Mode Notes

- **Use administrator privileges on first run** to allow auto bootstrap (hosts modification, CA installation, environment persistence).
- Bootstrap failure does not block startup; the system falls back to Tunnel Mode or Gateway Mode (see "Connection Modes").
- The first launch generates `ca.crt`, `ca.key`, `server.crt`, `server.key`, and `proxy.db` in the `data` directory.
- The first successful run persists the executable directory as `MCC_ROOT`; subsequent launches from any working directory resolve the certificate automatically.
- Always set the admin password via `-password` or `ADMIN_PASSWORD`; if unset, a random password is generated and printed once to startup output.
- Usage statistics default to reading the current user's Claude Code session directory: `~/.claude/projects`. Override with `CLAUDE_PROJECTS_DIR` if needed.

### 5. Install the CA Certificate

The proxy uses a self-signed CA certificate that must be trusted on the client machine. There are three options:

#### Option 1: Specify the certificate path (simplest)

Specify the certificate file path directly via an environment variable.

```bash
# Add the ca.crt path to an environment variable
echo 'export NODE_EXTRA_CA_CERTS=/path/to/data/ca.crt' >> ~/.bashrc
source ~/.bashrc
```

#### Advantages

- Simple configuration — one command does it
- Compatible with all Node.js versions

#### Disadvantages

- Only Node.js applications use this certificate
- Path must be updated when the project moves
- If the certificate location changes, the environment variable must be updated

#### Option 2: System certificate store + Node.js system CA support (recommended)

Install the certificate into the system certificate store and have Node.js read system certificates.

**macOS:**

```bash
# 1. Install the certificate into the system keychain
sudo security add-trusted-cert -d -r trustRoot \
  -k /Library/Keychains/System.keychain ./data/ca.crt

# 2. Set Node.js to use the system certificate store (requires Node.js 16+)
echo 'export NODE_OPTIONS="--use-system-ca"' >> ~/.bashrc
source ~/.bashrc

# 3. Configure desktop environment variables (so GUI apps also use the certificate)
# macOS uses launchctl setenv to set environment variables
launchctl setenv NODE_OPTIONS "--use-system-ca"

# To make the configuration permanent, add it to ~/.zshrc or ~/.bash_profile (recommended)
echo 'export NODE_OPTIONS="--use-system-ca"' >> ~/.zshrc  # zsh users
```

**Linux (Ubuntu/Debian):**

```bash
# 1. Copy the certificate to the system certificate directory
sudo cp ./data/ca.crt /usr/local/share/ca-certificates/

# 2. Update the certificate store
sudo update-ca-certificates

# If you see "skipping duplicate certificate", the certificate is already installed — no need to worry

# 3. Set Node.js to use the system certificate store (requires Node.js 16+)
echo 'export NODE_OPTIONS="--use-system-ca"' >> ~/.bashrc
source ~/.bashrc

# 4. Configure desktop environment variables (so GUI apps also use the certificate)
cat >> ~/.xprofile << 'EOF'
# Claude Code proxy certificate
export NODE_OPTIONS="--use-system-ca"
EOF

# Log out and back into the desktop environment, or reboot for .xprofile to take effect
```

#### Option 2 Advantages

- System-level trust — all applications recognize the certificate (curl, wget, browsers, etc.)
- Node.js also uses the system certificate store
- No need to update the environment variable path when the certificate is renewed
- Unified configuration across multiple projects

#### Option 2 Notes

- Requires Node.js 16 or higher
- Still need to configure the `NODE_OPTIONS` environment variable
- **Desktop apps need extra configuration**: applications launched from the desktop environment (such as VS Code) do not load `~/.bashrc`; configure `~/.xprofile` instead

#### Why is .xprofile needed?

| File | When loaded | Scope | Use case |
|------|-------------|-------|----------|
| `~/.bashrc` | When opening a terminal | Terminal sessions only | CLI tools, scripts |
| `~/.profile` | On login shell | Login sessions | SSH, TTY login |
| `~/.xprofile` | On desktop login | Entire desktop environment | GUI apps (VS Code, browsers, etc.) |

GUI applications launched from the desktop environment do not inherit `~/.bashrc` environment variables, so `~/.xprofile` is needed to set environment variables at desktop login, ensuring all desktop applications use the certificate correctly.

#### Option 3: System-level trust only (not recommended standalone)

Install the certificate into the system only, without configuring Node.js environment variables.

```bash
# Linux
sudo cp ./data/ca.crt /usr/local/share/ca-certificates/
sudo update-ca-certificates

# macOS
sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain ./data/ca.crt
```

#### Use Cases

- Non-Node.js applications (curl, wget, browsers, etc.)
- Combined with Option 1 or Option 2 (system tools + Node.js configured separately)

#### Note

- ⚠️ Node.js does not read the system certificate store by default
- Must be combined with `NODE_OPTIONS` from Option 1 or Option 2

### 6. Browser Certificate Import

The CA certificate installed in the system certificate store only affects command-line tools (curl, wget). Chrome and Firefox use a separate NSS certificate database and do not read the system CA — they need a separate import.

> **Prerequisite**: complete the system certificate store installation above (Option 2 or Option 3) before performing the following steps.

#### Common tool: install certutil

```bash
sudo apt install libnss3-tools -y
```

`certutil` is the CLI tool for managing NSS certificate databases; both Chrome and Firefox (apt version) use it to import certificates.

#### Chrome (Linux)

Chrome uses `~/.pki/nssdb` to store certificates under all installation methods:

```bash
# Import the CA into the Chrome NSS database
certutil -d sql:$HOME/.pki/nssdb -A -t "C,," -n "Claude Proxy Local CA" \
  -i /path/to/data/ca.crt

# Verify
certutil -d sql:$HOME/.pki/nssdb -L | grep -i "Claude"
```

After importing, **fully close Chrome** (including all windows) and reopen it.

#### Firefox (Linux)

The Firefox NSS database path depends on the installation method:

```bash
# Determine whether the current Firefox is snap or apt
which firefox | xargs ls -la 2>/dev/null | grep snap
```

##### apt Firefox

apt Firefox uses the standard NSS database under `~/.mozilla/firefox/`:

```bash
# Find the profile folder
PROFILE=$(ls ~/.mozilla/firefox/*/cert9.db 2>/dev/null | cut -d/ -f5 | head -1)
echo "Profile: $PROFILE"

# Import the CA
certutil -d sql:$HOME/.mozilla/firefox/${PROFILE} \
  -A -t "C,," -n "Claude Proxy Local CA" \
  -i /path/to/data/ca.crt

# Verify
certutil -d sql:$HOME/.mozilla/firefox/${PROFILE} -L | grep -i "Claude"
```

##### snap Firefox

snap Firefox runs in a sandboxed environment; **CLI import may not take effect**. Two recommended options:

Option 1: Import manually via the Firefox UI (recommended)

1. In the address bar, enter `about:preferences#privacy` and scroll to the **Certificates** section
2. Click **View Certificates** → switch to the **Authorities** tab
3. Click **Import** → select `data/ca.crt`
4. Check **"Trust this CA to identify websites"** → OK

Option 2: Switch to apt Firefox (radical fix)

The snap sandbox is the root limitation; switching to apt is recommended:

```bash
sudo snap disable firefox
sudo apt install firefox -y
```

Then follow the **apt Firefox** steps above to import the certificate.

#### snap vs apt browser comparison

| Installation | Characteristics | Reads system CA | certutil import |
|--------------|-----------------|-----------------|-----------------|
| apt browser | Standard install, runs in host environment | Yes | Takes effect directly |
| snap browser | Sandbox isolated, restricts host resource access | No | May not take effect |
| Chrome (any method) | Always uses ~/.pki/nssdb | No | Takes effect directly |

snap Firefox may not be readable by the Firefox process even if certutil writes successfully, due to sandbox restrictions. Therefore, for snap Firefox, **import via the Firefox UI** or **switch to apt** is recommended.

### 7. Configure the System

```bash
# Add the hosts mapping (point api.anthropic.com at the proxy)
echo "127.0.0.1 api.anthropic.com" | sudo tee -a /etc/hosts
```

### 8. Open the Configuration Page

Open a browser and visit: `https://localhost:8442`

Docker default password: `admin123`. For binary mode, use the password set via `-password` or `ADMIN_PASSWORD` at startup; if unset, a random password is generated and printed once to startup output.

## ⚙️ Configuration

### Port Privileges

The proxy service must bind port 443 (the default HTTPS port), which is a privileged port (< 1024) and requires special permissions.

**Docker deployment:**

`docker-compose.yml` already configures `cap_add: NET_BIND_SERVICE`, allowing the in-container process to bind the privileged port:

```yaml
cap_add:
  - NET_BIND_SERVICE
```

**Direct run (non-Docker):**

Grant the binary the capability to bind privileged ports:

```bash
# Option 1: use setcap (recommended)
sudo setcap 'cap_net_bind_service=+ep' ./bin/mcc

# Option 2: run with sudo
sudo ./bin/mcc -data ./data
```

**Note:** Switching to a non-privileged port (such as 8443) is not recommended because:

- Clients request `https://api.anthropic.com`, which defaults to port 443
- The hosts file maps the domain to 127.0.0.1 but cannot change the port number
- If the proxy listens on a non-443 port, client requests will fail

If a non-privileged port is mandatory:

```bash
# Use iptables to forward port 443 to 8443
sudo iptables -t nat -A PREROUTING -p tcp --dport 443 -j REDIRECT --to-port 8443
sudo iptables -t nat -A OUTPUT -p tcp --dport 443 -j REDIRECT --to-port 8443
```

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| ADMIN_PASSWORD | Admin password | admin123 |
| CLAUDE_PROJECTS_DIR | In-container Claude Code session log directory, used for usage statistics auto-reconciliation | /claude-projects |
| MCC_LANG | Manually set log language (`zh` or `en`) | Auto-detected from system |
| MCC_ROOT | MCC installation root directory, used for certificate discovery | Executable directory |
| MCC_HOST_HELPER | Host-side helper executable script path (Docker scenario; absolute path; the container invokes the helper's `hosts add` / `trust install` subcommands directly) | unset |

### Usage Statistics Auto-Reconciliation Directory

Usage statistics automatically scans Claude Code local session JSONL logs and imports the usage of completed requests into the statistics database. The service syncs once at startup and then every minute.

Inside the container, it reads:

```text
/claude-projects
```

The host-side path must be mounted into that directory via a Docker volume.

**Linux / macOS:**

Defaults to the current user's Claude Code session directory:

```text
$HOME/.claude/projects
```

If you use docker-compose, the default configuration already includes this mount:

```yaml
- ${CLAUDE_PROJECTS_DIR:-${HOME}/.claude/projects}:/claude-projects:ro
```

If your Claude Code logs are not in the default directory, override before starting:

```bash
export CLAUDE_PROJECTS_DIR=/path/to/.claude/projects
docker compose up -d --build
```

**Windows:**

Windows user directory paths differ; you cannot rely on the Linux/macOS default of `$HOME/.claude/projects`. Set `CLAUDE_PROJECTS_DIR` explicitly and prefer `/` as the path separator to avoid `:` and backslash escaping issues in Docker volumes.

PowerShell example:

```powershell
$env:CLAUDE_PROJECTS_DIR="C:/Users/YourUsername/.claude/projects"
docker compose up -d --build
```

`.env` example:

```env
CLAUDE_PROJECTS_DIR=C:/Users/YourUsername/.claude/projects
```

Then start:

```powershell
docker compose up -d --build
```

Notes:

- Docker Desktop must allow mounting that user directory.
- If the path does not exist, the service still starts, but session logs will not be imported and reconciliation data in usage statistics will be empty.
- The application code only reads the in-container `/claude-projects`; host-side path differences are handled by the Docker volume.

### Provider Configuration

Add providers on the frontend configuration page:

| Field | Description | Example |
|-------|-------------|---------|
| Name | Provider display name | Alibaba DashScope |
| API URL | Provider API endpoint | `https://dashscope.aliyuncs.com/api/v1/anthropic` |
| API Token | Authentication key | sk-xxx |
| Model Mapping | Client model → provider model | claude-sonnet-4 → qwen-max |

### Connection Modes

The "Connection Mode" area in the top header shows the current preferred mode, the effective mode, and buttons to switch among the three modes. Switching persists to the backend config and takes effect on the next startup.

| Mode | Use Case | `~/.claude/settings.json` |
|------|----------|---------------------------|
| Transparent Mode | Highest priority; intercepts hardcoded endpoints | Can stay default, or explicitly set `ANTHROPIC_BASE_URL=https://api.anthropic.com` |
| Tunnel Mode | No hosts change; forwards via proxy env vars | `HTTPS_PROXY=https://127.0.0.1:443` + `NODE_EXTRA_CA_CERTS=/path/to/data/ca.crt` |
| Gateway Mode | Lowest-privilege fallback; only covers explicitly configured clients | `ANTHROPIC_BASE_URL=http://127.0.0.1:17487` |

#### Transparent Mode

Transparent Mode has the highest priority and suits scenarios where you want to avoid changing the client as much as possible. Use administrator privileges on first run so the program can automatically modify hosts and import CA trust.

`~/.claude/settings.json` can stay default; to explicitly declare the official endpoint:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.anthropic.com"
  }
}
```

#### Tunnel Mode

Tunnel Mode does not modify hosts; it relies on `HTTPS_PROXY` and `NODE_EXTRA_CA_CERTS`. It suits scenarios where host privileges are limited, or you only want clients that support proxy environment variables to work.

`~/.claude/settings.json` example:

```json
{
  "env": {
    "HTTPS_PROXY": "https://127.0.0.1:443",
    "NODE_EXTRA_CA_CERTS": "/path/to/magic-claude-code/data/ca.crt"
  }
}
```

Save and restart Claude Code.

#### Gateway Mode

Gateway Mode only covers clients that honor `ANTHROPIC_BASE_URL`; it cannot intercept hardcoded `api.anthropic.com` requests. It is the lowest-privilege fallback and does not depend on hosts or the system CA trust store.

`~/.claude/settings.json` example:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:17487"
  }
}
```

Save and restart Claude Code.

The proxy automatically maps `claude-sonnet-4` to the provider-configured model (such as `glm-5`).

## ✅ First-Run Checklist

| Step | Automatic | Notes |
|------|-----------|-------|
| Generate CA certificate | ✅ | Auto-generated to `data/ca.crt` on startup |
| Modify hosts | Attempted | Requires admin privileges; falls back on failure |
| Install CA trust | Attempted | Requires admin privileges; falls back on failure |
| Persist MCC root | Attempted | Writes to shell profile or Windows environment variable |
| Start proxy | ✅ | Starts even if bootstrap fails |

**What you should see in the logs:**
- `CA certificate: /path/to/data/ca.crt`
- `[Bootstrap]`-prefixed bootstrap result messages
- On bootstrap success: `Transparent mode configured`
- On bootstrap failure: fallback guidance and manual commands

## 🔧 Troubleshooting

### Insufficient Privileges

If logs show hosts modification or CA installation failure:

1. Restart the program with administrator privileges (`sudo` or Windows admin PowerShell)
2. Or follow the log guidance to run the commands manually
3. Or fall back to Tunnel Mode / Gateway Mode

### Docker Limitations

A Docker container **cannot** directly modify the host machine's hosts file or CA trust store. Logs distinguish between:
- `helper missing`: no host-side helper in the container (`MCC_HOST_HELPER` not set)
- `host permission denied`: helper exists but cannot obtain host privileges

For Docker scenarios, it is recommended to:
- Configure hosts and CA trust manually on the host
- Or use Tunnel Mode (set `HTTPS_PROXY`)

### Docker Host Helper Usage

If you want Docker to automatically attempt host modification, prepare a **host-side executable helper**, mount it into the container, and point `MCC_HOST_HELPER` at the absolute path inside the container.

The helper's responsibilities are simple:

- On `hosts add api.anthropic.com 127.0.0.1`, add the host mapping on the host
- On `trust install /app/data/ca.crt`, install that CA into the host trust store
- Return `0` on success, non-zero on failure

The helper does not need to be provided by MCC — MCC only invokes it. You can implement it as a shell script, a small Go tool, or any executable, as long as the container can execute it directly.

Minimal mount example:

```yaml
services:
  mcc:
    volumes:
      - ./data:/app/data
      - /opt/mcc/mcc-host-helper.sh:/host-helper/mcc-host-helper.sh:ro
    environment:
      - MCC_HOST_HELPER=/host-helper/mcc-host-helper.sh
```

The helper file on the host must:

- Be at an absolute path
- Be a regular executable file
- Have the privileges required by your host's permission model to actually perform hosts / CA modifications

A minimal skeleton for Linux / macOS:

```bash
#!/usr/bin/env bash
set -euo pipefail

case "${1:-}" in
  hosts)
    # e.g.: hosts add api.anthropic.com 127.0.0.1
    ;;
  trust)
    # e.g.: trust install /app/data/ca.crt
    ;;
  *)
    echo "usage: $0 {hosts|trust} ..." >&2
    exit 2
    ;;
esac
```

If the helper fails, MCC does not pretend the host was modified; it continues startup and prints Transparent/Tunnel/Gateway fallback guidance.

### Manual CA Import

If automatic installation fails, import manually:

```bash
# macOS
sudo security add-trusted-cert -d -r trustRoot \
  -k /Library/Keychains/System.keychain /path/to/data/ca.crt

# Linux (Debian/Ubuntu)
sudo cp /path/to/data/ca.crt /usr/local/share/ca-certificates/mcc-proxy-ca.crt
sudo update-ca-certificates

# Windows (admin PowerShell)
Import-Certificate -FilePath "C:\mcc\data\ca.crt" -CertStoreLocation Cert:\LocalMachine\Root
```

## 🔌 Ports

| Port | Purpose |
|------|---------|
| 443 | Proxy entry |
| 8442 | Configuration page |

## 📂 Project Structure

```text
magic-claude-code/
├── cmd/server/          # Application entry
├── internal/
│   ├── config/          # Configuration management
│   ├── cert/            # Certificate management
│   ├── proxy/           # Proxy service
│   ├── admin/           # Configuration service
│   ├── bootstrap/       # Startup bootstrap (hosts/CA/env auto-config)
│   ├── i18n/            # Localization messages
│   └── frontend/        # Frontend page
├── data/                # Data directory
├── Dockerfile
└── docker-compose.yml
```

## 🛠️ Development

```bash
# Run tests
make test

# Run locally
make run

# Build
make build
```

## 📄 License

MIT License
