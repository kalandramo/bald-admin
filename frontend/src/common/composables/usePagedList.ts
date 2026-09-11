import { reactive, ref } from "vue"

interface UsePagedListOptions<T> {
  fetchFn: (params: any) => Promise<{ items: T[], total: number }>
  pageSize?: number
}

export function usePagedList<T>(options: UsePagedListOptions<T>) {
  const { fetchFn, pageSize = 10 } = options

  const loading = ref(false)
  const list = ref<T[]>([]) as Ref<T[]>
  const total = ref(0)

  const pagination = reactive({
    currentPage: 1,
    pageSize
  })

  const fetchList = async () => {
    loading.value = true
    try {
      const res = await fetchFn({
        page: pagination.currentPage,
        page_size: pagination.pageSize
      })
      list.value = res.items || []
      total.value = res.total || 0
    } finally {
      loading.value = false
    }
  }

  const handleCurrentChange = (val: number) => {
    pagination.currentPage = val
    fetchList()
  }

  const handleSizeChange = (val: number) => {
    pagination.pageSize = val
    pagination.currentPage = 1
    fetchList()
  }

  return {
    loading,
    list,
    total,
    pagination,
    fetchList,
    handleCurrentChange,
    handleSizeChange
  }
}
