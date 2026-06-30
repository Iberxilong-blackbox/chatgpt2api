#!/bin/bash
#
# 一键构建 + 更新部署脚本
# 在服务器上 git pull 之后运行：
#
#   sudo ./deploy/update.sh                # 构建前端 → 编译后端 → 更新部署
#   sudo ./deploy/update.sh --env          # 仅更新 .env，重启服务，跳过构建
#   sudo ./deploy/update.sh --sync-ac # 同步 data/auto_import/ 的账号 JSON 到部署目录并重启
#
# 选项:
#   --env            仅更新 .env 配置文件并重启服务，不做构建编译
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

    # 更新 .env
    log_info "更新 .env 配置文件..."
    cp .env "${INSTALL_DIR}/.env"
    chown "${APP_NAME}:${APP_NAME}" "${INSTALL_DIR}/.env"
    chmod 640 "${INSTALL_DIR}/.env"
    log_info ".env 已更新"

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

# 5. 同步 .env 配置文件
log_info "更新 .env 配置文件..."
cp .env "${INSTALL_DIR}/.env"
chown "${APP_NAME}:${APP_NAME}" "${INSTALL_DIR}/.env"
chmod 640 "${INSTALL_DIR}/.env"
log_info ".env 已更新"

# 6. 同步运行时数据文件
sync_warming_prompts

# 7. 启动服务
log_info "启动服务 ${SERVICE_NAME}..."
systemctl start "$SERVICE_NAME"
log_info "服务已启动"

# 8. 检查状态
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
