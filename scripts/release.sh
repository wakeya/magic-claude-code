#!/usr/bin/env bash
set -euo pipefail

# ===== 用法 =====
#   ./release.sh <tag>          # 例: ./release.sh v0.6.0
#
# 前置条件:
#   - 已设置 GITEE_TOKEN 和 GITCODE_TOKEN 环境变量
#   - sdd-docs/changes/release-notes/<tag>.md 存在（作为 Release 说明）
#   - git remotes: gitee, gitcode, gitlab, origin 已配置

TAG="${1:?用法: ./release.sh <tag>  例: ./release.sh v0.6.0}"
PRODUCT_NAME="Magic-Claude-Code"
BINARY_NAME="mcc"
GITEE_REMOTE="${GITEE_REMOTE:-gitee}"
GITCODE_REMOTE="${GITCODE_REMOTE:-gitcode}"
GITLAB_REMOTE="${GITLAB_REMOTE:-gitlab}"
GITEE_REPO="wakeya/magic-claude-code"
GITCODE_REPO="wakeya/magic-claude-code"
GITLAB_URL="${GITLAB_URL:-http://git.wakeya.top:56080}"
GITLAB_PROJECT_ID="wakeya%2Fmagic-claude-code"
RELEASE_NOTES="sdd-docs/changes/release-notes/${TAG}.md"
BUILD_DIR=$(mktemp -d)

GITHUB_OWNER="wakeya"
GITHUB_REPO="magic-claude-code"
GITHUB_DL_BASE="https://github.com/${GITHUB_OWNER}/${GITHUB_REPO}/releases/download/${TAG}"

# ===== 颜色输出 =====
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
info()  { echo -e "${GREEN}▶${NC} $*"; }
warn()  { echo -e "${YELLOW}⚠${NC} $*"; }
error() { echo -e "${RED}✗${NC} $*" >&2; }

# ===== 检查 =====
if [[ ! "$TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  error "Tag 格式应为 vX.Y.Z，当前: $TAG"
  exit 1
fi

if ! git rev-parse "$TAG" &>/dev/null; then
  error "Tag $TAG 不存在，请先创建并推送: git tag $TAG && git push origin $TAG"
  exit 1
fi

if ! command -v jq &>/dev/null; then
  error "需要 jq 命令，请先安装"
  exit 1
fi

check_token() {
  local name="$1"
  if [ -z "${!name:-}" ]; then
    warn "$name 未设置，将跳过对应平台的 Release 创建"
  fi
}
check_token GITEE_TOKEN
check_token GITCODE_TOKEN

if [ ! -f "$RELEASE_NOTES" ]; then
  warn "未找到 $RELEASE_NOTES，将使用默认说明"
  RELEASE_BODY="${TAG} release."
else
  info "从 $RELEASE_NOTES 读取发布说明"
  RELEASE_BODY=$(cat "$RELEASE_NOTES")
fi

info "发布 $TAG"

# ===== [1/8] 同步 main 分支 =====
info "[1/8] 同步 main 分支"
git fetch origin main
git checkout main
git pull origin main

# ===== [2/8] 构建前端 =====
info "[2/8] 构建前端"
npm ci --prefix internal/frontend
npm run build --prefix internal/frontend

# ===== [3/8] 运行测试 =====
info "[3/8] 运行测试"
go test ./...

# ===== [4/8] 构建二进制 =====
info "[4/8] 构建跨平台二进制"

build_target() {
  local goos="$1" goarch="$2" platform="$3" arch_label="$4" format="$5"
  local exe_suffix=""
  [ "$goos" = "windows" ] && exe_suffix=".exe"

  local pkg="${PRODUCT_NAME}-${TAG}-${platform}-${arch_label}"
  local pkg_dir="${BUILD_DIR}/${pkg}"
  mkdir -p "$pkg_dir"

  GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 \
    go build -trimpath -ldflags="-s -w -X magic-claude-code/internal/version.Version=${TAG}" \
    -o "${pkg_dir}/${BINARY_NAME}${exe_suffix}" ./cmd/server

  cp README.md "$pkg_dir/README.md"

  if [ "$format" = "zip" ]; then
    (cd "$BUILD_DIR" && zip -qr "${pkg}.zip" "$pkg")
  else
    tar -C "$BUILD_DIR" -czf "${BUILD_DIR}/${pkg}.tar.gz" "$pkg"
  fi
  rm -rf "$pkg_dir"
}

build_target linux   amd64 Linux   x86_64 tar.gz
build_target linux   arm64 Linux   arm64  tar.gz
build_target darwin  amd64 macOS   x86_64 tar.gz
build_target darwin  arm64 macOS   arm64  tar.gz
build_target windows amd64 Windows x86_64 zip
build_target windows arm64 Windows arm64  zip

(cd "$BUILD_DIR" && sha256sum * > SHA256SUMS.txt)

info "产物清单:"
ls -lh "$BUILD_DIR/"

# ===== [5/8] 推送代码和 tag =====
info "[5/8] 推送代码和 tag"
for remote in "$GITEE_REMOTE" "$GITCODE_REMOTE" "$GITLAB_REMOTE"; do
  if git remote get-url "$remote" &>/dev/null; then
    info "推送 $remote main..."
    git push "$remote" main || warn "推送 $remote main 失败"
    git push "$remote" "$TAG" 2>/dev/null || warn "推送 $remote tag 失败（可能已存在）"
  else
    warn "远程 $remote 不存在，跳过"
  fi
done

# ===== [6/8] Gitee Release + 附件上传 =====
info "[6/8] Gitee Release + 附件上传"
if [ -n "${GITEE_TOKEN:-}" ]; then
  # 创建或获取 Release
  GITEE_RELEASE_ID=$(curl -sf \
    -H "Authorization: Bearer ${GITEE_TOKEN}" \
    "https://gitee.com/api/v5/repos/${GITEE_REPO}/releases/tags/${TAG}" \
    2>/dev/null | jq -r '.id // empty') || true

  if [ -n "$GITEE_RELEASE_ID" ]; then
    info "Gitee Release 已存在 (id=${GITEE_RELEASE_ID})"
  else
    STATUS=$(curl -s -o /tmp/gitee-release.json -w "%{http_code}" \
      -X POST \
      -H "Authorization: Bearer ${GITEE_TOKEN}" \
      -H "Content-Type: application/json" \
      -d "$(jq -n --arg tag "$TAG" --arg body "$RELEASE_BODY" \
        '{tag_name:$tag, name:$tag, body:$body, target_commitish:"main"}')" \
      "https://gitee.com/api/v5/repos/${GITEE_REPO}/releases")

    if [ "$STATUS" -ge 200 ] && [ "$STATUS" -lt 300 ]; then
      GITEE_RELEASE_ID=$(jq -r '.id' /tmp/gitee-release.json)
      info "Gitee Release 创建成功 (id=${GITEE_RELEASE_ID})"
    else
      error "Gitee Release 创建失败 (HTTP ${STATUS})"
      cat /tmp/gitee-release.json >&2 2>/dev/null || true
    fi
  fi

  # 上传附件
  if [ -n "$GITEE_RELEASE_ID" ]; then
    info "上传 Gitee 附件..."
    for f in "$BUILD_DIR"/*; do
      fname=$(basename "$f")
      STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
        -X POST \
        -H "Authorization: Bearer ${GITEE_TOKEN}" \
        -F "file=@${f}" \
        "https://gitee.com/api/v5/repos/${GITEE_REPO}/releases/${GITEE_RELEASE_ID}/attach_files")

      if [ "$STATUS" -ge 200 ] && [ "$STATUS" -lt 300 ]; then
        info "  ✓ ${fname}"
      else
        warn "  ✗ ${fname} (HTTP ${STATUS})"
      fi
    done
  fi
else
  warn "GITEE_TOKEN 未设置，跳过"
fi

# ===== [7/8] GitCode Release + 附件上传 =====
info "[7/8] GitCode Release + 附件上传"
if [ -n "${GITCODE_TOKEN:-}" ]; then
  # 创建或获取 Release
  GITCODE_RELEASE_ID=$(curl -sf \
    -H "PRIVATE-TOKEN: ${GITCODE_TOKEN}" \
    "https://api.gitcode.com/api/v5/repos/${GITCODE_REPO}/releases/tags/${TAG}" \
    2>/dev/null | jq -r '.id // empty') || true

  if [ -n "$GITCODE_RELEASE_ID" ]; then
    info "GitCode Release 已存在 (id=${GITCODE_RELEASE_ID})"
  else
    STATUS=$(curl -s -o /tmp/gitcode-release.json -w "%{http_code}" \
      -X POST \
      -H "PRIVATE-TOKEN: ${GITCODE_TOKEN}" \
      -H "Content-Type: application/json" \
      -d "$(jq -n --arg tag "$TAG" --arg body "$RELEASE_BODY" \
        '{tag_name:$tag, name:$tag, body:$body, target_commitish:"main"}')" \
      "https://api.gitcode.com/api/v5/repos/${GITCODE_REPO}/releases")

    if [ "$STATUS" -ge 200 ] && [ "$STATUS" -lt 300 ]; then
      GITCODE_RELEASE_ID=$(jq -r '.id' /tmp/gitcode-release.json)
      info "GitCode Release 创建成功 (id=${GITCODE_RELEASE_ID})"
    else
      warn "GitCode Release 创建返回 ${STATUS}"
      cat /tmp/gitcode-release.json >&2 2>/dev/null || true
    fi
  fi

  # 上传附件（两步式：获取 OBS 预签名 URL → PUT 上传）
  if [ -n "$GITCODE_RELEASE_ID" ]; then
    info "上传 GitCode 附件..."
    for f in "$BUILD_DIR"/*; do
      fname=$(basename "$f")

      # Step 1: 获取 OBS 预签名上传 URL
      UPLOAD_URL=$(curl -sf \
        -H "PRIVATE-TOKEN: ${GITCODE_TOKEN}" \
        "https://api.gitcode.com/api/v5/repos/${GITCODE_REPO}/releases/${GITCODE_RELEASE_ID}/attach_files?file_name=${fname}" \
        2>/dev/null | jq -r '.upload_url // empty') || true

      if [ -z "$UPLOAD_URL" ]; then
        warn "  ✗ ${fname}: 无法获取上传 URL（可能已存在）"
        continue
      fi

      # Step 2: PUT 文件到 OBS
      STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
        -X PUT \
        -H "Content-Type: application/octet-stream" \
        --data-binary "@${f}" \
        "$UPLOAD_URL")

      if [ "$STATUS" -ge 200 ] && [ "$STATUS" -lt 300 ]; then
        info "  ✓ ${fname}"
      else
        warn "  ✗ ${fname} (HTTP ${STATUS})"
      fi
    done
  fi
else
  warn "GITCODE_TOKEN 未设置，跳过"
fi

# ===== [8/8] GitLab Release + 链接 =====
info "[8/8] GitLab Release + 下载链接"
if git remote get-url "$GITLAB_REMOTE" &>/dev/null; then
  ASSETS_JSON="[]"
  for f in "$BUILD_DIR"/*; do
    fname=$(basename "$f")
    ASSETS_JSON=$(echo "$ASSETS_JSON" | jq \
      --arg name "$fname" \
      --arg url "${GITHUB_DL_BASE}/${fname}" \
      '. + [{name: $name, url: $url, link_type: "other"}]')
  done

  STATUS=$(curl -s -o /tmp/gitlab-release.json -w "%{http_code}" \
    --noproxy '*' \
    -k \
    -X POST \
    -H "Content-Type: application/json" \
    -d "$(jq -n \
      --arg tag "$TAG" \
      --arg name "$TAG" \
      --arg body "$RELEASE_BODY" \
      --argjson assets "$ASSETS_JSON" \
      '{tag_name:$tag, name:$name, description:$body, assets:{links:$assets}}')" \
    "${GITLAB_URL}/api/v4/projects/${GITLAB_PROJECT_ID}/releases")

  if [ "$STATUS" -ge 200 ] && [ "$STATUS" -lt 300 ]; then
    info "GitLab Release 创建成功（链接指向 GitHub 下载）"
  elif [ "$STATUS" = "409" ]; then
    info "GitLab Release 已存在，跳过"
  else
    warn "GitLab Release 返回 ${STATUS}"
    cat /tmp/gitlab-release.json >&2 2>/dev/null || true
  fi
else
  warn "远程 ${GITLAB_REMOTE} 不存在，跳过 GitLab Release"
fi

info "清理构建目录"
rm -rf "$BUILD_DIR"

# ===== 完成 =====
info "${TAG} 发布完成"
