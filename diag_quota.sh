#!/bin/bash
# 诊断账号额度 — 在 Desi 服务器上运行
# 用法: bash diag_quota.sh <access_token>
# 如果不提供 token，自动从数据库取第一个限流账号的 token

set -euo pipefail
TOKEN="${1:-}"

BASE="https://chatgpt.com"
PROXY="--socks5-hostname 127.0.0.1:10086"

# Firefox User-Agent（匹配当前 surf impersonate.Firefox() 的 header 风格）
FF_UA="Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:148.0) Gecko/20100101 Firefox/148.0"

echo "=============================================="
echo "  账号额度诊断"
echo "=============================================="
echo ""

# 步骤 1: bootstrap — 验证 Cloudflare 是否放行
echo ">>> 步骤 1: Bootstrap (GET /) — 测试 Cloudflare 连通性"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" \
  -H "User-Agent: $FF_UA" \
  -H "Accept: text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8" \
  -H "Accept-Language: zh-CN,zh;q=0.9,en;q=0.8" \
  $PROXY \
  "$BASE/" 2>/dev/null || echo "000")
echo "  状态码: $HTTP_CODE"
if [ "$HTTP_CODE" = "403" ]; then
  echo "  ❌ Cloudflare 仍然拦截！"
elif [ "$HTTP_CODE" = "200" ]; then
  echo "  ✅ Cloudflare 放行"
else
  echo "  ⚠ 非预期状态码"
fi
echo ""

# 步骤 2: /backend-api/me
echo ">>> 步骤 2: GET /backend-api/me — 账号基本信息"
ME=$(curl -s \
  -H "Authorization: Bearer $TOKEN" \
  -H "User-Agent: $FF_UA" \
  -H "Accept: */*" \
  -H "Accept-Language: zh-CN,zh;q=0.9,en;q=0.8" \
  -H "Content-Type: application/json" \
  -H "Origin: https://chatgpt.com" \
  -H "Referer: https://chatgpt.com/" \
  $PROXY \
  "$BASE/backend-api/me" 2>/dev/null)
echo "  原始响应 (前 500 字符):"
echo "$ME" | head -c 500
echo ""
echo ""

# 步骤 3: /backend-api/conversation/init — 最关键的一步
echo ">>> 步骤 3: POST /backend-api/conversation/init — 获取 limits_progress"
INIT=$(curl -s \
  -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "User-Agent: $FF_UA" \
  -H "Accept: */*" \
  -H "Accept-Language: zh-CN,zh;q=0.9,en;q=0.8" \
  -H "Content-Type: application/json" \
  -H "Origin: https://chatgpt.com" \
  -H "Referer: https://chatgpt.com/" \
  $PROXY \
  -d '{"gizmo_id":null,"requested_default_model":null,"conversation_id":null,"timezone_offset_min":-480}' \
  "$BASE/backend-api/conversation/init" 2>/dev/null)
echo "  原始响应 (前 500 字符):"
echo "$INIT" | head -c 500
echo ""
echo ""

# 步骤 4: limits_progress 专项分析
echo ">>> 步骤 4: limits_progress 分析"
echo "$INIT" | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
except:
    print('  ❌ JSON 解析失败 — 可能是 403 或空响应')
    sys.exit(1)

limits = data.get('limits_progress', data.get('limits', []))
print(f'  limits_progress 条目数: {len(limits)}')
found_image = False
for item in limits:
    if isinstance(item, dict):
        fn = item.get('feature_name', '?')
        remaining = item.get('remaining', '?')
        reset = item.get('reset_after', '')
        limit = item.get('limit', '?')
        print(f'    feature={fn}  remaining={remaining}  limit={limit}  reset_after={reset}')
        if fn == 'image_gen':
            found_image = True
if not limits:
    print('  (空数组)')
if not found_image and limits:
    print('  ⚠ 没有 image_gen feature — 可能是 Free 账号不返回此 feature？')
if found_image:
    print()
    print('  >>> 分析:')
    print('  如果 remaining >= 某个值 (>0)，但 Web UI 显示 quota=0 → 代码有 bug')
    print('  如果 remaining == 0 → 账号确实零额度（H1+H5 确认）')
    print('  如果 remaining == null/不存在 → 字段名变了（H3）')
" 2>/dev/null || echo "  (无法解析 — 原始响应在上面)"

echo ""
echo "=============================================="
echo " 诊断完成"
echo "=============================================="
echo ""
echo "解读:"
echo "  1. 步骤1 403 → Cloudflare 仍然拦截 curl（但 Go 程序用 uTLS 可能不同）"
echo "  2. 步骤4 image_gen remaining=0 → 账号确实没额度"
echo "  3. 步骤4 image_gen remaining>0 → 代码后处理有问题"
echo "  4. 步骤4 无 image_gen → Free 账号或 API 格式变化"
