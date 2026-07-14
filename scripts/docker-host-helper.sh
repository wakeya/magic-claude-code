#!/bin/sh
# ================================================================
# Docker 宿主机配置状态检测脚本 | Docker Host Setup State Detector
# ================================================================
#
# 此脚本在容器内执行（由 bootstrap 通过 MCC_HOST_HELPER 调用），
# 通过 data 目录的标记文件检测宿主机配置状态。
# 标记文件由 setup-host.sh / setup-host.ps1 成功写入。
#
# bootstrap 调用参数（见 internal/bootstrap/adapters.go）:
#   docker-host-helper.sh hosts add <domain> <ip>
#   docker-host-helper.sh trust install <cert_path>
#
# 返回: 0 = 已配置, 1 = 未配置（bootstrap 将按真实原因降级）
#
# 注意: 此脚本必须是常规文件且权限 perm & 0o022 == 0（555 满足），
# 否则 validateHostHelperPath 会拒绝。
# ================================================================

DATA_DIR="${MCC_DATA_DIR:-/app/data}"

sha256_file() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print tolower($1)}'
        return
    fi
    if command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print tolower($1)}'
        return
    fi
    return 1
}

marker_fingerprint() {
    sed -n 's/.*"fingerprint"[[:space:]]*:[[:space:]]*"\([A-Fa-f0-9]*\)".*/\1/p' "$1" | head -1 | tr 'A-F' 'a-f'
}

case "$1" in
    hosts)
        if [ -f "$DATA_DIR/.hosts-configured" ]; then
            exit 0
        fi
        echo "[helper] hosts not configured on host; run: sudo ./scripts/setup-host.sh hosts" >&2
        exit 1
        ;;
    trust)
        marker="$DATA_DIR/.ca-trust-installed"
        cert="$3"
        if [ -f "$marker" ]; then
            want="$(marker_fingerprint "$marker")"
            if [ -z "$want" ]; then
                echo "[helper] CA trust marker is legacy/stale; run: sudo ./scripts/setup-host.sh trust" >&2
                exit 1
            fi
            if [ ! -f "$cert" ]; then
                echo "[helper] CA cert not found in container: $cert" >&2
                exit 1
            fi
            got="$(sha256_file "$cert" || true)"
            if [ -n "$got" ] && [ "$want" = "$got" ]; then
                exit 0
            fi
            echo "[helper] CA trust marker fingerprint mismatch/stale; run: sudo ./scripts/setup-host.sh trust" >&2
            exit 1
        fi
        echo "[helper] CA not installed on host; run: sudo ./scripts/setup-host.sh trust" >&2
        exit 1
        ;;
    *)
        echo "[helper] unknown command: $*" >&2
        exit 1
        ;;
esac
