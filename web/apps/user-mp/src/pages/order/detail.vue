<template>
  <view class="order-detail page-pad">
    <template v-if="order">
      <!-- 状态卡片 / 步骤条 -->
      <view class="card">
        <view class="row-between">
          <text class="status-text">{{ statusInfo(order.status).text }}</text>
          <text v-if="order.refund_status" class="tag tag-red">{{ refundText(order.refund_status) }}</text>
        </view>

        <view v-if="order.status === 6 || order.status === 7" class="cancel-tip">
          {{ order.status === 6 ? '订单已取消' : '乡厨已拒绝该订单' }}
          <text v-if="order.cancel_reason">（原因：{{ order.cancel_reason }}）</text>
        </view>

        <view v-else class="steps">
          <view v-for="(s, i) in steps" :key="s" class="step">
            <view class="step-dot" :class="{ done: stepIndex >= i }"></view>
            <view v-if="i < steps.length - 1" class="step-line" :class="{ done: stepIndex > i }"></view>
            <text class="step-label" :class="{ done: stepIndex >= i }">{{ s }}</text>
          </view>
        </view>
      </view>

      <!-- 宴席信息 -->
      <view class="card">
        <view class="card-title">宴席信息</view>
        <view class="kv"><text class="k">订单编号</text><text selectable>{{ order.order_no }}</text></view>
        <view class="kv"><text class="k">宴席类型</text><text>{{ order.banquet_type }}</text></view>
        <view class="kv"><text class="k">开席时间</text><text>{{ fmtDay(order.banquet_time) }}</text></view>
        <view class="kv"><text class="k">桌数</text><text>{{ order.table_count }}桌</text></view>
        <view class="kv"><text class="k">开席地址</text><text>{{ order.region }} {{ order.address }}</text></view>
        <view class="kv"><text class="k">联系人</text><text>{{ order.contact_name }} {{ order.contact_phone }}</text></view>
        <view v-if="order.menu_remark" class="kv"><text class="k">菜单备注</text><text>{{ order.menu_remark }}</text></view>
      </view>

      <!-- 费用 -->
      <view class="card">
        <view class="card-title">费用信息</view>
        <view class="kv"><text class="k">订单总额</text><text class="price">¥{{ yuan(order.amount) }}</text></view>
        <view class="kv"><text class="k">已付金额</text><text>¥{{ yuan(order.paid_amount) }}</text></view>
        <view v-if="order.paid_amount < order.amount" class="kv">
          <text class="k">待付金额</text>
          <text class="price">¥{{ yuan(order.amount - order.paid_amount) }}</text>
        </view>
        <view class="kv"><text class="k">支付方式</text><text>{{ order.pay_type === 1 ? '定金30%' : '全款' }}</text></view>
        <view class="kv"><text class="k">下单时间</text><text>{{ fmtTime(order.created_at) }}</text></view>
      </view>

      <!-- 操作区 -->
      <view class="actions">
        <view v-if="order.status === 0" class="btn-plain grow" @click="cancelOrder">取消订单</view>
        <view v-if="order.status === 0" class="btn-primary grow" @click="payNow">去支付</view>
        <view v-if="order.status === 1" class="btn-plain grow" @click="cancelOrder">取消订单</view>
        <view
          v-if="[2, 3, 4].includes(order.status) && order.paid_amount < order.amount"
          class="btn-primary grow"
          @click="payBalance"
        >
          补尾款 ¥{{ yuan(order.amount - order.paid_amount) }}
        </view>
        <view v-if="order.status === 5" class="btn-plain grow" @click="goReview">评价订单</view>
        <view v-if="[2, 3, 4, 5].includes(order.status)" class="btn-primary grow" @click="goAftersale">申请售后</view>
      </view>
    </template>
    <view v-else class="empty">{{ loading ? '加载中...' : '订单不存在' }}</view>
  </view>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { onLoad, onShow } from '@dcloudio/uni-app'
import type { Order } from '@xc/shared/types'
import { orderApi } from '@/utils/api'
import { ORDER_STATUS, REFUND_STATUS, errMsg, fmtDay, fmtTime, yuan } from '@/utils/format'
import { handlePayResult } from '@/utils/pay'

const orderId = ref(0)
const order = ref<Order | null>(null)
const loading = ref(true)

const steps = ['待支付', '待接单', '已接单', '备菜中', '服务中', '已完成']
const stepIndex = computed(() => {
  const s = order.value?.status ?? 0
  if (s <= 5) return s
  return -1
})

function statusInfo(status: number) {
  return ORDER_STATUS[status] ?? { text: `状态${status}`, cls: 'tag-grey' }
}
function refundText(s: number) {
  return REFUND_STATUS[s] || ''
}

function load() {
  loading.value = true
  orderApi
    .detail(orderId.value)
    .then((res) => (order.value = res))
    .catch((e: unknown) => uni.showToast({ title: errMsg(e), icon: 'none' }))
    .finally(() => (loading.value = false))
}

onLoad((query) => {
  orderId.value = Number(query?.id || 0)
  if (!orderId.value) {
    loading.value = false
    return
  }
})
onShow(() => {
  if (orderId.value) load()
})

// 发起支付（定金/尾款/全款统一走 pay-balance 返回支付参数）
function doPay(pay: () => ReturnType<typeof orderApi.payBalance>) {
  pay()
    .then((res) => handlePayResult(res))
    .then(() => load())
    .catch((e: unknown) => {
      if ((e as Error).message !== '已取消支付') uni.showToast({ title: errMsg(e), icon: 'none' })
      load()
    })
}

function payNow() {
  doPay(() => orderApi.payBalance(orderId.value))
}
function payBalance() {
  doPay(() => orderApi.payBalance(orderId.value))
}

function cancelOrder() {
  uni.showModal({
    title: '取消订单',
    content: '确认取消该订单吗？已付定金将按规则原路退回。',
    success: (r) => {
      if (!r.confirm) return
      orderApi
        .cancel(orderId.value)
        .then(() => {
          uni.showToast({ title: '已取消', icon: 'success' })
          load()
        })
        .catch((e: unknown) => uni.showToast({ title: errMsg(e), icon: 'none' }))
    },
  })
}

function goReview() {
  uni.navigateTo({ url: `/pages/order/review?order_id=${orderId.value}` })
}
function goAftersale() {
  uni.navigateTo({ url: `/pages/order/aftersale?order_id=${orderId.value}` })
}
</script>

<style scoped>
.status-text {
  font-size: 36rpx;
  font-weight: 700;
  color: #d97706;
}
.cancel-tip {
  margin-top: 16rpx;
  color: #999;
  font-size: 26rpx;
}
.card-title {
  font-size: 30rpx;
  font-weight: 700;
  margin-bottom: 16rpx;
}
.kv {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  padding: 10rpx 0;
  font-size: 26rpx;
  gap: 24rpx;
}
.kv .k {
  color: #999;
  flex-shrink: 0;
  margin-right: 24rpx;
}
.kv text:last-child {
  text-align: right;
  word-break: break-all;
}

/* 步骤条 */
.steps {
  display: flex;
  margin-top: 28rpx;
}
.step {
  flex: 1;
  position: relative;
  text-align: center;
}
.step-dot {
  width: 18rpx;
  height: 18rpx;
  border-radius: 50%;
  background: #e5e7eb;
  margin: 0 auto;
  position: relative;
  z-index: 2;
}
.step-dot.done {
  background: #d97706;
}
.step-line {
  position: absolute;
  top: 8rpx;
  left: 50%;
  width: 100%;
  height: 4rpx;
  background: #e5e7eb;
  z-index: 1;
}
.step-line.done {
  background: #d97706;
}
.step-label {
  display: block;
  margin-top: 12rpx;
  font-size: 20rpx;
  color: #999;
}
.step-label.done {
  color: #d97706;
}
.actions {
  display: flex;
  gap: 20rpx;
  padding: 8rpx 0 60rpx;
}
</style>
