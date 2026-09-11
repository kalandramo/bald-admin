interface UseConfirmActionOptions {
  confirmTitle?: string
  confirmMessage?: string
  successMessage?: string
}

export function useConfirmAction(options: UseConfirmActionOptions = {}) {
  const {
    confirmTitle = "提示",
    confirmMessage = "确认执行此操作？",
    successMessage = "操作成功"
  } = options

  const handleDelete = async (
    action: () => Promise<any>,
    onSuccess?: () => void
  ) => {
    try {
      await ElMessageBox.confirm(confirmMessage, confirmTitle, {
        confirmButtonText: "确定",
        cancelButtonText: "取消",
        type: "warning"
      })
      await action()
      ElMessage.success(successMessage)
      onSuccess?.()
    } catch (error: any) {
      // 错误已由 Axios 拦截器统一展示 ElMessage，此处不再重复弹错
      if (error !== "cancel") {
        console.error("[useConfirmAction]", error)
      }
    }
  }

  return { handleDelete }
}
