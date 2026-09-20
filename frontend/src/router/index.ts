import type { RouteRecordRaw } from "vue-router"
import { createRouter } from "vue-router"
import { DASHBOARD_PATH, REDIRECT_PATH, routerConfig } from "@/router/config"
import { registerNavigationGuard } from "@/router/guard"
import { flatMultiLevelRoutes } from "./helper"

const Layouts = () => import("@/layouts/index.vue")

/**
 * 静态路由
 */
export const constantRoutes: RouteRecordRaw[] = [
  {
    path: REDIRECT_PATH,
    component: Layouts,
    meta: { hidden: true },
    children: [
      {
        path: ":path(.*)",
        component: () => import("@/pages/redirect/index.vue")
      }
    ]
  },
  {
    path: "/403",
    component: () => import("@/pages/error/403.vue"),
    meta: { hidden: true }
  },
  {
    path: "/404",
    component: () => import("@/pages/error/404.vue"),
    meta: { hidden: true },
    alias: "/:pathMatch(.*)*"
  },
  {
    path: "/login",
    component: () => import("@/pages/login/index.vue"),
    meta: { hidden: true }
  },
  {
    path: "/",
    component: Layouts,
    redirect: DASHBOARD_PATH,
    children: [
      {
        path: "dashboard",
        component: () => import("@/pages/dashboard/index.vue"),
        name: "Dashboard",
        meta: {
          title: "首页",
          svgIcon: "dashboard",
          affix: true
        }
      }
    ]
  },
  {
    path: "/link",
    meta: {
      title: "文档链接",
      elIcon: "Link"
    },
    children: [
      {
        path: "https://github.com/kalandramo/bald",
        component: () => {},
        name: "Link1",
        meta: { title: "Bald 框架" }
      }
    ]
  }
]

/**
 * 动态路由（带权限控制）
 */
export const dynamicRoutes: RouteRecordRaw[] = [
  // 租户管理
  {
    path: "/tenants",
    component: Layouts,
    redirect: "/tenants/index",
    name: "Tenants",
    meta: {
      title: "租户管理",
      elIcon: "OfficeBuilding",
      roles: ["admin"]
    },
    children: [
      {
        path: "index",
        component: () => import("@/pages/tenants/index.vue"),
        name: "TenantsIndex",
        meta: {
          title: "租户列表",
          elIcon: "List",
          roles: ["admin"]
        }
      }
    ]
  },
  // 用户管理
  {
    path: "/users",
    component: Layouts,
    redirect: "/users/index",
    name: "Users",
    meta: {
      title: "用户管理",
      elIcon: "User",
      roles: ["admin"]
    },
    children: [
      {
        path: "index",
        component: () => import("@/pages/users/index.vue"),
        name: "UsersIndex",
        meta: {
          title: "用户列表",
          elIcon: "List",
          roles: ["admin"]
        }
      }
    ]
  },
  // 系统管理
  {
    path: "/system",
    component: Layouts,
    redirect: "/system/menus",
    name: "System",
    meta: {
      title: "系统管理",
      elIcon: "Setting",
      alwaysShow: true,
      roles: ["admin"]
    },
    children: [
      {
        path: "menus",
        component: () => import("@/pages/system/menus/index.vue"),
        name: "SystemMenus",
        meta: {
          title: "菜单管理",
          elIcon: "Menu",
          roles: ["admin"]
        }
      },
      {
        path: "permissions",
        component: () => import("@/pages/system/permissions/index.vue"),
        name: "SystemPermissions",
        meta: {
          title: "权限管理",
          elIcon: "Lock",
          roles: ["admin"]
        }
      },
      {
        path: "languages",
        component: () => import("@/pages/system/languages/index.vue"),
        name: "SystemLanguages",
        meta: {
          title: "语言管理",
          elIcon: "Notification",
          roles: ["admin"]
        }
      },
      {
        path: "cache-monitor",
        component: () => import("@/pages/system/cache-monitor/index.vue"),
        name: "SystemCacheMonitor",
        meta: {
          title: "缓存监控",
          elIcon: "Odometer",
          roles: ["admin"]
        }
      }
    ]
  },
  // 字典管理
  {
    path: "/dicts",
    component: Layouts,
    redirect: "/dicts/index",
    name: "Dicts",
    meta: {
      title: "字典管理",
      elIcon: "Collection",
      roles: ["admin"]
    },
    children: [
      {
        path: "index",
        component: () => import("@/pages/dicts/index.vue"),
        name: "DictsIndex",
        meta: {
          title: "字典列表",
          elIcon: "List",
          roles: ["admin"]
        }
      }
    ]
  },
  // 文件管理
  {
    path: "/files",
    component: Layouts,
    redirect: "/files/index",
    name: "Files",
    meta: {
      title: "文件管理",
      elIcon: "Folder",
      roles: ["admin"]
    },
    children: [
      {
        path: "index",
        component: () => import("@/pages/files/index.vue"),
        name: "FilesIndex",
        meta: {
          title: "文件列表",
          elIcon: "List",
          roles: ["admin"]
        }
      }
    ]
  },
  // 审计日志
  {
    path: "/audits",
    component: Layouts,
    redirect: "/audits/index",
    name: "Audits",
    meta: {
      title: "审计日志",
      elIcon: "Document",
      roles: ["admin"]
    },
    children: [
      {
        path: "index",
        component: () => import("@/pages/audits/index.vue"),
        name: "AuditsIndex",
        meta: {
          title: "日志查询",
          elIcon: "Search",
          roles: ["admin"]
        }
      }
    ]
  },
  // 组织架构
  {
    path: "/org",
    component: Layouts,
    redirect: "/org/index",
    name: "Org",
    meta: {
      title: "组织架构",
      elIcon: "OfficeBuilding",
      alwaysShow: true,
      roles: ["admin"]
    },
    children: [
      {
        path: "index",
        component: () => import("@/pages/org/index.vue"),
        name: "OrgUnits",
        meta: {
          title: "组织单元",
          elIcon: "List",
          roles: ["admin"]
        }
      },
      {
        path: "positions",
        component: () => import("@/pages/org/positions/index.vue"),
        name: "OrgPositions",
        meta: {
          title: "岗位管理",
          elIcon: "Postcard",
          roles: ["admin"]
        }
      }
    ]
  },
  // 套餐配额
  {
    path: "/plans",
    component: Layouts,
    redirect: "/plans/index",
    name: "Plans",
    meta: {
      title: "套餐配额",
      elIcon: "Goods",
      alwaysShow: true,
      roles: ["admin"]
    },
    children: [
      {
        path: "index",
        component: () => import("@/pages/plans/index.vue"),
        name: "PlansIndex",
        meta: {
          title: "套餐管理",
          elIcon: "List",
          roles: ["admin"]
        }
      },
      {
        path: "modules",
        component: () => import("@/pages/plans/modules/index.vue"),
        name: "PlanModules",
        meta: {
          title: "套餐模块",
          elIcon: "Grid",
          roles: ["admin"]
        }
      },
      {
        path: "quotas",
        component: () => import("@/pages/plans/quotas/index.vue"),
        name: "PlanQuotas",
        meta: {
          title: "套餐配额",
          elIcon: "Histogram",
          roles: ["admin"]
        }
      }
    ]
  },
  // 身份凭证
  {
    path: "/identity",
    component: Layouts,
    redirect: "/identity/credentials",
    name: "Identity",
    meta: {
      title: "身份凭证",
      elIcon: "Key",
      alwaysShow: true,
      roles: ["admin"]
    },
    children: [
      {
        path: "credentials",
        component: () => import("@/pages/identity/credentials/index.vue"),
        name: "IdentityCredentials",
        meta: {
          title: "用户凭证",
          elIcon: "Postcard",
          roles: ["admin"]
        }
      },
      {
        path: "policies",
        component: () => import("@/pages/identity/policies/index.vue"),
        name: "IdentityPolicies",
        meta: {
          title: "登录策略",
          elIcon: "Lock",
          roles: ["admin"]
        }
      }
    ]
  },
  // 多因素认证
  {
    path: "/mfa",
    component: Layouts,
    redirect: "/mfa/index",
    name: "MFA",
    meta: {
      title: "多因素认证",
      elIcon: "Lock",
      roles: ["admin"]
    },
    children: [
      {
        path: "index",
        component: () => import("@/pages/mfa/index.vue"),
        name: "MFAIndex",
        meta: {
          title: "MFA 设置",
          elIcon: "List",
          roles: ["admin"]
        }
      }
    ]
  },
  // 站内消息
  {
    path: "/messages",
    component: Layouts,
    redirect: "/messages/index",
    name: "Messages",
    meta: {
      title: "站内消息",
      elIcon: "ChatDotRound",
      alwaysShow: true,
      roles: ["admin"]
    },
    children: [
      {
        path: "index",
        component: () => import("@/pages/messages/index.vue"),
        name: "MessagesIndex",
        meta: {
          title: "消息管理",
          elIcon: "List",
          roles: ["admin"]
        }
      },
      {
        path: "inbox",
        component: () => import("@/pages/messages/inbox.vue"),
        name: "MessagesInbox",
        meta: {
          title: "我的收件箱",
          elIcon: "Message",
          roles: ["admin"]
        }
      }
    ]
  },
  // 任务调度
  {
    path: "/tasks",
    component: Layouts,
    redirect: "/tasks/index",
    name: "Tasks",
    meta: {
      title: "任务调度",
      elIcon: "Timer",
      roles: ["admin"]
    },
    children: [
      {
        path: "index",
        component: () => import("@/pages/tasks/index.vue"),
        name: "TasksIndex",
        meta: {
          title: "任务列表",
          elIcon: "List",
          roles: ["admin"]
        }
      }
    ]
  }
]

/** 路由实例 */
export const router = createRouter({
  history: routerConfig.history,
  routes: routerConfig.thirdLevelRouteCache ? flatMultiLevelRoutes(constantRoutes) : constantRoutes
})

/** 重置路由 */
export function resetRouter() {
  try {
    router.getRoutes().forEach((route) => {
      const { name, meta } = route
      if (name && (meta.roles?.length || meta.permissions?.length)) {
        router.hasRoute(name) && router.removeRoute(name)
      }
    })
  } catch {
    location.reload()
  }
}

// 注册路由导航守卫
registerNavigationGuard(router)
