#!/usr/bin/env bash
set -euo pipefail

# ===== 用法 =====
#   ./release.sh <tag>          # 例: ./release.sh v0.6.0
#
# 前置条件:
#   - 发布 token 用 gopass 管理（推荐），或临时 export 环境变量（CI 场景）：
#       gopass insert tokens/gitee-release
#       gopass insert tokens/gitcode-release
#       gopass insert tokens/gitlab-release
#     本地切勿把 token 写进 .bashrc——会被所有交互 shell 与 Claude Code/Codex
#     等工具的子进程继承而泄露。
#     脚本启动时的 [预检] 步会自动解密读取全部 token（等同于执行
#     `gopass show tokens/<x>-release`）：若 store 已上锁，会在此提示输入口令
#     完成解锁。token 一次性读入【非导出】变量，之后构建耗时多久都不再依赖
#     gopass 口令缓存（该缓存闲置 10 分钟 / 累计 2 小时会自动失效）。
#     若某条目存在却读不出来（上锁且无法交互输入口令），脚本会明确报错并退出，
#     而非静默跳过——解锁后重跑即可（脚本幂等，已建的 Release 会自动跳过）。
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
GITLAB_PROJECT_ID="${GITLAB_PROJECT_ID:-21}"
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

# ===== Secret 管理（gopass 按需读取，降低泄露）=====
# 三层防护：
#   1) token 不进 .bashrc，且在本脚本内【不 export】——只是非导出 shell 变量，
#      npm/go/git 等子进程拿不到（只有被 export 的变量才会进入子进程 environ）。
#   2) 预检一次性读取：启动时 [预检] 步解密全部 token 到内存，规避"构建耗时
#      超过口令缓存有效期（10 分钟）导致第 6/7/8 步读取失败"；非导出保证子进程不可见。
#   3) 传给 curl 时走 --config 管道，而非 `-H "X: <token>"`，token 不出现在
#      curl 命令行（/proc/<pid>/cmdline 与 ps 中不可见）。
#
# gopass 中的路径（按需修改，须与 `gopass insert <path>` 时一致）：
GOPASS_PATH_GITEE="${GOPASS_PATH_GITEE:-tokens/gitee-release}"
GOPASS_PATH_GITCODE="${GOPASS_PATH_GITCODE:-tokens/gitcode-release}"
GOPASS_PATH_GITLAB="${GOPASS_PATH_GITLAB:-tokens/gitlab-release}"

# load_secret <变量名> <gopass路径>
#   - 已有同名环境变量（CI 注入）→ 沿用；
#   - 未装 gopass 或 gopass 中无此条目 → 静默返回（按"未配置"跳过该平台）；
#   - 条目存在但解密失败（store 上锁且无法交互输入口令）→ 明确报错并返回 1。
# 读取到的值写入同名【非导出】变量。首次解密会触发 gpg-agent 口令提示=解锁。
load_secret() {
  local var="$1" path="$2" val
  if [ -n "${!var:-}" ]; then
    return 0
  fi
  if ! command -v gopass >/dev/null 2>&1; then
    return 0
  fi
  # 仅列名字、不解密，判断条目是否存在（store 上锁时也能列出）
  if ! gopass ls --flat 2>/dev/null | grep -qxF "$path"; then
    return 0
  fi
  if ! val=$(gopass show -o "$path" 2>/dev/null) || [ -z "$val" ]; then
    error "$var: gopass 条目 '$path' 存在但读取失败——store 可能已上锁。"
    error "  请解锁后重跑（脚本幂等）：gopass show $path >/dev/null"
    return 1
  fi
  printf -v "$var" '%s' "$val"
}

# curl_auth <header名> <header值> [curl 参数...]
# 通过 --config 从进程管道传 header，token 不进入 curl 命令行。
# 用法: curl_auth "Authorization" "Bearer ${TOKEN}" -sf "<url>"
curl_auth() {
  local hname="$1" hval="$2"; shift 2
  curl --config <(printf 'header = "%s: %s"\n' "$hname" "$hval") "$@"
}

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

# token 在启动后的 [预检] 步统一读取（见下文），此处不再做 token 检查。

if [ ! -f "$RELEASE_NOTES" ]; then
  warn "未找到 $RELEASE_NOTES，将使用默认说明"
  RELEASE_BODY="${TAG} release."
else
  info "从 $RELEASE_NOTES 读取发布说明"
  RELEASE_BODY=$(cat "$RELEASE_NOTES")
fi

info "发布 $TAG"

# ===== [预检] 读取发布 token（自动解锁 gopass）=====
# 首次解密会触发 gpg-agent 口令提示（=解锁）；token 读入非导出变量后，
# 后续构建无论耗时多久都不再依赖 gopass 缓存。条目存在却读不出会报错退出。
info "[预检] 读取发布 token（若 gopass 已上锁，将提示输入口令解锁）"
load_secret GITEE_TOKEN   "$GOPASS_PATH_GITEE"
load_secret GITCODE_TOKEN "$GOPASS_PATH_GITCODE"
load_secret GITLAB_TOKEN  "$GOPASS_PATH_GITLAB"

# ===== [1/8] 同步代码到目标 ref =====
# 默认 checkout main（正常发版：main == tag 对应代码）；
# 设 RELEASE_REF=<tag> 时 checkout 该 tag（detached HEAD），用于补发历史版本——
# 否则会在最新 main 上构建并注入旧版本号，产物不是真正的历史版本二进制。
RELEASE_REF="${RELEASE_REF:-main}"
info "[1/8] 同步代码到 $RELEASE_REF"
if [ "$RELEASE_REF" = "main" ]; then
  git fetch origin main
  git checkout main
  git pull origin main
else
  git fetch origin --tags
  git checkout "$RELEASE_REF"
fi

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
  cp README.en.md "$pkg_dir/README.en.md"
  cp scripts/SCRIPTS.md "$pkg_dir/SCRIPTS.md"
  cp scripts/SCRIPTS.en.md "$pkg_dir/SCRIPTS.en.md"
  # 附带对应平台的宿主机配置脚本：bootstrap 自动配置失败时，
  # 用户可手动运行脚本完成 hosts + CA 配置。脚本含国内镜像 fallback。
  if [ "$goos" = "windows" ]; then
    cp scripts/setup-host.ps1 "$pkg_dir/setup-host.ps1"
    cp scripts/start-mcc.ps1 "$pkg_dir/start-mcc.ps1"
    cp scripts/stop-mcc.ps1 "$pkg_dir/stop-mcc.ps1"
    cp scripts/register-mcc-task.ps1 "$pkg_dir/register-mcc-task.ps1"
  else
    cp scripts/setup-host.sh "$pkg_dir/setup-host.sh"
    cp scripts/docker-host-helper.sh "$pkg_dir/docker-host-helper.sh"
    chmod +x "$pkg_dir/setup-host.sh" "$pkg_dir/docker-host-helper.sh"
  fi

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
  GITEE_RELEASE_ID=$(curl_auth "Authorization" "Bearer ${GITEE_TOKEN}" -sf \
    "https://gitee.com/api/v5/repos/${GITEE_REPO}/releases/tags/${TAG}" \
    2>/dev/null | jq -r '.id // empty') || true

  if [ -n "$GITEE_RELEASE_ID" ]; then
    info "Gitee Release 已存在 (id=${GITEE_RELEASE_ID})"
  else
    STATUS=$(curl_auth "Authorization" "Bearer ${GITEE_TOKEN}" \
      -s -o /tmp/gitee-release.json -w "%{http_code}" \
      -X POST \
      -H "Content-Type: application/json" \
      -d "$(jq -n --arg tag "$TAG" --arg body "$RELEASE_BODY" \
        '{tag_name:$tag, name:$tag, body:$body, target_commitish:"main"}')" \
      "https://gitee.com/api/v5/repos/${GITEE_REPO}/releases" || true)

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
      STATUS=$(curl_auth "Authorization" "Bearer ${GITEE_TOKEN}" \
        -s -o /dev/null -w "%{http_code}" \
        -X POST \
        -F "file=@${f}" \
        "https://gitee.com/api/v5/repos/${GITEE_REPO}/releases/${GITEE_RELEASE_ID}/attach_files" || true)

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
  # 检查 Release 是否已存在
  GITCODE_RELEASE_EXISTS=$(curl_auth "PRIVATE-TOKEN" "${GITCODE_TOKEN}" -sf -o /dev/null \
    "https://api.gitcode.com/api/v5/repos/${GITCODE_REPO}/releases/tags/${TAG}" \
    2>/dev/null && echo "yes" || echo "no")

  if [ "$GITCODE_RELEASE_EXISTS" = "yes" ]; then
    info "GitCode Release 已存在"
  else
    STATUS=$(curl_auth "PRIVATE-TOKEN" "${GITCODE_TOKEN}" \
      -s -o /dev/null -w "%{http_code}" \
      -X POST \
      -H "Content-Type: application/json" \
      -d "$(jq -n --arg tag "$TAG" --arg body "$RELEASE_BODY" \
        '{tag_name:$tag, name:$tag, body:$body, target_commitish:"main"}')" \
      "https://api.gitcode.com/api/v5/repos/${GITCODE_REPO}/releases" || true)

    if [ "$STATUS" -ge 200 ] && [ "$STATUS" -lt 300 ]; then
      info "GitCode Release 创建成功"
    else
      warn "GitCode Release 创建返回 ${STATUS}"
    fi
  fi

  # 上传附件（两步式：获取 OBS 预签名 URL → PUT 上传）
  info "上传 GitCode 附件..."
  for f in "$BUILD_DIR"/*; do
    fname=$(basename "$f")

    # Step 1: 获取 OBS 预签名上传 URL 和所需 headers
    RESP=$(curl_auth "PRIVATE-TOKEN" "${GITCODE_TOKEN}" -sf \
      "https://api.gitcode.com/api/v5/repos/${GITCODE_REPO}/releases/${TAG}/upload_url?file_name=${fname}" \
      2>/dev/null) || true

    UPLOAD_URL=$(echo "$RESP" | jq -r '.url // empty')
    if [ -z "$UPLOAD_URL" ]; then
      warn "  ✗ ${fname}: 无法获取上传 URL（可能已存在）"
      continue
    fi

    HDR_PROJECT=$(echo "$RESP" | jq -r '.headers["x-obs-meta-project-id"]')
    HDR_ACL=$(echo "$RESP" | jq -r '.headers["x-obs-acl"]')
    HDR_CALLBACK=$(echo "$RESP" | jq -r '.headers["x-obs-callback"]')
    HDR_CTYPE=$(echo "$RESP" | jq -r '.headers["Content-Type"]')

    # Step 2: PUT 文件到 OBS
    STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
      -X PUT \
      -H "x-obs-meta-project-id: ${HDR_PROJECT}" \
      -H "x-obs-acl: ${HDR_ACL}" \
      -H "x-obs-callback: ${HDR_CALLBACK}" \
      -H "Content-Type: ${HDR_CTYPE}" \
      --data-binary "@${f}" \
      "$UPLOAD_URL" || true)

    if [ "$STATUS" -ge 200 ] && [ "$STATUS" -lt 300 ]; then
      info "  ✓ ${fname}"
    else
      warn "  ✗ ${fname} (HTTP ${STATUS})"
    fi
  done
else
  warn "GITCODE_TOKEN 未设置，跳过"
fi

# ===== [8/8] GitLab Release + 链接 =====
info "[8/8] GitLab Release + 下载链接"
if [ -n "${GITLAB_TOKEN:-}" ]; then
  ASSETS_JSON="[]"
  for f in "$BUILD_DIR"/*; do
    fname=$(basename "$f")
    ASSETS_JSON=$(echo "$ASSETS_JSON" | jq \
      --arg name "$fname" \
      --arg url "${GITHUB_DL_BASE}/${fname}" \
      '. + [{name: $name, url: $url, link_type: "other"}]')
  done

  STATUS=$(curl_auth "PRIVATE-TOKEN" "${GITLAB_TOKEN}" \
    -s -o /tmp/gitlab-release.json -w "%{http_code}" \
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
    "${GITLAB_URL}/api/v4/projects/${GITLAB_PROJECT_ID}/releases" || true)

  if [ "$STATUS" -ge 200 ] && [ "$STATUS" -lt 300 ]; then
    info "GitLab Release 创建成功（链接指向 GitHub 下载）"
  elif [ "$STATUS" = "409" ]; then
    info "GitLab Release 已存在，跳过"
  else
    warn "GitLab Release 返回 ${STATUS}"
    cat /tmp/gitlab-release.json >&2 2>/dev/null || true
  fi
else
  warn "GITLAB_TOKEN 未设置，跳过 GitLab Release"
fi

info "清理构建目录"
rm -rf "$BUILD_DIR"

# ===== 完成 =====
info "${TAG} 发布完成"
