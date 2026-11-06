<template>
  <view class="booking page-pad">
    <!-- 套餐 -->
    <view class="card">
      <view class="card-title">选择套餐</view>
      <view
        v-for="p in usablePackages"
        :key="p.id"
        class="pkg"
        :class="{ active: form.package_id === p.id }"
        @click="pickPackage(p.id)"
      >
        <view class="row-between">
          <text class="pkg-name">{{ p.name }}</text>
          <text class="price">¥{{ yuan(p.price_per_table) }}/桌</text>
        </view>
        <view class="muted">{{ p.banquet_type }} · {{ p.min_tables }}桌起</view>
      </view>
      <view v-if="!usablePackages.length && !loading" class="empty">该乡厨暂无可订套餐</view>
    </view>

    <!-- 宴席信息 -->
    <view class="card">
      <view class="card-title">宴席信息</view>
      <view class="form-item">
        <view class="form-label">宴席类型</view>
        <view class="chips">
          <view
            v-for="t in banquetTypes"
            :key="t"
            class="chip"
            :class="{ active: form.banquet_type === t }"
            @click="form.banquet_type = t"
          >
            {{ t }}
          </view>
        </view>
      </view>
      <view class="form-item">
        <view class="form-label">宴席日期</view>
        <view class="form-input picker-value date-entry" @click="goPickDate">
          <text :class="{ ghost: !form.banquet_time }">
            {{ form.banquet_time || '选择宴席日期（可查看乡厨档期）' }}
          </text>
          <text class="arrow">›</text>
        </view>
      </view>
      <view class="form-item">
        <view class="form-label">桌数</view>
        <input v-model="tableCountText" class="form-input" type="number" placeholder="请输入桌数" />
      </view>
      <view class="form-item">
        <view class="form-label">用餐人数</view>
        <input v-model="peopleCountText" class="form-input" type="number" :placeholder="`默认按每桌 10 人（${tableCount * 10} 人）`" />
      </view>
      <view class="form-item">
        <view class="form-label">菜单备注</view>
        <input v-model="form.menu_remark" class="form-input" placeholder="口味 / 忌口 / 加菜要求（选填）" />
      </view>
    </view>

    <!-- 开席地址 -->
    <view class="card">
      <view class="row-between">
        <view class="card-title">开席地址</view>
        <view class="link" @click="pickFromAddressBook">从地址簿选择</view>
      </view>
      <view class="form-item">
        <view class="form-label">所在地区</view>
        <picker mode="region" @change="onRegionChange">
          <view class="form-input picker-value">{{ form.region || '选择省 / 市 / 区' }}</view>
        </picker>
      </view>
      <view class="form-item">
        <view class="form-label">详细地址</view>
        <input v-model="form.address" class="form-input" placeholder="村镇 / 街道 / 门牌" />
      </view>
      <view class="form-item">
        <view class="form-label">联系人</view>
        <input v-model="form.contact_name" class="form-input" placeholder="姓名" />
      </view>
      <view class="form-item">
        <view class="form-label">联系电话</view>
        <input v-model="form.contact_phone" class="form-input" type="number" maxlength="11" placeholder="手机号" />
      </view>
    </view>

    <!-- 优惠券 -->
    <view class="card">
      <view class="card-title">优惠券</view>
      <picker :range="couponLabels" :value="couponIndex" @change="onCouponChange">
        <view class="row-between">
          <text :class="{ muted: couponIndex <= 0 }">{{ currentCouponLabel }}</text>
          <text class="muted">›</text>
        </view>
      </picker>
      <view v-if="couponDiscount > 0" class="muted discount-tip">已选券可抵扣 ¥{{ yuan(couponDiscount) }}</view>
    </view>

    <!-- 金额与支付 -->
    <view class="card">
      <view class="card-title">费用与支付</view>
      <view class="fee-row">
        <text>套餐费</text>
        <text>¥{{ yuan(subtotal) }}（¥{{ yuan(unitPrice) }}/桌 × {{ tableCount }}桌）</text>
      </view>
      <view class="fee-row">
        <text>优惠券</text>
        <text>-¥{{ yuan(couponDiscount) }}</text>
      </view>
      <view class="fee-row total">
        <text>应付总额</text>
        <text class="price">¥{{ yuan(payable) }}</text>
      </view>
      <view class="chips pay-chips">
        <view class="chip" :class="{ active: form.pay_type === 1 }" @click="form.pay_type = 1">
          定金（30% · 约¥{{ yuan(depositAmount) }}）
        </view>
        <view class="chip" :class="{ active: form.pay_type === 2 }" @click="form.pay_type = 2">
          全款 ¥{{ yuan(payable) }}
        </view>
      </view>
    </view>

    <button class="btn-primary submit" :class="{ disabled: submitting }" @click="submit">
      {{ form.pay_type === 1 ? `提交并支付定金 ¥${yuan(depositAmount)}` : `提交并支付全款 ¥${yuan(payable)}` }}
    </button>
    <view class="bottom-space"></view>
  </view>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { onLoad, onShow } from '@dcloudio/uni-app'
import type { Address, ChefPackage, UserCoupon } from '@xc/shared/types'
import { chefPublicApi, orderApi, userApi } from '@/utils/api'
import { BANQUET_TYPES, errMsg, today, yuan } from '@/utils/format'
import { handlePayResult } from '@/utils/pay'
import { useSession } from '@/stores/session'

const banquetTypes = BANQUET_TYPES

const { state, isLogged } = useSession()

const chefId = ref(0)
const loading = ref(true)
const packages = ref<ChefPackage[]>([])

const tableCountText = ref('')
const peopleCountText = ref('')
const couponIndex = ref(0) // 0 = 不使用
const myCoupons = ref<UserCoupon[]>([])
const submitting = ref(false)

const form = ref({
  package_id: 0,
  banquet_type: banquetTypes[0],
  banquet_time: '',
  region: '',
  address: '',
  contact_name: '',
  contact_phone: '',
  menu_remark: '',
  pay_type: 1 as 1 | 2,
})

const usablePackages = computed(() => packages.value.filter((p) => p.status === 1))

const selectedPackage = computed(() => usablePackages.value.find((p) => p.id === form.value.package_id) ?? null)

const tableCount = computed(() => Math.max(0, parseInt(tableCountText.value, 10) || 0))
// 用餐人数：未填写时默认按每桌 10 人估算
const peopleCount = computed(() => {
  const typed = parseInt(peopleCountText.value, 10)
  if (typed > 0) return typed
  return tableCount.value * 10
})
const unitPrice = computed(() => selectedPackage.value?.price_per_table ?? 0)
const subtotal = computed(() => unitPrice.value * tableCount.value)

// 可用券：status=0 且 threshold <= 应付金额
const availableCoupons = computed(() =>
  myCoupons.value.filter((c) => c.status === 0 && subtotal.value > 0 && c.threshold <= subtotal.value),
)

const couponLabels = computed(() => ['不使用优惠券', ...availableCoupons.value.map((c) => `${c.name}（满${yuan(c.threshold)}减${yuan(c.amount)}）`)])

const couponDiscount = computed(() => {
  if (couponIndex.value <= 0) return 0
  const c = availableCoupons.value[couponIndex.value - 1]
  if (!c) return 0
  return Math.min(c.amount, subtotal.value)
})

const payable = computed(() => Math.max(0, subtotal.value - couponDiscount.value))
const depositAmount = computed(() => Math.round((payable.value * 30) / 100))

const currentCouponLabel = computed(() => couponLabels.value[couponIndex.value] || '不使用优惠券')

function pickPackage(id: number) {
  form.value.package_id = id
  couponIndex.value = 0 // 套餐变化后应付金额变化，重置券选择
}

function goPickDate() {
  uni.$once('selectDate', (d: string) => {
    form.value.banquet_time = d
  })
  uni.navigateTo({
    url: `/pages/booking/date?chef_id=${chefId.value}`,
    fail: () => uni.showToast({ title: '日期页打开失败，请在 DevTools 重新编译', icon: 'none' }),
  })
}

function onRegionChange(e: { detail: { value: string[] } }) {
  const v = e.detail.value || []
  form.value.region = v.filter(Boolean).join(' ')
}

function onCouponChange(e: { detail: { value: number } }) {
  couponIndex.value = Number(e.detail.value) || 0
}

function pickFromAddressBook() {
  uni.$once('selectAddress', (addr: Address) => {
    form.value.region = addr.region
    form.value.address = addr.address
    form.value.contact_name = addr.contact_name
    form.value.contact_phone = addr.contact_phone
  })
  uni.navigateTo({ url: '/pages/address/index?select=1' })
}

function loadPackages() {
  loading.value = true
  chefPublicApi
    .detail(chefId.value)
    .then((res) => {
      packages.value = res.packages || []
      // 若从详情页带入预选套餐则使用，否则默认第一个
      if (!usablePackages.value.some((p) => p.id === form.value.package_id)) {
        form.value.package_id = preselect.value || usablePackages.value[0]?.id || 0
      }
    })
    .catch((e: unknown) => uni.showToast({ title: errMsg(e), icon: 'none' }))
    .finally(() => (loading.value = false))
}

function loadCoupons() {
  if (!isLogged.value) return
  userApi
    .myCoupons()
    .then((res) => (myCoupons.value = res || []))
    .catch(() => {
      /* 券加载失败不阻塞下单 */
    })
}

const preselect = ref(0)

onLoad((query) => {
  chefId.value = Number(query?.chef_id || 0)
  preselect.value = Number(query?.package_id || 0)
  if (preselect.value) form.value.package_id = preselect.value
  if (!chefId.value) {
    uni.showToast({ title: '缺少乡厨参数', icon: 'none' })
    setTimeout(() => uni.navigateBack(), 800)
    loading.value = false
    return
  }
  loadPackages()
})

onShow(() => {
  if (chefId.value) loadCoupons()
})

function validate(): boolean {
  if (!isLogged.value || !state.token) {
    uni.showToast({ title: '请先登录', icon: 'none' })
    uni.reLaunch({ url: '/pages/login/index' })
    return false
  }
  if (!selectedPackage.value) {
    uni.showToast({ title: '请选择套餐', icon: 'none' })
    return false
  }
  if (!form.value.banquet_type) {
    uni.showToast({ title: '请选择宴席类型', icon: 'none' })
    return false
  }
  if (!form.value.banquet_time || form.value.banquet_time <= today()) {
    uni.showToast({ title: '请选择未来日期', icon: 'none' })
    return false
  }
  const minTables = selectedPackage.value.min_tables || 1
  if (tableCount.value < minTables) {
    uni.showToast({ title: `该套餐至少 ${minTables} 桌`, icon: 'none' })
    return false
  }
  if (peopleCountText.value && (parseInt(peopleCountText.value, 10) || 0) < 1) {
    uni.showToast({ title: '请填写正确的用餐人数', icon: 'none' })
    return false
  }
  if (!form.value.region || !form.value.address) {
    uni.showToast({ title: '请填写开席地址', icon: 'none' })
    return false
  }
  if (!form.value.contact_name) {
    uni.showToast({ title: '请填写联系人', icon: 'none' })
    return false
  }
  if (!/^1\d{10}$/.test(form.value.contact_phone)) {
    uni.showToast({ title: '请填写正确的联系电话', icon: 'none' })
    return false
  }
  return true
}

function submit() {
  if (submitting.value) return
  if (!validate()) return
  const pkg = selectedPackage.value as ChefPackage
  submitting.value = true
  orderApi
    .create({
      chef_id: chefId.value,
      package_id: pkg.id,
      banquet_type: form.value.banquet_type,
      banquet_time: form.value.banquet_time,
      table_count: tableCount.value,
      people_count: peopleCount.value,
      region: form.value.region,
      address: form.value.address,
      contact_name: form.value.contact_name,
      contact_phone: form.value.contact_phone,
      menu_remark: form.value.menu_remark || undefined,
      user_coupon_id: couponIndex.value > 0 ? availableCoupons.value[couponIndex.value - 1]?.id : undefined,
      pay_type: form.value.pay_type,
    })
    .then((res) => {
      // 订单创建即成立：支付取消/失败也必须进入订单详情（待支付），
      // 停留在本页易导致重复下单并重复锁档期；详情页可「去支付」继续
      return handlePayResult({ payment_no: res.payment_no, params: res.params })
        .catch(() => {
          uni.showToast({ title: '支付未完成，可稍后在订单中继续支付', icon: 'none' })
        })
        .finally(() => {
          uni.redirectTo({ url: `/pages/order/detail?id=${res.order_id}&from=pay` })
        })
    })
    .catch((e: unknown) => uni.showToast({ title: errMsg(e), icon: 'none' }))
    .finally(() => (submitting.value = false))
}
</script>

<style scoped>
.card-title {
  font-size: 30rpx;
  font-weight: 700;
  margin-bottom: 16rpx;
}
.pkg {
  border: 1rpx solid #f0f0f0;
  border-radius: 12rpx;
  padding: 20rpx;
  margin-bottom: 16rpx;
}
.pkg.active {
  border-color: #d97706;
  background: #fffbeb;
}
.pkg-name {
  font-size: 30rpx;
  font-weight: 600;
}
.picker-value {
  color: #333;
}
.date-entry {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.date-entry .ghost {
  color: #bbbbbb;
}
.date-entry .arrow {
  color: #cccccc;
}
.discount-tip {
  margin-top: 12rpx;
}
.fee-row {
  display: flex;
  justify-content: space-between;
  font-size: 26rpx;
  color: #666;
  padding: 10rpx 0;
}
.fee-row.total {
  font-size: 30rpx;
  color: #333;
  font-weight: 600;
  border-top: 1rpx solid #f0f0f0;
  margin-top: 8rpx;
  padding-top: 20rpx;
}
.pay-chips {
  margin-top: 16rpx;
}
.submit {
  margin-top: 8rpx;
}
.bottom-space {
  height: 40rpx;
}
.link {
  color: #d97706;
  font-size: 26rpx;
  margin-bottom: 16rpx;
}
</style>
