import { whoamiApi } from "@@/apis/auth"
import { setToken as _setToken, getToken, removeToken } from "@@/utils/local-storage"
import { pinia } from "@/pinia"
import { resetRouter, router } from "@/router"
import { useSettingsStore } from "./settings"
import { useTagsViewStore } from "./tags-view"

export const useUserStore = defineStore("user", () => {
  const token = ref<string>(getToken() || "")
  const roles = ref<string[]>([])
  const permissions = ref<string[]>([])
  const username = ref<string>("")
  const userId = ref<string>("")
  const tenantId = ref<string>("")
  const isGotUserInfo = ref<boolean>(false)

  const tagsViewStore = useTagsViewStore()
  const settingsStore = useSettingsStore()

  // 设置 Token
  const setToken = (value: string) => {
    _setToken(value)
    token.value = value
  }

  // 获取用户详情
  const getInfo = async () => {
    const res = await whoamiApi()
    username.value = res.username
    userId.value = res.user_id
    tenantId.value = res.tenant_id
    roles.value = res.roles ?? []
    permissions.value = [] // 后端暂未返回 permissions，保留字段
    isGotUserInfo.value = true
  }

  // 登出
  const logout = () => {
    resetToken()
    resetRouter()
    resetTagsView()
    router.replace("/login")
  }

  // 重置 Token
  const resetToken = () => {
    removeToken()
    token.value = ""
    roles.value = []
    permissions.value = []
    username.value = ""
    userId.value = ""
    tenantId.value = ""
    isGotUserInfo.value = false
  }

  // 重置 Visited Views 和 Cached Views
  const resetTagsView = () => {
    if (!settingsStore.cacheTagsView) {
      tagsViewStore.delAllVisitedViews()
      tagsViewStore.delAllCachedViews()
    }
  }

  return {
    token,
    roles,
    permissions,
    username,
    userId,
    tenantId,
    isGotUserInfo,
    setToken,
    getInfo,
    logout,
    resetToken
  }
})

export function useUserStoreOutside() {
  return useUserStore(pinia)
}
