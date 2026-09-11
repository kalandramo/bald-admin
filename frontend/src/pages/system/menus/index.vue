<script lang="ts" setup>
import type { Menu } from "@/api/generated/model"
import { useConfirmAction } from "@@/composables/useConfirmAction"
import { formatDateTime } from "@@/utils/datetime"
import { menuServiceCreateMenu, menuServiceDeleteMenu, menuServiceListMenus, menuServiceUpdateMenu } from "@/api/generated/menu-service"
import { MenuStatus, MenuType } from "@/api/generated/model"

const { handleDelete } = useConfirmAction({ confirmMessage: "确认删除该菜单？子菜单将一并删除" })

const loading = ref(false)
const menuList = ref<Menu[]>([])
const dialogVisible = ref(false)
const dialogTitle = ref("")
const submitting = ref(false)
const formRef = useTemplateRef("formRef")

function defaultForm(): Menu {
  return {
    id: "",
    parent_id: "",
    // protojson 输出枚举名（后端 writePB UseProtoNames，实测 "TYPE_MENU"）
    type: MenuType.TYPE_MENU,
    name: "",
    path: "",
    component: "",
    title: "",
    icon: "",
    order: 0,
    status: MenuStatus.STATUS_ON,
    remark: "",
    created_at: "",
    updated_at: ""
  }
}
const form = reactive<Menu>(defaultForm())

const rules = {
  id: [{ required: true, message: "请输入菜单ID", trigger: "blur" }],
  name: [{ required: true, message: "请输入路由名称", trigger: "blur" }],
  title: [{ required: true, message: "请输入展示标题", trigger: "blur" }]
}

async function fetchList() {
  loading.value = true
  try {
    const res = await menuServiceListMenus()
    menuList.value = res.items || []
  } finally {
    loading.value = false
  }
}

function handleCreate() {
  Object.assign(form, defaultForm())
  dialogTitle.value = "新增菜单"
  dialogVisible.value = true
}

function handleEdit(row: Menu) {
  Object.assign(form, row)
  dialogTitle.value = "编辑菜单"
  dialogVisible.value = true
}

function handleSubmit() {
  formRef.value?.validate(async (valid: boolean) => {
    if (!valid) return
    submitting.value = true
    try {
      if (form.created_at) {
        await menuServiceUpdateMenu(form.id!, {
          parent_id: form.parent_id,
          type: form.type,
          name: form.name,
          path: form.path,
          component: form.component,
          title: form.title,
          icon: form.icon,
          order: form.order,
          order_set: true,
          status: form.status,
          remark: form.remark
        })
      } else {
        await menuServiceCreateMenu({
          id: form.id,
          parent_id: form.parent_id,
          type: form.type,
          name: form.name,
          path: form.path,
          component: form.component,
          title: form.title,
          icon: form.icon,
          order: form.order,
          remark: form.remark
        })
      }
      ElMessage.success("保存成功")
      dialogVisible.value = false
      fetchList()
    } finally {
      submitting.value = false
    }
  })
}

function handleDeleteAction(row: Menu) {
  handleDelete(() => menuServiceDeleteMenu(row.id!), fetchList)
}

function typeLabel(type?: string) {
  const map: Record<string, string> = { TYPE_CATALOG: "目录", TYPE_MENU: "菜单", TYPE_BUTTON: "按钮" }
  return map[type ?? ""] || "未知"
}

function statusTag(status?: string) {
  return status === MenuStatus.STATUS_ON
    ? { type: "success" as const, label: "启用" }
    : { type: "info" as const, label: "禁用" }
}

onMounted(fetchList)
</script>

<template>
  <div class="app-container">
    <el-card shadow="never" class="search-wrapper">
      <el-button type="primary" @click="handleCreate">
        新增菜单
      </el-button>
    </el-card>
    <el-card shadow="never">
      <el-table v-loading="loading" :data="menuList" row-key="id" border stripe default-expand-all :tree-props="{ children: 'children' }">
        <el-table-column prop="title" label="标题" />
        <el-table-column prop="name" label="路由名称" align="center" />
        <el-table-column label="类型" align="center" width="80">
          <template #default="{ row }">
            <el-tag size="small">
              {{ typeLabel(row.type) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="path" label="路由路径" align="center" />
        <el-table-column prop="component" label="组件路径" align="center" />
        <el-table-column prop="icon" label="图标" align="center" width="60" />
        <el-table-column label="状态" align="center" width="80">
          <template #default="{ row }">
            <el-tag :type="statusTag(row.status).type" size="small">
              {{ statusTag(row.status).label }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="创建时间" align="center" width="180">
          <template #default="{ row }">
            {{ formatDateTime(row.created_at) }}
          </template>
        </el-table-column>
        <el-table-column label="操作" align="center" width="180">
          <template #default="{ row }">
            <el-button type="primary" link @click="handleEdit(row as Menu)">
              编辑
            </el-button>
            <el-button type="danger" link @click="handleDeleteAction(row as Menu)">
              删除
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-dialog v-model="dialogVisible" :title="dialogTitle" width="550px" :close-on-click-modal="false">
      <el-form ref="formRef" :model="form" :rules="rules" label-width="100px">
        <el-form-item label="菜单ID" prop="id">
          <el-input v-model="form.id" placeholder="如 menu-system" :disabled="!!form.created_at" />
        </el-form-item>
        <el-form-item label="父级ID" prop="parent_id">
          <el-input v-model="form.parent_id" placeholder="留空为根节点" />
        </el-form-item>
        <el-form-item label="类型" prop="type">
          <el-radio-group v-model="form.type">
            <el-radio :value="MenuType.TYPE_CATALOG">
              目录
            </el-radio>
            <el-radio :value="MenuType.TYPE_MENU">
              菜单
            </el-radio>
            <el-radio :value="MenuType.TYPE_BUTTON">
              按钮
            </el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="路由名称" prop="name">
          <el-input v-model="form.name" placeholder="对应 vue router name" />
        </el-form-item>
        <el-form-item label="展示标题" prop="title">
          <el-input v-model="form.title" placeholder="菜单显示名称" />
        </el-form-item>
        <el-form-item label="路由路径" prop="path">
          <el-input v-model="form.path" placeholder="如 /system/menu" />
        </el-form-item>
        <el-form-item label="组件路径" prop="component">
          <el-input v-model="form.component" placeholder="如 system/menus/index.vue" />
        </el-form-item>
        <el-form-item label="图标" prop="icon">
          <el-input v-model="form.icon" placeholder="如 menu" />
        </el-form-item>
        <el-form-item label="排序" prop="order">
          <el-input-number v-model="form.order" :min="0" />
        </el-form-item>
        <el-form-item label="状态" prop="status">
          <el-radio-group v-model="form.status">
            <el-radio :value="MenuStatus.STATUS_ON">
              启用
            </el-radio>
            <el-radio :value="MenuStatus.STATUS_OFF">
              禁用
            </el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="备注" prop="remark">
          <el-input v-model="form.remark" type="textarea" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">
          取消
        </el-button>
        <el-button type="primary" :loading="submitting" @click="handleSubmit">
          确定
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style lang="scss" scoped>
.app-container {
  padding: 20px;
}
.search-wrapper {
  margin-bottom: 20px;
}
</style>
