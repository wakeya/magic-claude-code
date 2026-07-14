# Release Package Scripts

Prefer starting `mcc` / `mcc.exe` directly and let the built-in bootstrap configure hosts, system CA trust, and client environment variables. The scripts below are for bootstrap recovery, Docker host pre-configuration, Windows background/autostart operation, or standalone hosts/CA setup.

## Linux / macOS

The release package includes:

- `setup-host.sh`
- `docker-host-helper.sh`

Configure hosts and CA trust:

```bash
sudo ./setup-host.sh
```

Configure hosts only:

```bash
sudo ./setup-host.sh hosts
```

Install CA only:

```bash
sudo ./setup-host.sh trust
```

After CA installation, the script prints client environment guidance. On Linux, `SSL_CERT_FILE` must point to the full system CA bundle, for example `/etc/ssl/certs/ca-certificates.crt`; do not point it at the single `data/ca.crt`.

`docker-host-helper.sh` is for Docker scenarios. Run `setup-host.sh` on the host first, then mount the helper into the container and set `MCC_HOST_HELPER` to it so the container can check host hosts/CA state.

## Windows

The release package includes:

- `setup-host.ps1`
- `start-mcc.ps1`
- `stop-mcc.ps1`
- `register-mcc-task.ps1`

Configure hosts and CA trust from an Administrator PowerShell:

```powershell
.\setup-host.ps1
```

Configure hosts only:

```powershell
.\setup-host.ps1 -Action hosts
```

Install CA only:

```powershell
.\setup-host.ps1 -Action trust
```

Start / stop in the background:

```powershell
.\start-mcc.ps1
.\stop-mcc.ps1
```

Register autostart when the current user logs in:

```powershell
.\register-mcc-task.ps1 -Force
```

By default no fixed admin password is configured; `mcc.exe` generates one and writes it to the stdout log. If you pass `-Password`, the password is stored in scheduled-task arguments and can be read by local administrators.
