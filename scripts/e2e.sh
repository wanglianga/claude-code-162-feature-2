#!/usr/bin/env bash
# 端到端验证: 完整链路 + 全部异常场景
# 前置: scripts/infra.sh 已执行; 本脚本会重建数据库并启动 server+worker
set -uo pipefail

cd "$(dirname "$0")/.."
export PG_DSN="${PG_DSN:-postgres://postgres@127.0.0.1:5433/recycle?sslmode=disable}"
export REDIS_ADDR="${REDIS_ADDR:-127.0.0.1:6379}"
export KAFKA_BROKERS="${KAFKA_BROKERS:-127.0.0.1:9092}"
export HTTP_ADDR=":18080"
export LATE_GRACE_MINUTES=0
BASE="http://127.0.0.1:18080/api/v1"

PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); echo "  PASS  $1"; }
bad()  { FAIL=$((FAIL+1)); echo "  FAIL  $1"; }
check(){ # check <desc> <actual> <expected>
  if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (got=$2 want=$3)"; fi
}
jqget(){ echo "$2" | jq -r "$1"; }

R1="Authorization: Bearer token-resident1"
R2="Authorization: Bearer token-resident2"
C1="Authorization: Bearer token-collector1"
S1="Authorization: Bearer token-station1"
ST="Authorization: Bearer token-street1"
CT="Content-Type: application/json"

# wait_status <order_id> <token-header> <expected> [timeout]
wait_status(){
  local oid="$1" hdr="$2" want="$3" tmo="${4:-15}" i
  for i in $(seq 1 "$tmo"); do
    local st
    st=$(curl -s "$BASE/orders/$oid" -H "$hdr" | jq -r '.order.status')
    [ "$st" = "$want" ] && { echo "$st"; return 0; }
    sleep 1
  done
  curl -s "$BASE/orders/$oid" -H "$hdr" | jq -r '.order.status'
  return 1
}
# wait_balance <expected> [timeout]
wait_balance(){
  local want="$1" tmo="${2:-15}" i
  for i in $(seq 1 "$tmo"); do
    local b
    b=$(curl -s "$BASE/points/balance" -H "$R1" | jq -r '.balance')
    [ "$b" = "$want" ] && { echo "$b"; return 0; }
    sleep 1
  done
  curl -s "$BASE/points/balance" -H "$R1" | jq -r '.balance'
  return 1
}

echo "=== 0. 重建环境 ==="
psql "$PG_DSN" -q -c "DROP SCHEMA public CASCADE; CREATE SCHEMA public;" 2>/dev/null || { echo "pg reset failed"; exit 1; }
redis-cli -n 0 FLUSHALL >/dev/null
pkill -x recycle-app 2>/dev/null; sleep 1
go build -o /tmp/recycle-app ./cmd/app || { echo "build failed"; exit 1; }
/tmp/recycle-app seed >/dev/null || { echo "seed failed"; exit 1; }
POINTS_CONSUMER_PAUSED=0 /tmp/recycle-app all > /tmp/recycle-app.log 2>&1 &
APP_PID=$!
for i in $(seq 1 20); do curl -sf "http://127.0.0.1:18080/healthz" >/dev/null 2>&1 && break; sleep 1; done
curl -sf "http://127.0.0.1:18080/healthz" >/dev/null || { echo "server not up"; tail -20 /tmp/recycle-app.log; exit 1; }
echo "server up (pid $APP_PID)"

NOW=$(date +%s)
WS=$(date -u -d "@$((NOW+600))" +%Y-%m-%dT%H:%M:%SZ)
WE=$(date -u -d "@$((NOW+3600))" +%Y-%m-%dT%H:%M:%SZ)

echo "=== 1. 居民预约 -> 预估价(价格快照) ==="
RESP=$(curl -s -X POST "$BASE/orders" -H "$R1" -H "$CT" -d "{
  \"items\":[{\"category\":\"paper\",\"est_weight_kg\":12.5},{\"category\":\"plastic\",\"est_weight_kg\":5}],
  \"time_window_start\":\"$WS\",\"time_window_end\":\"$WE\",
  \"photos\":[\"p1.jpg\"],\"idempotency_key\":\"e2e-order-1\"}")
OID=$(jqget '.order.id' "$RESP")
EST=$(jqget '.order.estimate_yuan' "$RESP")
VCODE=$(jqget '.order.verify_code' "$RESP")
# 纸12.5kg*1.2=15元 + 塑5kg*2.0=10元 = 25元
check "预估价=25元" "$EST" "25"
[ -n "$OID" ] && [ "$OID" != "null" ] && ok "订单创建 id=$OID" || bad "订单创建 $RESP"

echo "=== 2. 幂等: 同键重复提交 ==="
RESP2=$(curl -s -X POST "$BASE/orders" -H "$R1" -H "$CT" -d "{
  \"items\":[{\"category\":\"paper\",\"est_weight_kg\":12.5},{\"category\":\"plastic\",\"est_weight_kg\":5}],
  \"time_window_start\":\"$WS\",\"time_window_end\":\"$WE\",\"idempotency_key\":\"e2e-order-1\"}")
check "幂等返回同一订单" "$(jqget '.order.id' "$RESP2")" "$OID"

echo "=== 3. 派单(GEO 就近) ==="
check "状态=ASSIGNED" "$(wait_status "$OID" "$R1" ASSIGNED)" "ASSIGNED"

echo "=== 4. 回收员接单->出发->到达 ==="
for act in accept start arrive; do
  curl -s -X POST "$BASE/collector/orders/$OID/$act" -H "$C1" >/dev/null
done
ST1=$(curl -s "$BASE/orders/$OID" -H "$C1" | jq -r '.order.status')
check "状态=ARRIVED" "$ST1" "ARRIVED"

echo "=== 5. 核验居民(核验码) ==="
ERR=$(curl -s -X POST "$BASE/collector/orders/$OID/verify-resident" -H "$C1" -H "$CT" \
  -d '{"method":"SCAN_CODE","verify_code":"000000"}' | jq -r '.error // "none"')
check "错误核验码被拒" "$ERR" "verify_code mismatch"
curl -s -X POST "$BASE/collector/orders/$OID/verify-resident" -H "$C1" -H "$CT" \
  -d "{\"method\":\"SCAN_CODE\",\"verify_code\":\"$VCODE\"}" >/dev/null
ST1=$(curl -s "$BASE/orders/$OID" -H "$C1" | jq -r '.order.status')
check "状态=RESIDENT_VERIFIED" "$ST1" "RESIDENT_VERIFIED"

echo "=== 6. 居民临时加品类 ==="
ADD=$(curl -s -X POST "$BASE/collector/orders/$OID/items" -H "$C1" -H "$CT" \
  -d '{"category":"metal","est_weight_kg":3}')
check "加品类来源" "$(jqget '.item.source' "$ADD")" "ONSITE_ADDED"

echo "=== 7. 现场称重(含扣杂: 塑料混入10%不可回收物) ==="
W=$(curl -s -X POST "$BASE/collector/orders/$OID/weigh" -H "$C1" -H "$CT" -d '{
  "weights":[{"category":"paper","weight_kg":12.0},
             {"category":"plastic","weight_kg":5.0,"contamination_pm":100},
             {"category":"metal","weight_kg":3.0}],
  "photos":["w1.jpg"]}')
# 纸12*1.2=14.4 + 塑5*0.9*2=9 + 金属3*3.5=10.5 = 33.9元
check "现场定价=33.9元" "$(jqget '.order.onsite_yuan' "$W")" "33.9"
check "状态=WEIGHED" "$(jqget '.order.status' "$W")" "WEIGHED"

echo "=== 8. 居民确认称重 ==="
curl -s -X POST "$BASE/orders/$OID/confirm" -H "$R1" >/dev/null
ST1=$(curl -s "$BASE/orders/$OID" -H "$R1" | jq -r '.order.status')
check "状态=CONFIRMED" "$ST1" "CONFIRMED"

echo "=== 9. 积分结算(Kafka 异步入账) ==="
SE=$(curl -s -X POST "$BASE/collector/orders/$OID/settle" -H "$C1" -H "$CT" -d '{"type":"points"}')
# 33.9元 * 10 * 1.1 = 372.9 -> 373 积分
check "应得积分=373" "$(jqget '.settlement.points' "$SE")" "373"
check "积分到账 balance=373" "$(wait_balance 373)" "373"
check "状态=SETTLED" "$(wait_status "$OID" "$R1" SETTLED)" "SETTLED"

echo "=== 10. 送达站点 -> 复称(容差内) -> 环保凭证 ==="
curl -s -X POST "$BASE/collector/orders/$OID/deliver" -H "$C1" >/dev/null
RW=$(curl -s -X POST "$BASE/station/inbound/$OID/reweigh" -H "$S1" -H "$CT" -d '{
  "weights":[{"category":"paper","weight_kg":11.9},
             {"category":"plastic","weight_kg":5.05},
             {"category":"metal","weight_kg":3.0}]}')
check "状态=COMPLETED" "$(jqget '.order.status' "$RW")" "COMPLETED"
VST=$(jqget '.items[0].variance_status' "$RW")
check "复称差异在容差内" "$VST" "OK"
CERTS=$(curl -s "$BASE/certificates" -H "$R1" | jq -r '.certificates | length')
check "环保凭证=3张" "$CERTS" "3"

echo "=== 11. 全链路追踪 ==="
TR=$(curl -s "$BASE/orders/$OID/trace" -H "$R1")
NCONF=$(echo "$TR" | jq -r '.confirmations | length')
[ "$NCONF" -ge 3 ] && ok "确认记录 $NCONF 条(核验/称重/结算)" || bad "确认记录不足: $NCONF"
NEV=$(echo "$TR" | jq -r '.events | length')
[ "$NEV" -ge 10 ] && ok "状态事件 $NEV 条" || bad "事件不足: $NEV"
check "最终金额存在" "$(echo "$TR" | jq -r '.order.final_cents > 0')" "true"

echo "=== 12. 现金结算 + 支付失败重试 ==="
RESP=$(curl -s -X POST "$BASE/orders" -H "$R1" -H "$CT" -d "{
  \"items\":[{\"category\":\"appliance\",\"est_weight_kg\":8}],
  \"time_window_start\":\"$WS\",\"time_window_end\":\"$WE\",\"idempotency_key\":\"e2e-order-2\"}")
OID2=$(jqget '.order.id' "$RESP")
VCODE2=$(jqget '.order.verify_code' "$RESP")
wait_status "$OID2" "$C1" ASSIGNED >/dev/null
curl -s -X POST "$BASE/collector/orders/$OID2/accept" -H "$C1" >/dev/null
curl -s -X POST "$BASE/collector/orders/$OID2/start" -H "$C1" >/dev/null
curl -s -X POST "$BASE/collector/orders/$OID2/arrive" -H "$C1" >/dev/null
curl -s -X POST "$BASE/collector/orders/$OID2/verify-resident" -H "$C1" -H "$CT" \
  -d "{\"method\":\"SCAN_CODE\",\"verify_code\":\"$VCODE2\"}" >/dev/null
curl -s -X POST "$BASE/collector/orders/$OID2/weigh" -H "$C1" -H "$CT" \
  -d '{"weights":[{"category":"appliance","weight_kg":8}]}' >/dev/null
curl -s -X POST "$BASE/orders/$OID2/confirm" -H "$R1" >/dev/null
F1=$(curl -s -X POST "$BASE/collector/orders/$OID2/settle" -H "$C1" -H "$CT" \
  -d '{"type":"cash","simulate_fail":true}')
check "现金支付失败" "$(jqget '.settlement.status' "$F1")" "FAILED"
ST1=$(curl -s "$BASE/orders/$OID2" -H "$R1" | jq -r '.order.status')
check "失败后订单停留 CONFIRMED" "$ST1" "CONFIRMED"
F2=$(curl -s -X POST "$BASE/collector/orders/$OID2/settle/retry" -H "$C1" -H "$CT" -d '{}')
check "重试成功" "$(jqget '.settlement.status' "$F2")" "SUCCESS"
check "重试后 SETTLED" "$(jqget '.order.status' "$F2")" "SETTLED"
# 8kg 家电: 现场 8*1.5=12元
check "现金金额=12元" "$(jqget '.settlement.amount_yuan' "$F2")" "12"

echo "=== 13. 老人协助确认 ==="
RESP=$(curl -s -X POST "$BASE/orders" -H "$R2" -H "$CT" -d "{
  \"items\":[{\"category\":\"clothing\",\"est_weight_kg\":6}],
  \"time_window_start\":\"$WS\",\"time_window_end\":\"$WE\",\"idempotency_key\":\"e2e-order-3\"}")
OID3=$(jqget '.order.id' "$RESP")
wait_status "$OID3" "$C1" ASSIGNED >/dev/null
curl -s -X POST "$BASE/collector/orders/$OID3/accept" -H "$C1" >/dev/null
curl -s -X POST "$BASE/collector/orders/$OID3/start" -H "$C1" >/dev/null
curl -s -X POST "$BASE/collector/orders/$OID3/arrive" -H "$C1" >/dev/null
# 老人不会扫码: 回收员协助核验+协助确认
curl -s -X POST "$BASE/collector/orders/$OID3/verify-resident" -H "$C1" -H "$CT" \
  -d '{"method":"ASSISTED","assisted_note":"老人无智能手机, 口头确认并拍照"}' >/dev/null
curl -s -X POST "$BASE/collector/orders/$OID3/weigh" -H "$C1" -H "$CT" \
  -d '{"weights":[{"category":"clothing","weight_kg":6}]}' >/dev/null
AC=$(curl -s -X POST "$BASE/collector/orders/$OID3/confirm-assisted" -H "$C1" -H "$CT" \
  -d '{"assisted_note":"老人确认重量6kg, 女儿电话复核"}')
check "协助确认后 CONFIRMED" "$(jqget '.order.status' "$AC")" "CONFIRMED"
M=$(curl -s "$BASE/orders/$OID3/trace" -H "$ST" | jq -r '[.confirmations[].method] | join(",")')
echo "$M" | grep -q "ASSISTED" && ok "协助确认留痕 method=ASSISTED" || bad "协助确认留痕: $M"
# 非老人用协助确认应被拒
RESP=$(curl -s -X POST "$BASE/orders" -H "$R1" -H "$CT" -d "{
  \"items\":[{\"category\":\"paper\",\"est_weight_kg\":2}],
  \"time_window_start\":\"$WS\",\"time_window_end\":\"$WE\",\"idempotency_key\":\"e2e-order-3b\"}")
OID3B=$(jqget '.order.id' "$RESP")
wait_status "$OID3B" "$C1" ASSIGNED >/dev/null
curl -s -X POST "$BASE/collector/orders/$OID3B/accept" -H "$C1" >/dev/null
curl -s -X POST "$BASE/collector/orders/$OID3B/start" -H "$C1" >/dev/null
curl -s -X POST "$BASE/collector/orders/$OID3B/arrive" -H "$C1" >/dev/null
ERR=$(curl -s -X POST "$BASE/collector/orders/$OID3B/verify-resident" -H "$C1" -H "$CT" \
  -d '{"method":"ASSISTED","assisted_note":"x"}' | jq -r '.error // "none"')
check "非老人协助确认被拒" "$ERR" "assisted confirm only allowed for elderly residents"
curl -s -X POST "$BASE/orders/$OID3B/cancel" -H "$R1" -H "$CT" -d '{"reason":"test cleanup"}' >/dev/null

echo "=== 14. 称重争议 -> 街道裁决 ==="
RESP=$(curl -s -X POST "$BASE/orders" -H "$R1" -H "$CT" -d "{
  \"items\":[{\"category\":\"metal\",\"est_weight_kg\":10}],
  \"time_window_start\":\"$WS\",\"time_window_end\":\"$WE\",\"idempotency_key\":\"e2e-order-4\"}")
OID4=$(jqget '.order.id' "$RESP")
VCODE4=$(jqget '.order.verify_code' "$RESP")
wait_status "$OID4" "$C1" ASSIGNED >/dev/null
curl -s -X POST "$BASE/collector/orders/$OID4/accept" -H "$C1" >/dev/null
curl -s -X POST "$BASE/collector/orders/$OID4/start" -H "$C1" >/dev/null
curl -s -X POST "$BASE/collector/orders/$OID4/arrive" -H "$C1" >/dev/null
curl -s -X POST "$BASE/collector/orders/$OID4/verify-resident" -H "$C1" -H "$CT" \
  -d "{\"method\":\"SCAN_CODE\",\"verify_code\":\"$VCODE4\"}" >/dev/null
curl -s -X POST "$BASE/collector/orders/$OID4/weigh" -H "$C1" -H "$CT" \
  -d '{"weights":[{"category":"metal","weight_kg":8}]}' >/dev/null
D=$(curl -s -X POST "$BASE/orders/$OID4/disputes" -H "$R1" -H "$CT" \
  -d '{"kind":"WEIGHT","detail":"居民复称约10kg, 与回收员8kg不一致"}')
DID=$(jqget '.dispute.id' "$D")
ST1=$(curl -s "$BASE/orders/$OID4" -H "$R1" | jq -r '.order.status')
check "争议后 DISPUTED" "$ST1" "DISPUTED"
curl -s -X POST "$BASE/street/disputes/$DID/resolve" -H "$ST" -H "$CT" \
  -d '{"action":"adjust","note":"站点视频复核为9.5kg","adjusted_weights":[{"category":"metal","weight_kg":9.5}]}' >/dev/null
ST1=$(curl -s "$BASE/orders/$OID4" -H "$R1" | jq -r '.order.status')
check "裁决后回到 WEIGHED" "$ST1" "WEIGHED"
curl -s -X POST "$BASE/orders/$OID4/confirm" -H "$R1" >/dev/null
SE=$(curl -s -X POST "$BASE/collector/orders/$OID4/settle" -H "$C1" -H "$CT" -d '{"type":"cash"}')
# 9.5kg * 3.5 = 33.25元
check "裁决后按9.5kg结算=33.25元" "$(jqget '.settlement.amount_yuan' "$SE")" "33.25"

echo "=== 15. 复称差异超差 -> 收益扣罚 ==="
curl -s -X POST "$BASE/collector/orders/$OID4/deliver" -H "$C1" >/dev/null
# 现场 9.5kg, 站点复称 8kg: 差 1.5kg (15.8%) 超差
RW=$(curl -s -X POST "$BASE/station/inbound/$OID4/reweigh" -H "$S1" -H "$CT" \
  -d '{"weights":[{"category":"metal","weight_kg":8}]}')
check "超差标记" "$(jqget '.items[0].variance_status' "$RW")" "OVER_TOLERANCE"
EARN=$(curl -s "$BASE/collector/earnings" -H "$C1")
PEN=$(echo "$EARN" | jq -r ".earnings[] | select(.order_id==$OID4) | .penalty_yuan")
# 差异 9.5*3.5-8*3.5=5.25元 扣罚
check "回收员扣罚=5.25元" "$PEN" "5.25"

echo "=== 16. 积分不到账 -> 申诉对账 ==="
kill "$APP_PID" 2>/dev/null; sleep 1
POINTS_CONSUMER_PAUSED=1 /tmp/recycle-app all > /tmp/recycle-app2.log 2>&1 &
APP_PID=$!
for i in $(seq 1 20); do curl -sf "http://127.0.0.1:18080/healthz" >/dev/null 2>&1 && break; sleep 1; done
RESP=$(curl -s -X POST "$BASE/orders" -H "$R1" -H "$CT" -d "{
  \"items\":[{\"category\":\"paper\",\"est_weight_kg\":10}],
  \"time_window_start\":\"$WS\",\"time_window_end\":\"$WE\",\"idempotency_key\":\"e2e-order-5\"}")
OID5=$(jqget '.order.id' "$RESP")
VCODE5=$(jqget '.order.verify_code' "$RESP")
wait_status "$OID5" "$C1" ASSIGNED >/dev/null
curl -s -X POST "$BASE/collector/orders/$OID5/accept" -H "$C1" >/dev/null
curl -s -X POST "$BASE/collector/orders/$OID5/start" -H "$C1" >/dev/null
curl -s -X POST "$BASE/collector/orders/$OID5/arrive" -H "$C1" >/dev/null
curl -s -X POST "$BASE/collector/orders/$OID5/verify-resident" -H "$C1" -H "$CT" \
  -d "{\"method\":\"SCAN_CODE\",\"verify_code\":\"$VCODE5\"}" >/dev/null
curl -s -X POST "$BASE/collector/orders/$OID5/weigh" -H "$C1" -H "$CT" \
  -d '{"weights":[{"category":"paper","weight_kg":10}]}' >/dev/null
curl -s -X POST "$BASE/orders/$OID5/confirm" -H "$R1" >/dev/null
BAL_BEFORE=$(curl -s "$BASE/points/balance" -H "$R1" | jq -r '.balance')
curl -s -X POST "$BASE/collector/orders/$OID5/settle" -H "$C1" -H "$CT" -d '{"type":"points"}' >/dev/null
sleep 3
BAL_AFTER=$(curl -s "$BASE/points/balance" -H "$R1" | jq -r '.balance')
check "消费者暂停: 积分未到账" "$BAL_AFTER" "$BAL_BEFORE"
CL=$(curl -s -X POST "$BASE/points/claims" -H "$R1" -H "$CT" -d "{\"order_id\":$OID5,\"note\":\"积分没到账\"}")
check "申诉后补账" "$(jqget '.status' "$CL")" "RESOLVED_CREDITED"
# 10kg纸=12元 -> 132 积分
check "补账积分=132" "$(jqget '.credited' "$CL")" "132"
CL2=$(curl -s -X POST "$BASE/points/claims" -H "$R1" -H "$CT" -d "{\"order_id\":$OID5}")
check "重复申诉=已入账" "$(jqget '.status' "$CL2")" "ALREADY_CREDITED"
ST1=$(curl -s "$BASE/orders/$OID5" -H "$R1" | jq -r '.order.status')
check "补账后 SETTLED" "$ST1" "SETTLED"

echo "=== 17. 回收员迟到 ==="
PAST_WS=$(date -u -d "@$((NOW-1800))" +%Y-%m-%dT%H:%M:%SZ)
PAST_WE=$(date -u -d "@$((NOW-60))" +%Y-%m-%dT%H:%M:%SZ)
RESP=$(curl -s -X POST "$BASE/orders" -H "$R1" -H "$CT" -d "{
  \"items\":[{\"category\":\"paper\",\"est_weight_kg\":1}],
  \"time_window_start\":\"$PAST_WS\",\"time_window_end\":\"$PAST_WE\",\"idempotency_key\":\"e2e-order-6\"}")
OID6=$(jqget '.order.id' "$RESP")
LATE=0
for i in $(seq 1 20); do
  LATE=$(curl -s "$BASE/orders/$OID6" -H "$R1" | jq -r '.order.late_minutes')
  [ "$LATE" -gt 0 ] && break
  sleep 1
done
[ "$LATE" -gt 0 ] && ok "迟到标记 late_minutes=$LATE" || bad "迟到未标记"
NOTIF=$(curl -s "$BASE/notifications" -H "$R1" | jq -r '[.notifications[] | select(.kind=="ORDER_LATE")] | length')
[ "$NOTIF" -ge 1 ] && ok "迟到通知已送达" || bad "迟到通知缺失"
CC=$(curl -s -X POST "$BASE/orders/$OID6/cancel" -H "$R1" -H "$CT" -d '{"reason":"回收员迟到, 改约"}')
check "迟到后取消" "$(jqget '.order.status' "$CC")" "CANCELLED"

echo "=== 18. 投诉 ==="
CMP=$(curl -s -X POST "$BASE/orders/$OID/complaints" -H "$R1" -H "$CT" \
  -d '{"content":"回收员称重时未去皮"}')
CMPID=$(jqget '.complaint.id' "$CMP")
curl -s -X POST "$BASE/street/complaints/$CMPID/resolve" -H "$ST" -H "$CT" \
  -d '{"resolution":"已约谈回收员, 复称无误差, 向居民解释"}' >/dev/null
CST=$(curl -s "$BASE/street/complaints?status=RESOLVED" -H "$ST" | jq -r "[.complaints[] | select(.id==$CMPID)] | length")
check "投诉已处理" "$CST" "1"

echo "=== 19. 街道视角: 参与与资源流向 ==="
STATS=$(curl -s "$BASE/street/stats?street=城东街道" -H "$ST")
[ "$(jqget '.participating_residents' "$STATS")" -ge 2 ] && ok "参与居民>=2" || bad "参与居民统计异常"
FLOWS=$(curl -s "$BASE/street/flows?street=城东街道" -H "$ST")
FLOWN=$(jqget '.flows | length' "$FLOWS")
[ "$FLOWN" -ge 3 ] && ok "资源流向 $FLOWN 条(按品类)" || bad "资源流向不足: $FLOWN"

echo "=== 20. 站点入库质量 ==="
Q=$(curl -s "$BASE/station/quality" -H "$S1")
QN=$(jqget '.quality | length' "$Q")
[ "$QN" -ge 3 ] && ok "入库质量统计 $QN 个品类" || bad "入库质量统计不足"

echo "=== 21. 市场价更新(版本化) ==="
curl -s -X POST "$BASE/street/prices" -H "$ST" -H "$CT" \
  -d '{"category":"paper","price_yuan_per_kg":1.5}' >/dev/null
P=$(curl -s "$BASE/prices" -H "$R1" | jq -r '.prices[] | select(.category=="paper") | .price_yuan_per_kg')
check "新价=1.5元" "$P" "1.5"
# 老订单价格快照不受影响: 订单1的纸单价仍是1.2
OLDP=$(curl -s "$BASE/orders/$OID" -H "$R1" | jq -r '.items[] | select(.category=="paper") | .est_price_yuan')
check "老订单价格快照=1.2元" "$OLDP" "1.2"

echo
echo "==================================="
echo "PASS=$PASS FAIL=$FAIL"
kill "$APP_PID" 2>/dev/null
[ "$FAIL" -eq 0 ] && echo "E2E_ALL_PASS" || echo "E2E_HAS_FAILURES"
exit $FAIL
