#!/bin/bash
#
# 一键构建 + 更新部署脚本
# 在服务器上 git pull 之后运行：
#
#   sudo ./deploy/update.sh                # 构建前端 → 编译后端 → 更新部署
#   sudo ./deploy/update.sh --env          # 仅更新 .env，重启服务，跳过构建
#   sudo ./deploy/update.sh --env-set KEY=VALUE  # 设置单个环境变量并重启服务
#   sudo ./deploy/update.sh --sync-ac # 同步 data/auto_import/ 的账号 JSON 到部署目录并重启
#
# 选项:
#   --env            仅更新 .env 配置文件并重启服务，不做构建编译
#   --env-set KV    设置运行目录 .env 中的单个环境变量（存在则覆盖，不存在则追加），然后重启服务
#   --sync-ac        将项目 data/auto_import/ 下的 .json 文件同步到 /opt/chatgpt2api/data/auto_import/ 并重启服务

set -euo pipefail

APP_NAME="chatgpt2api"
INSTALL_DIR="/opt/${APP_NAME}"
SERVICE_NAME="${APP_NAME}"
BINARY="./${APP_NAME}"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[INFO]${NC}  $1"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC}  $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

# merge_env src dst — 单向合并 .env，仅补充新 key，不覆盖已有值
merge_env() {
    local src="$1" dst="$2"

    if [ ! -f "$dst" ]; then
        cp "$src" "$dst"
        log_info ".env 已创建"
        return
    fi

    local tmp_dst
    tmp_dst=$(mktemp) || { log_error "创建临时文件失败"; return 1; }
    cp "$dst" "$tmp_dst"
    local added=0

    while IFS= read -r line; do
        # 跳过空行和注释
        [[ "$line" =~ ^[[:space:]]*# || "$line" =~ ^[[:space:]]*$ ]] && continue

        # 提取 key（去掉 export 前缀，取第一个 = 之前的部分）
        local key="${line#export }"
        # 去掉行首空白
        key="${key#"${key%%[![:space:]]*}"}"
        key="${key%%=*}"
        key="${key%"${key##*[![:space:]]}"}"
        [[ -z "$key" ]] && continue

        # 如果运行目录已有该 key，跳过
        if grep -qE "^(export[[:space:]]+)?${key}=" "$tmp_dst" 2>/dev/null; then
            continue
        fi

        echo "$line" >> "$tmp_dst"
        added=$((added + 1))
    done < "$src"

    if [ "$added" -gt 0 ]; then
        mv "$tmp_dst" "$dst"
        log_info "已新增 ${added} 条配置项"
    else
        rm "$tmp_dst"
        log_info ".env 已是最新，无需更新"
    fi
}

sync_warming_prompts() {
    local src="${PROJECT_DIR}/data/warming_prompts.json"
    local dst_dir="${INSTALL_DIR}/data"
    local dst="${dst_dir}/warming_prompts.json"

    if [ ! -f "$src" ]; then
        log_warn "未找到养号语料文件，跳过同步: ${src}"
        return 0
    fi

    mkdir -p "$dst_dir"
    cp "$src" "$dst"
    chown "${APP_NAME}:${APP_NAME}" "$dst"
    chmod 640 "$dst"
    log_info "已同步养号语料文件到 ${dst}"
}

# 检查是否以 root 运行
if [ "$(id -u)" -ne 0 ]; then
    log_error "请以 root 或通过 sudo 运行此脚本"
    exit 1
fi

PROJECT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$PROJECT_DIR"
log_info "项目目录: ${PROJECT_DIR}"

# 补充 PATH：sudo 环境下可能不包含 bun、go 等用户安装的命令
export PATH="$HOME/.bun/bin:$HOME/.local/bin:/usr/local/go/bin:$PATH"
# 如果 bun 或 go 仍找不到，尝试从用户 shell profile 中加载 PATH
if ! command -v bun &>/dev/null || ! command -v go &>/dev/null; then
    for rc in "$HOME/.bashrc" "$HOME/.zshrc" "$HOME/.profile"; do
        [ -f "$rc" ] && source "$rc" 2>/dev/null || true
    done
fi

# ============================================================
# 模式判断：--env 模式 = 仅更新配置，跳过构建
# ============================================================
if [ "${1:-}" = "--env" ]; then
    log_info "模式: 仅更新配置（跳过构建编译）"

    # 检查 .env 文件
    if [ ! -f ".env" ]; then
        log_error "当前目录未找到 .env 文件"
        exit 1
    fi

    # 停止服务
    log_info "停止服务 ${SERVICE_NAME}..."
    systemctl stop "$SERVICE_NAME"

    # 更新 .env（单向合并，不覆盖运行目录已有的 key）
    log_info "更新 .env 配置文件..."
    merge_env .env "${INSTALL_DIR}/.env"
    chown "${APP_NAME}:${APP_NAME}" "${INSTALL_DIR}/.env"
    chmod 640 "${INSTALL_DIR}/.env"

    # 启动服务
    log_info "启动服务 ${SERVICE_NAME}..."
    systemctl start "$SERVICE_NAME"
    log_info "服务已启动"

    # 检查状态
    sleep 1
    if systemctl is-active --quiet "$SERVICE_NAME"; then
        log_info "服务运行正常"
    else
        log_error "服务启动失败，查看日志: journalctl -u ${SERVICE_NAME} -n 50"
        exit 1
    fi

    echo ""
    echo "========== 实时日志（Ctrl+C 退出） =========="
    journalctl -u "$SERVICE_NAME" -n 20 -f
    exit 0
fi

# ============================================================
# 模式判断：--env-set = 设置单个环境变量并重启
# ============================================================
if [ "${1:-}" = "--env-set" ]; then
    if [ -z "${2:-}" ]; then
        log_error "用法: $0 --env-set KEY=VALUE"
        exit 1
    fi

    env_pair="$2"
    key="${env_pair%%=*}"
    value="${env_pair#*=}"

    if [ -z "$key" ] || [ "$key" = "$env_pair" ]; then
        log_error "格式错误，需要 KEY=VALUE: ${env_pair}"
        exit 1
    fi

    env_file="${INSTALL_DIR}/.env"

    if [ ! -f "$env_file" ]; then
        log_error ".env 文件不存在: ${env_file}"
        exit 1
    fi

    # 检查是否已存在该 key（支持 export 前缀）
    if grep -qE "^(export[[:space:]]+)?${key}=" "$env_file" 2>/dev/null; then
        # 已存在，用 bash 循环替换（比 sed/awk 更安全地处理特殊字符）
        tmp_env=$(mktemp) || { log_error "创建临时文件失败"; exit 1; }
        while IFS= read -r line; do
            line_key="${line#export }"
            line_key="${line_key#"${line_key%%[![:space:]]*}"}"
            line_key="${line_key%%=*}"
            if [ "$line_key" = "$key" ]; then
                echo "${key}=${value}"
            else
                echo "$line"
            fi
        done < "$env_file" > "$tmp_env"
        mv "$tmp_env" "$env_file"
        log_info "已更新: ${key}=${value}"
    else
        # 不存在，追加
        echo "${key}=${value}" >> "$env_file"
        log_info "已新增: ${key}=${value}"
    fi

    chown "${APP_NAME}:${APP_NAME}" "$env_file"
    chmod 640 "$env_file"

    # 重启服务
    log_info "重启服务 ${SERVICE_NAME}..."
    systemctl restart "$SERVICE_NAME"
    sleep 1

    if systemctl is-active --quiet "$SERVICE_NAME"; then
        log_info "服务运行正常"
    else
        log_error "服务启动失败，查看日志: journalctl -u ${SERVICE_NAME} -n 50"
        exit 1
    fi

    echo ""
    echo "========== 实时日志（Ctrl+C 退出） =========="
    journalctl -u "$SERVICE_NAME" -n 20 -f
    exit 0
fi

# ============================================================
# 模式判断：--sync-ac = 同步账号 JSON 并重启
# ============================================================
if [ "${1:-}" = "--sync-ac" ]; then
    log_info "模式: 同步账号 JSON（跳过构建编译）"

    SRC_DIR="${PROJECT_DIR}/data/auto_import"
    DST_DIR="${INSTALL_DIR}/data/auto_import"

    if [ ! -d "$SRC_DIR" ]; then
        log_error "源目录不存在: ${SRC_DIR}"
        exit 1
    fi

    # 统计待同步的 .json 文件（排除 imported 子目录）
    json_count=$(find "$SRC_DIR" -maxdepth 1 -name '*.json' -type f 2>/dev/null | wc -l)
    if [ "$json_count" -eq 0 ]; then
        log_warn "${SRC_DIR} 下没有 .json 文件，无需同步"
        exit 0
    fi

    log_info "发现 ${json_count} 个账号 JSON 文件"

    # 确保目标目录存在
    mkdir -p "$DST_DIR"

    # 复制 .json 文件（仅第一层，不复制 imported 子目录）
    cp "$SRC_DIR"/*.json "$DST_DIR"/
    chown -R "${APP_NAME}:${APP_NAME}" "$DST_DIR"
    chmod 640 "$DST_DIR"/*.json 2>/dev/null || true
    log_info "已同步 ${json_count} 个文件到 ${DST_DIR}"

    sync_warming_prompts

    # 重启服务触发导入
    log_info "重启服务 ${SERVICE_NAME}..."
    systemctl restart "$SERVICE_NAME"
    sleep 1

    if systemctl is-active --quiet "$SERVICE_NAME"; then
        log_info "服务运行正常，账号 JSON 将在启动时自动导入"
    else
        log_error "服务启动失败，查看日志: journalctl -u ${SERVICE_NAME} -n 50"
        exit 1
    fi

    # 查看导入日志
    echo ""
    echo "========== 实时日志（Ctrl+C 退出） =========="
    journalctl -u "$SERVICE_NAME" -n 20 -f
    exit 0
fi

# ============================================================
# 默认模式：完整构建 + 更新部署
# ============================================================
log_info "模式: 完整构建 + 更新部署"

# 1. 构建前端
log_info "构建前端..."
cd web
bun install
bun run build
cd ..
log_info "前端构建完成"

# 2. 构建后端
log_info "编译后端..."
go build -o "$BINARY" ./internal
log_info "后端编译完成"

# 3. 停止服务
log_info "停止服务 ${SERVICE_NAME}..."
if systemctl is-active --quiet "$SERVICE_NAME"; then
    systemctl stop "$SERVICE_NAME"
    log_info "服务已停止"
else
    log_warn "服务未在运行，跳过停止"
fi

# 4. 替换二进制文件
log_info "更新二进制文件..."
cp "$BINARY" "${INSTALL_DIR}/${BINARY}"
chown "${APP_NAME}:${APP_NAME}" "${INSTALL_DIR}/${BINARY}"
chmod 755 "${INSTALL_DIR}/${BINARY}"
log_info "二进制文件已更新"

# 5. 同步运行时数据文件
sync_warming_prompts

# 6. 启动服务
log_info "启动服务 ${SERVICE_NAME}..."
systemctl start "$SERVICE_NAME"
log_info "服务已启动"

# 7. 检查状态
sleep 1
if systemctl is-active --quiet "$SERVICE_NAME"; then
    log_info "服务运行正常"
else
    log_error "服务启动失败，查看日志: journalctl -u ${SERVICE_NAME} -n 50"
    exit 1
fi

echo ""
echo "========== 实时日志（Ctrl+C 退出） =========="
journalctl -u "$SERVICE_NAME" -n 20 -f
