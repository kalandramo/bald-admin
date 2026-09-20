<script lang="ts" setup>
import type { Message } from "@@/apis/message/type"
import { deleteMessageApi, listMessagesApi, revokeMessageApi, sendMessageApi } from "@@/apis/message"
import { useConfirmAction } from "@@/composables/useConfirmAction"
import { formatUnixSeconds } from "@@/utils/datetime"

defineOptions({ name: "Messages" })

const { handleDelete } = useConfirmAction({ confirmMessage: "确认删除该消息？" })

const loading = ref(false)
const list = ref<Message[]>([])

// 发送弹窗
const sendVisible = ref(false)
const sending = ref(false)
const sendRef = useTemplateRef("sendRef")
const sendForm = reactive({
  title: "",
  content: "",
  type: "NOTICE",
  category_id: "",
  mode: "all" as "all" | "one" | "many",
  recipient_user_id: "",
  target_user_ids: ""
})
const sendRules = {
  title: [{ required: true, message: "请输入标题", trigger: "blur" }],
  content: [{ required: true, message: "请输入内容", trigger: "blur" }]
}

async function fetchList() {
  loading.value = true
  try {
    const res = await listMessagesApi()
    list.value = res.items || []
  } finally {
    loading.value = false
  }
}

function handleSend() {
  Object.assign(sendForm, {
    title: "",
    content: "",
    type: "NOTICE",
    category_id: "",
    mode: "all",
    recipient_user_id: "",
    target_user_ids: ""
  })
  sendVisible.value = true
}

function submitSend() {
  sendRef.value?.validate(async (valid: boolean) => {
    if (!valid) return
    sending.value = true
    try {
      await sendMessageApi({
        title: sendForm.title,
        content: sendForm.content,
        type: sendForm.type,
        category_id: sendForm.category_id,
        target_all: sendForm.mode === "all",
        recipient_user_id: sendForm.mode === "one" ? sendForm.recipient_user_id : "",
        target_user_ids: sendForm.mode === "many"
          ? sendForm.target_user_ids.split(",").map(s => s.trim()).filter(Boolean)
          : []
      })
      ElMessage.success("发送成功")
      sendVisible.value = false
      fetchList()
    } finally {
      sending.value = false
    }
  })
}

function handleRevoke(row: Message) {
  revokeMessageApi(row.id).then(() => {
    ElMessage.success("已撤回")
    fetchList()
  })
}

function handleDeleteAction(row: Message) {
  handleDelete(() => deleteMessageApi(row.id), fetchList)
}

onMounted(fetchList)
</script>

<template>
  <div class="app-container">
    <el-card shadow="never" class="search-wrapper">
      <el-button type="primary" @click="handleSend">
        发送消息
      </el-button>
    </el-card>
    <el-card shadow="never">
      <el-table v-loading="loading" :data="list" border stripe>
        <el-table-column prop="id" label="ID" />
        <el-table-column prop="title" label="标题" />
        <el-table-column prop="type" label="类型" align="center" width="100" />
        <el-table-column prop="sender_name" label="发送人" align="center" />
        <el-table-column prop="status" label="状态" align="center" width="100" />
        <el-table-column label="创建时间" align="center" width="180">
          <template #default="{ row }">
            {{ row.created_at ? formatUnixSeconds(row.created_at) : "—" }}
          </template>
        </el-table-column>
        <el-table-column label="操作" align="center" width="200">
          <template #default="{ row }">
            <el-button type="warning" link @click="handleRevoke(row as Message)">
              撤回
            </el-button>
            <el-button type="danger" link @click="handleDeleteAction(row as Message)">
              删除
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-dialog v-model="sendVisible" title="发送消息" width="560px" :close-on-click-modal="false">
      <el-form ref="sendRef" :model="sendForm" :rules="sendRules" label-width="100px">
        <el-form-item label="标题" prop="title">
          <el-input v-model="sendForm.title" />
        </el-form-item>
        <el-form-item label="内容" prop="content">
          <el-input v-model="sendForm.content" type="textarea" :rows="3" />
        </el-form-item>
        <el-form-item label="类型" prop="type">
          <el-input v-model="sendForm.type" placeholder="如 NOTICE" />
        </el-form-item>
        <el-form-item label="分类ID" prop="category_id">
          <el-input v-model="sendForm.category_id" placeholder="可空" />
        </el-form-item>
        <el-form-item label="接收范围">
          <el-radio-group v-model="sendForm.mode">
            <el-radio value="all">
              全员
            </el-radio>
            <el-radio value="one">
              指定用户
            </el-radio>
            <el-radio value="many">
              多用户
            </el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item v-if="sendForm.mode === 'one'" label="用户ID">
          <el-input v-model="sendForm.recipient_user_id" />
        </el-form-item>
        <el-form-item v-if="sendForm.mode === 'many'" label="用户ID列表">
          <el-input v-model="sendForm.target_user_ids" placeholder="逗号分隔，如 u-1,u-2" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="sendVisible = false">
          取消
        </el-button>
        <el-button type="primary" :loading="sending" @click="submitSend">
          发送
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
