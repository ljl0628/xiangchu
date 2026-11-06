<template>
  <el-container class="layout">
    <el-aside width="220px" class="aside">
      <div class="logo">{{ auth.role === 'supplier' ? '乡厨供应商工作台' : '乡厨管理后台' }}</div>
      <el-menu
        router
        class="menu"
        :default-active="activeMenu"
        background-color="#001529"
        text-color="#a6adb4"
        active-text-color="#ffffff"
      >
        <!-- 供应商菜单 -->
        <template v-if="auth.role === 'supplier'">
          <el-menu-item index="/sup/ingredients">
            <el-icon><Goods /></el-icon>
            <span>商品管理</span>
          </el-menu-item>
          <el-menu-item index="/sup/purchases">
            <el-icon><ShoppingCart /></el-icon>
            <span>采购履约</span>
          </el-menu-item>
          <el-menu-item index="/sup/finance">
            <el-icon><Money /></el-icon>
            <span>收入与提现</span>
          </el-menu-item>
        </template>

        <!-- 平台管理员菜单 -->
        <template v-else>
          <el-menu-item index="/dashboard">
            <el-icon><Odometer /></el-icon>
            <span>仪表盘</span>
          </el-menu-item>
          <el-menu-item index="/users">
            <el-icon><User /></el-icon>
            <span>用户管理</span>
          </el-menu-item>
          <el-menu-item index="/chefs">
            <el-icon><Food /></el-icon>
            <span>乡厨管理</span>
          </el-menu-item>
          <el-menu-item index="/suppliers">
            <el-icon><Shop /></el-icon>
            <span>供应商管理</span>
          </el-menu-item>
          <el-menu-item index="/orders">
            <el-icon><List /></el-icon>
            <span>订单管理</span>
          </el-menu-item>
          <el-menu-item index="/aftersales">
            <el-icon><Service /></el-icon>
            <span>售后管理</span>
          </el-menu-item>
          <el-menu-item index="/reviews">
            <el-icon><ChatDotRound /></el-icon>
            <span>评价管理</span>
          </el-menu-item>
          <el-menu-item index="/purchases">
            <el-icon><ShoppingCart /></el-icon>
            <span>采购管理</span>
          </el-menu-item>
          <el-menu-item index="/coupons">
            <el-icon><Ticket /></el-icon>
            <span>优惠券管理</span>
          </el-menu-item>
          <el-sub-menu index="finance">
            <template #title>
              <el-icon><Money /></el-icon>
              <span>财务管理</span>
            </template>
            <el-menu-item index="/finance/settlements">结算记录</el-menu-item>
            <el-menu-item index="/finance/withdrawals">提现审核</el-menu-item>
            <el-menu-item index="/finance/settings">费率设置</el-menu-item>
          </el-sub-menu>
          <el-menu-item index="/oper-logs">
            <el-icon><Document /></el-icon>
            <span>操作日志</span>
          </el-menu-item>
        </template>
      </el-menu>
    </el-aside>

    <el-container class="right">
      <el-header class="header" height="56px">
        <el-breadcrumb separator="/">
          <el-breadcrumb-item :to="{ path: '/' }">首页</el-breadcrumb-item>
          <el-breadcrumb-item v-if="route.meta.title">{{ route.meta.title }}</el-breadcrumb-item>
        </el-breadcrumb>
        <div class="header-right">
          <span class="username">
            <el-icon><UserFilled /></el-icon>
            {{ auth.username || '管理员' }}
          </span>
          <el-button type="danger" text @click="onLogout">
            <el-icon><SwitchButton /></el-icon>
            退出登录
          </el-button>
        </div>
      </el-header>
      <el-main class="main">
        <router-view />
      </el-main>
    </el-container>
  </el-container>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessageBox } from 'element-plus'
import {
  ChatDotRound,
  Document,
  Food,
  Goods,
  List,
  Money,
  Odometer,
  Service,
  Shop,
  ShoppingCart,
  SwitchButton,
  Ticket,
  User,
  UserFilled,
} from '@element-plus/icons-vue'

import { useAuthStore } from '@/stores/auth'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const activeMenu = computed(() => route.path)

async function onLogout() {
  try {
    await ElMessageBox.confirm('确定要退出登录吗？', '提示', {
      confirmButtonText: '退出',
      cancelButtonText: '取消',
      type: 'warning',
    })
  } catch {
    return
  }
  auth.logout()
  router.push('/login')
}
</script>

<style scoped>
.layout {
  height: 100%;
}

.aside {
  background-color: #001529;
  overflow-y: auto;
}

.logo {
  height: 56px;
  line-height: 56px;
  text-align: center;
  color: #fff;
  font-size: 16px;
  font-weight: 600;
  letter-spacing: 1px;
  white-space: nowrap;
  overflow: hidden;
}

.menu {
  border-right: none;
}

.right {
  min-width: 0;
}

.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  background: #fff;
  border-bottom: 1px solid #e6e8eb;
}

.header-right {
  display: flex;
  align-items: center;
  gap: 16px;
}

.username {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: #303133;
  font-size: 14px;
}

.main {
  background-color: #f5f7fa;
  padding: 16px;
  overflow-y: auto;
}
</style>
