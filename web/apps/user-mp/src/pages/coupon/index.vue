<template>
  <view class="coupon-page">
    <view class="tabs">
      <view class="tab" :class="{ active: tab === 'center' }" @click="switchTab('center')">领券中心</view>
      <view class="tab" :class="{ active: tab === 'mine' }" @click="switchTab('mine')">我的优惠券</view>
    </view>

    <!-- 领券中心 -->
    <view v-if="tab === 'center'" class="page-pad">
      <view v-for="c in center" :key="c.id" class="card coupon-card">
        <view class="coupon-left">
          <text class="coupon-amount">¥{{ yuan(c.amount) }}</text>
          <text class="coupon-cond">满{{ yuan(c.threshold) }}可用</text>
        </view>
        <view class="grow coupon-mid">
          <view class="strong">{{ c.name }}</view>
          <view class="muted">满减券 · {{ fmtDate(c.valid_to) }} 前有效</view>
        </view>
        <view class="btn-primary btn-mini" :class="{ disabled: c.received }" @click="receive(c)">
          {{ c.received ? '已领取' : '领取' }}
        </view>
      </view>
      <view v-if="!center.length && !loading" class="empty">暂无可领优惠券</view>
      <view v-if="center.length && !centerFinished" class="btn-plain load-more" @click="loadCenter">
        {{ loading ? '加载中...' : '加载更多' }}
      </view>
    </view>

    <!-- 我的优惠券 -->
    <view v-else class="page-pad">
      <view v-for="c in mine" :key="c.id" class="card coupon-card" :class="{ used: c.status !== 0 }">
        <view class="coupon-left">
          <text class="coupon-amount">¥{{ yuan(c.amount) }}</text>
          <text class="coupon-cond">满{{ yuan(c.threshold) }}可用</text>
        </view>
        <view class="grow coupon-mid">
          <view class="strong">{{ c.name }}</view>
          <view class="muted">{{ fmtDate(c.expire_at) }} 前有效</view>
        </view>
        <text class="tag" :class="c.status === 0 ? 'tag-green' : 'tag-grey'">
          {{ c.status === 0 ? '可用' : c.status === 2 ? '使用中' : '已使用' }}
        </text>
      </view>
      <view v-if="!mine.length && !loading" class="empty">还没有优惠券，去领券中心看看</view>
    </view>
  </view>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { onLoad, onShow } from '@dcloudio/uni-app'
import type { Coupon, UserCoupon } from '@xc/shared/types'
import { userApi } from '@/utils/api'
import { errMsg, yuan } from '@/utils/format'
import { useSession } from '@/stores/session'

const tab = ref<'center' | 'mine'>('center')
const center = ref<Coupon[]>([])
const centerPage = ref(1)
const centerFinished = ref(false)
const mine = ref<UserCoupon[]>([])
const loading = ref(false)

const { isLogged } = useSession()

function fmtDate(s: string): string {
  return (s || '').slice(0, 10)
}

function loadCenter() {
  if (!isLogged.value) {
    center.value = []
    return
  }
  if (loading.value || centerFinished.value) return
  loading.value = true
  userApi
    .couponCenter(centerPage.value, 10)
    .then((res) => {
      const items = res.list || []
      center.value = centerPage.value === 1 ? items : center.value.concat(items)
      centerFinished.value = items.length < 10
    })
    .catch((e: unknown) => uni.showToast({ title: errMsg(e), icon: 'none' }))
    .finally(() => (loading.value = false))
}

function loadMine() {
  if (!isLogged.value) {
    mine.value = []
    return
  }
  userApi
    .myCoupons()
    .then((res) => (mine.value = res || []))
    .catch((e: unknown) => uni.showToast({ title: errMsg(e), icon: 'none' }))
}

function switchTab(t: 'center' | 'mine') {
  tab.value = t
  if (t === 'mine') loadMine()
}

function receive(c: Coupon) {
  if (c.received) return
  userApi
    .receiveCoupon(c.id)
    .then(() => {
      c.received = true
      uni.showToast({ title: '领取成功', icon: 'success' })
    })
    .catch((e: unknown) => uni.showToast({ title: errMsg(e), icon: 'none' }))
}

onLoad((query) => {
  if (query?.tab === 'mine') tab.value = 'mine'
})
onShow(() => {
  loadCenter()
  loadMine()
})
</script>

<style scoped>
.tabs {
  display: flex;
  background: #fff;
  position: sticky;
  top: 0;
  z-index: 5;
}
.tab {
  flex: 1;
  text-align: center;
  padding: 22rpx 0;
  font-size: 28rpx;
  color: #666;
  position: relative;
}
.tab.active {
  color: #d97706;
  font-weight: 700;
}
.tab.active::after {
  content: '';
  position: absolute;
  left: 50%;
  transform: translateX(-50%);
  bottom: 0;
  width: 48rpx;
  height: 6rpx;
  border-radius: 4rpx;
  background: #d97706;
}
.coupon-card {
  display: flex;
  align-items: center;
}
.coupon-card.used {
  opacity: 0.55;
}
.coupon-left {
  display: flex;
  flex-direction: column;
  align-items: center;
  padding-right: 24rpx;
  border-right: 1rpx dashed #eee;
  margin-right: 24rpx;
  min-width: 150rpx;
}
.coupon-amount {
  color: #d97706;
  font-size: 44rpx;
  font-weight: 700;
}
.coupon-cond {
  color: #999;
  font-size: 20rpx;
}
.coupon-mid {
  min-width: 0;
}
.load-more {
  margin-top: 8rpx;
}
</style>
