import { ref } from "vue"

export function useCrudLoading() {
  const listLoading = ref(false)
  const saveLoading = ref(false)
  const deleteLoading = ref(false)

  return {
    listLoading,
    saveLoading,
    deleteLoading
  }
}
