<template>
  <div>
    <el-card shadow="never" class="page-card">
      <div class="table-toolbar">
        <el-select v-model="status" placeholder="状态" clearable style="width: 140px" @change="onSearch">
          <el-option
            v-for="(meta, key) in couponStatusMap"
            :key="key"
            :label="meta.label"
            :value="Number(key)"
          />
        </el-select>
        <el-button type="primary" :icon="Search" @click="onSearch">搜索</el-button>
        <el-button type="success" :icon="Plus" @click="openEdit(null)">新建优惠券</el-button>
      </div>

      <el-table v-loading="loading" :data="list" stripe>
        <el-table-column prop="id" label="ID" width="70" />
        <el-table-column prop="name" label="名称" min-width="160" />
        <el-table-column label="面额" width="100">
          <template #default="{ row }">¥{{ money(row.amount) }}</template>
        </el-table-column>
        <el-table-column label="门槛" width="120">
          <template #default="{ row }">
            {{ row.threshold > 0 ? `满¥${money(row.threshold)}` : '无门槛' }}
          </template>
        </el-table-column>
        <el-table-column label="有效期" min-width="300">
          <template #default="{ row }">
            {{ fmtTime(row.valid_from) }} ~ {{ fmtTime(row.valid_to) }}
          </template>
        </el-table-column>
        <el-table-column label="已领/总量" width="110">
          <template #default="{ row }">{{ row.received_count ?? 0 }} / {{ row.total_count ?? '-' }}</template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="statusMeta(couponStatusMap, row.status).type">
              {{ statusMeta(couponStatusMap, row.status).label }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="创建时间" min-width="170">
          <template #default="{ row }">{{ fmtTime(row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="180" fixed="right">
          <template #default="{ row }">
            <el-button type="primary" link @click="openEdit(row)">编辑</el-button>
            <el-button :type="row.status === 1 ? 'warning' : 'success'" link @click="toggleStatus(row)">
              {{ row.status === 1 ? '下架' : '上架' }}
            </el-button>
            <el-button type="success" link :disabled="row.status !== 1" @click="openGrant(row)">发放</el-button>
          </template>
        </el-table-column>
      </el-table>

      <div class="pagination-wrap">
        <el-pagination
          v-model:current-page="page"
          layout="total, prev, pager, next"
          :total="total"
          :page-size="size"
          @current-change="load"
        />
      </div>
    </el-card>

    <!-- 新建/编辑对话框 -->
    <el-dialog v-model="editVisible" :title="editForm.id ? '编辑优惠券' : '新建优惠券'" width="520px" destroy-on-close>
      <el-form label-width="90px">
        <el-form-item label="名称" required>
          <el-input v-model="editForm.name" maxlength="64" placeholder="如：满100减20" />
        </el-form-item>
        <el-form-item label="类型" required>
          <el-radio-group v-model="editForm.type">
            <el-radio :value="1">满减券</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="面额（元）" required>
          <el-input-number v-model="editForm.amountYuan" :min="0.01" :precision="2" :step="1" style="width: 180px" />
        </el-form-item>
        <el-form-item label="门槛（元）">
          <el-input-number v-model="editForm.thresholdYuan" :min="0" :precision="2" :step="1" style="width: 180px" />
          <span class="form-tip">0 表示无门槛</span>
        </el-form-item>
        <el-form-item label="发放总量" required>
          <el-input-number v-model="editForm.totalCount" :min="1" :step="10" style="width: 180px" />
          <span v-if="editForm.id" class="form-tip">不可低于已领 {{ editForm.receivedCount }} 张</span>
        </el-form-item>
        <el-form-item label="有效期" required>
          <el-date-picker
            v-model="editForm.range"
            type="datetimerange"
            start-placeholder="开始时间"
            end-placeholder="结束时间"
            style="width: 360px"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="editVisible = false">取消</el-button>
        <el-button type="primary" :loading="editLoading" @click="submitEdit">确定</el-button>
      </template>
    </el-dialog>

    <!-- 发放对话框 -->
    <el-dialog v-model="grantVisible" title="手动发放" width="520px" destroy-on-close>
      <el-form label-width="90px">
        <el-form-item label="优惠券">
          {{ grantCoupon?.name }}（剩余 {{ grantRemain }} 张）
        </el-form-item>
        <el-form-item label="目标用户" required>
          <el-select
            v-model="grantForm.userIds"
            multiple
            filterable
            remote
            reserve-keyword
            :remote-method="searchUsers"
            :loading="userSearching"
            placeholder="输入手机号/昵称搜索"
            style="width: 100%"
          >
            <el-option
              v-for="u in userOptions"
              :key="u.id"
              :label="`${u.nickname || u.phone}（${u.phone}）`"
              :value="u.id"
            />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="grantVisible = false">取消</el-button>
        <el-button type="primary" :loading="grantLoading" @click="submitGrant">发放</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Search } from '@element-plus/icons-vue'
import type { Coupon } from '@xc/shared/types'

import { api } from '@/api/client'
import { couponStatusMap, fmtTime, money, statusMeta } from '@/utils/format'

const loading = ref(false)
const status = ref<number | undefined>(undefined)
const page = ref(1)
const size = 20
const total = ref(0)
const list = ref<Coupon[]>([])

async function load() {
  loading.value = true
  try {
    const res = await api.coupons({ status: status.value, page: page.value, size })
    list.value = res.list ?? []
    total.value = res.total ?? 0
  } catch {
    // 拦截器已提示
  } finally {
    loading.value = false
  }
}

function onSearch() {
  page.value = 1
  load()
}

// ---------- 新建/编辑 ----------

const editVisible = ref(false)
const editLoading = ref(false)
const editForm = reactive({
  id: 0,
  name: '',
  type: 1,
  amountYuan: 10,
  thresholdYuan: 0,
  totalCount: 100,
  receivedCount: 0,
  range: [] as Date[],
})

function openEdit(row: Coupon | null) {
  editForm.id = row?.id ?? 0
  editForm.name = row?.name ?? ''
  editForm.type = 1 // 暂仅支持满减券
  editForm.amountYuan = row ? row.amount / 100 : 10
  editForm.thresholdYuan = row ? row.threshold / 100 : 0
  editForm.totalCount = row?.total_count ?? 100
  editForm.receivedCount = row?.received_count ?? 0
  editForm.range = row ? [new Date(row.valid_from!), new Date(row.valid_to)] : []
  editVisible.value = true
}

async function submitEdit() {
  if (!editForm.name.trim()) {
    ElMessage.warning('请填写名称')
    return
  }
  if (editForm.range.length !== 2 || !editForm.range[0] || !editForm.range[1]) {
    ElMessage.warning('请选择有效期')
    return
  }
  editLoading.value = true
  try {
    const data = {
      name: editForm.name.trim(),
      type: editForm.type,
      threshold: Math.round(editForm.thresholdYuan * 100),
      amount: Math.round(editForm.amountYuan * 100),
      valid_from: editForm.range[0].toISOString(),
      valid_to: editForm.range[1].toISOString(),
      total_count: editForm.totalCount,
    }
    if (editForm.id) {
      await api.updateCoupon(editForm.id, data)
      ElMessage.success('已保存')
    } else {
      await api.createCoupon(data)
      ElMessage.success('已创建并上架')
    }
    editVisible.value = false
    await load()
  } catch {
    // 拦截器已提示
  } finally {
    editLoading.value = false
  }
}

// ---------- 上下架 ----------

async function toggleStatus(row: Coupon) {
  const to = row.status === 1 ? 0 : 1
  const action = to === 1 ? '上架' : '下架'
  try {
    await ElMessageBox.confirm(`确定要${action}「${row.name}」吗？下架不影响已领用户在有效期内使用。`, '提示', {
      confirmButtonText: action,
      cancelButtonText: '取消',
      type: 'warning',
    })
  } catch {
    return
  }
  try {
    await api.setCouponStatus(row.id, to)
    ElMessage.success(`已${action}`)
    await load()
  } catch {
    // 拦截器已提示
  }
}

// ---------- 手动发放 ----------

const grantVisible = ref(false)
const grantLoading = ref(false)
const grantCoupon = ref<Coupon | null>(null)
const grantForm = reactive({ userIds: [] as number[] })
const userSearching = ref(false)
const userOptions = ref<{ id: number; phone: string; nickname: string }[]>([])

const grantRemain = computed(() => {
  const c = grantCoupon.value
  if (!c || c.total_count === undefined) return '-'
  return c.total_count - (c.received_count ?? 0)
})

function openGrant(row: Coupon) {
  grantCoupon.value = row
  grantForm.userIds = []
  userOptions.value = []
  grantVisible.value = true
}

async function searchUsers(keyword: string) {
  if (!keyword.trim()) {
    userOptions.value = []
    return
  }
  userSearching.value = true
  try {
    const res = await api.users({ keyword: keyword.trim(), page: 1, size: 20 })
    userOptions.value = (res.list ?? []).map((u) => ({
      id: Number(u.id),
      phone: String(u.phone ?? ''),
      nickname: String(u.nickname ?? ''),
    }))
  } catch {
    // 拦截器已提示
  } finally {
    userSearching.value = false
  }
}

async function submitGrant() {
  if (!grantCoupon.value) return
  if (grantForm.userIds.length === 0) {
    ElMessage.warning('请选择目标用户')
    return
  }
  grantLoading.value = true
  try {
    const res = await api.grantCoupon(grantCoupon.value.id, grantForm.userIds)
    ElMessage.success(`成功发放 ${res.granted} 人，跳过 ${res.skipped} 人（已持有）`)
    grantVisible.value = false
    await load()
  } catch {
    // 拦截器已提示
  } finally {
    grantLoading.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.form-tip {
  margin-left: 8px;
  color: #909399;
  font-size: 12px;
}
</style>
