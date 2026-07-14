#!/usr/bin/env bash
set -euo pipefail

# ================================================================
# MCC Proxy 宿主机一键配置脚本 | MCC Proxy Host Setup Script
# ================================================================
#
# 自动完成透明模式所需的宿主机配置：
#   1. 在 /etc/hosts 中添加 api.anthropic.com → 127.0.0.1
#   2. 将 MCC 生成的 CA 证书安装到系统信任库
#
# 用法 | Usage:
#   sudo ./setup-host.sh                          # 一键完成 hosts + CA
#   sudo ./setup-host.sh hosts                    # 只配置 hosts
#   sudo ./setup-host.sh trust                    # 只安装 CA
#   sudo ./setup-host.sh --cert /path/ca.crt      # 指定 CA 路径
#   sudo ./setup-host.sh --domain api.x.com       # 自定义域名
#
# Helper 接口（供 MCC_HOST_HELPER 调用）:
#   ./setup-host.sh hosts add <domain> <ip>
#   ./setup-host.sh trust install <cert_path>
#
# ================================================================

TARGET_DOMAIN="api.anthropic.com"
TARGET_IP="127.0.0.1"
ACTION="all"
CERT_PATH=""
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

# ─── 颜色 ───
if [[ -t 1 ]]; then
    C_GREEN='\033[0;32m'
    C_YELLOW='\033[0;33m'
    C_RED='\033[0;31m'
    C_NC='\033[0m'
else
    C_GREEN='' C_YELLOW='' C_RED='' C_NC=''
fi

info()  { printf "${C_GREEN}✓${C_NC} %s\n" "$*"; }
warn()  { printf "${C_YELLOW}!${C_NC} %s\n" "$*"; }
error() { printf "${C_RED}✗${C_NC} %s\n" "$*" >&2; }
fatal() { error "$*"; exit 1; }

# ─── 配置标记（供 Docker 容器内的 helper 检测宿主机状态）───
# setup-host.sh / setup-host.ps1 成功后写入 data/ 目录，
# docker-host-helper.sh 通过这些标记判断透明模式是否就绪。
# CA 标记额外写入证书 SHA256 fingerprint，bootstrap 据此检测证书轮换。
write_marker() {
    local name="$1"
    local cert="${2:-}"
    local data_dir="$PROJECT_DIR/data"
    mkdir -p "$data_dir" 2>/dev/null || return 0
    local marker="$data_dir/.$name"
    safe_marker_path "$marker" || return 0
    local fp_field=""
    if [[ "$name" == "ca-trust-installed" && -n "$cert" && -f "$cert" ]]; then
        local fp
        if command -v sha256sum >/dev/null 2>&1; then
            fp=$(sha256sum "$cert" | awk '{print $1}')
        elif command -v shasum >/dev/null 2>&1; then
            fp=$(shasum -a 256 "$cert" | awk '{print $1}')
        fi
        [[ -n "$fp" ]] && fp_field="\"fingerprint\":\"$fp\","
    fi
    local tmp
    tmp=$(mktemp "$data_dir/.$name.tmp.XXXXXX" 2>/dev/null) || return 0
    printf '{"action":"%s",%s"os":"%s","timestamp":"%s"}\n' \
        "$name" "$fp_field" "$OS_TYPE" "$(date -Iseconds 2>/dev/null || date)" \
        > "$tmp" 2>/dev/null || { rm -f "$tmp"; return 0; }
    chmod 0644 "$tmp" 2>/dev/null || true
    mv -f "$tmp" "$marker" 2>/dev/null || rm -f "$tmp"
}

safe_marker_path() {
    local path="$1"
    if [[ -e "$path" || -L "$path" ]]; then
        [[ -L "$path" ]] && return 1
        [[ -f "$path" ]] || return 1
    fi
    return 0
}

# ─── 参数解析 ───
ORIGINAL_ARGS=("$@")
while [[ $# -gt 0 ]]; do
    case "$1" in
        all|hosts|trust) ACTION="$1"; shift ;;
        --cert) CERT_PATH="$2"; shift 2 ;;
        --domain) TARGET_DOMAIN="$2"; shift 2 ;;
        --ip) TARGET_IP="$2"; shift 2 ;;
        --help|-h)
            head -22 "$0" | tail -20
            exit 0
            ;;
        *)
            # helper 接口: hosts add <domain> <ip> | trust install <cert>
            if [[ "$1" == "add" ]]; then
                ACTION="hosts"; TARGET_DOMAIN="$2"; TARGET_IP="$3"; shift 3; continue
            fi
            if [[ "$1" == "install" ]]; then
                ACTION="trust"; CERT_PATH="$2"; shift 2; continue
            fi
            # 单个未知参数且 ACTION 已被前一个参数设为 hosts/trust 时，跳过
            if [[ "$ACTION" == "hosts" || "$ACTION" == "trust" ]] && [[ -z "${2:-}" ]]; then
                shift; continue
            fi
            fatal "未知参数: $1（用 --help 查看用法）"
            ;;
    esac
done

# ─── OS 检测 ───
detect_os() {
    case "$(uname -s)" in
        Darwin) echo "macos" ;;
        Linux)
            if [[ -f /etc/os-release ]]; then
                . /etc/os-release 2>/dev/null || true
                case "${ID:-}" in
                    debian|ubuntu|linuxmint|pop) echo "debian" ;;
                    centos|rhel|fedora|rocky|alma|amzn|ol) echo "rhel" ;;
                    alpine) echo "alpine" ;;
                    arch|manjaro|endeavouros) echo "arch" ;;
                    opensuse*|suse|sles) echo "suse" ;;
                    *) echo "linux" ;;
                esac
            else
                echo "linux"
            fi
            ;;
        MINGW*|MSYS*|CYGWIN*) echo "windows" ;;
        *) echo "unknown" ;;
    esac
}

OS_TYPE="$(detect_os)"

# ─── 权限检查 ───
ensure_privileges() {
    if [[ "$OS_TYPE" == "macos" ]]; then
        # macOS: 需要 sudo 来改 hosts 和 keychain
        if [[ $EUID -ne 0 ]] && ! sudo -n true 2>/dev/null; then
            warn "需要管理员权限，系统将提示输入密码。"
        fi
    elif [[ "$OS_TYPE" == "windows" ]]; then
        : # Windows (Git Bash/MSYS): 需要管理员权限的命令由脚本内的 net session 检测
    else
        if [[ $EUID -ne 0 ]]; then
            info "需要 root 权限，正在通过 sudo 重新运行..."
            exec sudo -E bash "$0" "${ORIGINAL_ARGS[@]}"
        fi
    fi
}

# ─── CA 证书查找 ───
find_cert() {
    if [[ -n "$CERT_PATH" && -f "$CERT_PATH" ]]; then
        echo "$CERT_PATH"; return 0
    fi
    local candidates=(
        "$PROJECT_DIR/data/ca.crt"
        "$SCRIPT_DIR/../data/ca.crt"
        "$SCRIPT_DIR/data/ca.crt"
        "./data/ca.crt"
        "./ca.crt"
    )
    for c in "${candidates[@]}"; do
        if [[ -f "$c" ]]; then
            echo "$c"; return 0
        fi
    done
    return 1
}

find_system_ca_bundle() {
    local candidates=(
        "/etc/ssl/certs/ca-certificates.crt"
        "/etc/pki/tls/certs/ca-bundle.crt"
        "/etc/ssl/ca-bundle.pem"
        "/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem"
    )
    local c
    for c in "${candidates[@]}"; do
        if [[ -f "$c" ]]; then
            echo "$c"; return 0
        fi
    done
    return 1
}

# ─── hosts 配置 ───
setup_hosts() {
    local hosts_file="/etc/hosts"
    [[ "$OS_TYPE" == "windows" ]] && hosts_file="C:/Windows/System32/drivers/etc/hosts"

    info "配置 hosts: $TARGET_IP → $TARGET_DOMAIN"

    # 检查已有映射
    if grep -qE "^[^#]*[[:space:]]+${TARGET_DOMAIN//./\\.}\b" "$hosts_file" 2>/dev/null; then
        local existing
        existing=$(grep -E "^[^#]*[[:space:]]+${TARGET_DOMAIN//./\\.}\b" "$hosts_file" | awk '{print $1}' | head -1)
        if [[ "$existing" == "$TARGET_IP" ]]; then
            info "hosts 已包含正确映射，跳过。"
            write_marker "hosts-configured"
            return 0
        fi
        warn "hosts 中已有 $TARGET_DOMAIN → $existing，更新为 $TARGET_IP"
        local tmp; tmp=$(mktemp)
        grep -vE "^[^#]*[[:space:]]+${TARGET_DOMAIN//./\\.}\b" "$hosts_file" > "$tmp" || true
        cat "$tmp" > "$hosts_file"
        rm -f "$tmp"
    fi

    if [[ "$OS_TYPE" == "macos" ]]; then
        echo "$TARGET_IP $TARGET_DOMAIN" | sudo tee -a "$hosts_file" >/dev/null
    else
        echo "$TARGET_IP $TARGET_DOMAIN" >> "$hosts_file"
    fi
    info "hosts 配置完成。"
    write_marker "hosts-configured"
}

# ─── 包安装（含国内镜像 fallback）───

apt_install_with_mirror_fallback() {
    local package="$1"
    info "通过 apt 安装 $package ..."
    if apt-get install -y --no-install-recommends "$package" 2>/dev/null; then
        return 0
    fi
    warn "默认源安装失败，切换阿里云镜像重试..."
    local src="/etc/apt/sources.list"
    local bak="$src.mcc-backup"
    [[ ! -f "$bak" ]] && cp "$src" "$bak"
    sed -i.bak \
        -e 's|deb\.debian\.org|mirrors.aliyun.com|g' \
        -e 's|security\.debian\.org|mirrors.aliyun.com|g' \
        -e 's|archive\.ubuntu\.com|mirrors.aliyun.com|g' \
        -e 's|security\.ubuntu\.com|mirrors.aliyun.com|g' \
        "$src" 2>/dev/null || true
    apt-get update -y 2>/dev/null || true
    local ok=1
    apt-get install -y --no-install-recommends "$package" || ok=0
    # 恢复原始源
    cp "$bak" "$src"
    rm -f "${src}.bak"
    return $ok
}

yum_install_with_mirror_fallback() {
    local package="$1"
    local mgr="yum"
    command -v dnf &>/dev/null && mgr="dnf"
    info "通过 $mgr 安装 $package ..."
    if $mgr install -y "$package" 2>/dev/null; then
        return 0
    fi
    warn "默认源安装失败，切换阿里云镜像重试..."
    # RHEL/CentOS 镜像替换
    if [[ -d /etc/yum.repos.d ]]; then
        local bak_dir="/etc/yum.repos.d.mcc-backup"
        [[ ! -d "$bak_dir" ]] && cp -r /etc/yum.repos.d "$bak_dir"
        for repo in /etc/yum.repos.d/*.repo; do
            [[ -f "$repo" ]] || continue
            sed -i.bak \
                -e 's|mirror\.centos\.org|mirrors.aliyun.com|g' \
                -e 's|download\.fedaproject\.org|mirrors.aliyun.com|g' \
                -e 's|//dl\.fedoraproject\.org|//mirrors.aliyun.com|g' \
                -e 's|vault\.centos\.org|mirrors.aliyun.com|g' \
                -e 's|repo\.almalinux\.org|mirrors.aliyun.com|g' \
                -e 's|download\.rockylinux\.org|mirrors.aliyun.com|g' \
                "$repo" 2>/dev/null || true
        done
        $mgr clean all 2>/dev/null || true
        $mgr makecache 2>/dev/null || true
        local ok=1
        $mgr install -y "$package" || ok=0
        # 恢复原始 repo
        rm -f /etc/yum.repos.d/*.repo
        cp "$bak_dir"/*.* /etc/yum.repos.d/ 2>/dev/null || true
        rm -rf "$bak_dir"
        rm -f /etc/yum.repos.d/*.bak
        return $ok
    fi
    return 1
}

ensure_ca_tool() {
    case "$OS_TYPE" in
        debian)
            if command -v update-ca-certificates &>/dev/null; then return 0; fi
            info "缺少 update-ca-certificates，需要安装 ca-certificates 包。"
            apt-get update -y 2>/dev/null || true
            apt_install_with_mirror_fallback ca-certificates || \
                fatal "无法安装 ca-certificates，请手动运行: apt-get install ca-certificates"
            ;;
        rhel)
            if command -v update-ca-trust &>/dev/null; then return 0; fi
            info "缺少 update-ca-trust，需要安装 ca-certificates 包。"
            yum_install_with_mirror_fallback ca-certificates || \
                fatal "无法安装 ca-certificates，请手动运行: yum install ca-certificates"
            ;;
        alpine)
            if command -v update-ca-certificates &>/dev/null; then return 0; fi
            info "安装 ca-certificates ..."
            if ! apk add --no-cache ca-certificates 2>/dev/null; then
                warn "默认源失败，切换阿里云镜像..."
                local bak="/etc/apk/repositories.mcc-backup"
                [[ ! -f "$bak" ]] && cp /etc/apk/repositories "$bak"
                sed -i 's|dl-cdn\.alpinelinux\.org|mirrors.aliyun.com|g' /etc/apk/repositories
                apk update 2>/dev/null || true
                apk add --no-cache ca-certificates || {
                    cp "$bak" /etc/apk/repositories
                    fatal "无法安装 ca-certificates"
                }
                cp "$bak" /etc/apk/repositories
            fi
            ;;
        suse)
            if command -v update-ca-certificates &>/dev/null; then return 0; fi
            info "安装 ca-certificates ..."
            if ! zypper install -y ca-certificates 2>/dev/null; then
                warn "默认源失败，切换阿里云镜像..."
                local bak="/etc/zypp/repos.d.mcc-backup"
                [[ ! -d "$bak" ]] && mkdir -p "$bak" && cp /etc/zypp/repos.d/*.repo "$bak/" 2>/dev/null || true
                for repo in /etc/zypp/repos.d/*.repo; do
                    [[ -f "$repo" ]] || continue
                    sed -i.bak \
                        -e 's|download\.opensuse\.org|mirrors.aliyun.com|g' \
                        -e 's|download\.suse\.com|mirrors.aliyun.com|g' \
                        "$repo" 2>/dev/null || true
                done
                zypper --gpg-auto-import-keys refresh 2>/dev/null || true
                zypper install -y ca-certificates || fatal "无法安装 ca-certificates"
                cp "$bak"/*.repo /etc/zypp/repos.d/ 2>/dev/null || true
                rm -rf "$bak" /etc/zypp/repos.d/*.bak
            fi
            ;;
        arch)
            if command -v update-ca-trust &>/dev/null; then return 0; fi
            info "安装 ca-certificates ..."
            if ! pacman -Sy --noconfirm ca-certificates 2>/dev/null; then
                warn "默认源失败，切换清华镜像..."
                local bak="/etc/pacman.d/mirrorlist.mcc-backup"
                [[ ! -f "$bak" ]] && cp /etc/pacman.d/mirrorlist "$bak"
                echo 'Server = https://mirrors.tuna.tsinghua.edu.cn/archlinux/$repo/os/$arch' > /etc/pacman.d/mirrorlist
                pacman -Sy --noconfirm ca-certificates || fatal "无法安装 ca-certificates"
                cp "$bak" /etc/pacman.d/mirrorlist
            fi
            ;;
        macos)
            if command -v security &>/dev/null; then return 0; fi
            fatal "macOS 缺少 security 命令，请确保系统完整或安装 Xcode Command Line Tools: xcode-select --install"
            ;;
    esac
}

# ─── CA 信任安装 ───
setup_trust() {
    local cert; cert=$(find_cert) || fatal "找不到 CA 证书文件。请用 --cert /path/to/ca.crt 指定路径。"
    info "安装 CA 证书: $cert"
    [[ -f "$cert" ]] || fatal "证书文件不存在: $cert"

    ensure_ca_tool

    case "$OS_TYPE" in
        debian)
            local dest="/usr/local/share/ca-certificates/mcc-proxy-ca.crt"
            cp "$cert" "$dest"
            chmod 644 "$dest"
            update-ca-certificates
            info "CA 已安装到 Debian/Ubuntu 系统信任库。"
            ;;
        rhel)
            local dest="/etc/pki/ca-trust/source/anchors/mcc-proxy-ca.pem"
            cp "$cert" "$dest"
            chmod 644 "$dest"
            update-ca-trust extract
            info "CA 已安装到 RHEL/CentOS/Fedora 系统信任库。"
            ;;
        alpine)
            local dest="/usr/local/share/ca-certificates/mcc-proxy-ca.crt"
            cp "$cert" "$dest"
            chmod 644 "$dest"
            update-ca-certificates
            info "CA 已安装到 Alpine 系统信任库。"
            ;;
        suse)
            local dest="/etc/pki/trust/anchors/mcc-proxy-ca.pem"
            cp "$cert" "$dest"
            chmod 644 "$dest"
            update-ca-certificates
            info "CA 已安装到 openSUSE/SLES 系统信任库。"
            ;;
        arch)
            local dest="/etc/ca-certificates/trust-source/mcc-proxy-ca.crt"
            mkdir -p "$(dirname "$dest")"
            cp "$cert" "$dest"
            chmod 644 "$dest"
            update-ca-trust
            info "CA 已安装到 Arch Linux 系统信任库。"
            ;;
        macos)
            sudo security add-trusted-cert -d -r trustRoot \
                -k /Library/Keychains/System.keychain "$cert"
            info "CA 已安装到 macOS 系统钥匙串。"
            ;;
        windows)
            certutil -addstore -f ROOT "$cert"
            info "CA 已安装到 Windows 根证书存储。"
            ;;
        *)
            fatal "不支持的操作系统: $OS_TYPE。请参考 README 手动安装 CA。"
            ;;
    esac

    write_marker "ca-trust-installed" "$cert"
    info "提示: 如果使用 Node.js (Claude Code)，还需设置环境变量:"
    echo "  export NODE_EXTRA_CA_CERTS=$(cd "$(dirname "$cert")" && pwd)/$(basename "$cert")"
    if [[ "$OS_TYPE" != "macos" && "$OS_TYPE" != "windows" ]]; then
        local bundle
        if bundle=$(find_system_ca_bundle); then
            echo "  export SSL_CERT_FILE=$bundle"
            echo "  # SSL_CERT_FILE 必须指向完整系统 CA bundle，不能指向单个 data/ca.crt"
        else
            warn "未找到常见完整系统 CA bundle；请按 README 手动设置 SSL_CERT_FILE。"
        fi
    fi
}

# ─── 主逻辑 ───
main() {
    info "MCC Proxy 宿主机配置 | OS: $OS_TYPE"
    echo ""

    case "$ACTION" in
        hosts)
            ensure_privileges
            setup_hosts
            ;;
        trust)
            ensure_privileges
            setup_trust
            ;;
        all)
            ensure_privileges
            setup_hosts
            echo ""
            setup_trust
            echo ""
            info "全部配置完成！现在可以启动 Claude Code 了。"
            ;;
    esac
}

main
