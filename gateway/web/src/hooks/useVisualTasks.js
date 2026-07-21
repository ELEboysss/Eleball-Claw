import { useCallback, useEffect, useRef, useState } from 'react'
import { visualApi } from '../api/client'
import { isTerminal } from '../utils/visualTasks'

const POLL_INTERVAL = 5000        // 正常轮询间隔 5 秒
const IDLE_POLL_INTERVAL = 10000  // 没有运行中任务时每隔 10 秒检查一次
const POLL_INTERVAL_MAX = 30000   // 遇到 429 等错误时最大退避到 30 秒
const ERROR_BACKOFF_FACTOR = 2    // 每次失败翻倍

export function useVisualTasks(conversationId) {
  const [tasks, setTasks] = useState([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)

  // 统一轮询控制器，用 ref 保存 tasks 避免 useEffect 因 tasks 变化而频繁重建定时器
  const tasksRef = useRef(tasks)
  tasksRef.current = tasks
  const timeoutRef = useRef(null)
  const pollIntervalRef = useRef(POLL_INTERVAL)
  const scheduleNextRef = useRef(null)

  // 加载会话下全部任务
  const loadTasks = useCallback(async () => {
    if (!conversationId) return
    setLoading(true)
    setError(null)
    try {
      const data = await visualApi.getConversation(conversationId)
      const list = data?.tasks || []
      setTasks(list)
      // 加载成功后恢复正常轮询间隔
      pollIntervalRef.current = POLL_INTERVAL
    } catch (err) {
      setError(err)
      // 遇到限流则退避
      if (err.response?.status === 429) {
        pollIntervalRef.current = Math.min(
          pollIntervalRef.current * ERROR_BACKOFF_FACTOR,
          POLL_INTERVAL_MAX
        )
      }
    } finally {
      setLoading(false)
    }
  }, [conversationId])

  // 单个任务轻量更新（创建/取消等操作后用于刷新单条状态，不单独轮询）
  const updateTask = useCallback((id, patch) => {
    setTasks((prev) => prev.map((t) => (t.id === id ? { ...t, ...patch } : t)))
  }, [])

  // 统一轮询：只要存在非终态任务，就按间隔刷新整个会话
  useEffect(() => {
    if (!conversationId) return

    const scheduleNext = (delay = pollIntervalRef.current) => {
      if (timeoutRef.current) return
      timeoutRef.current = setTimeout(async () => {
        timeoutRef.current = null
        const hasRunning = tasksRef.current.some((t) => !isTerminal(t.status))
        if (hasRunning) {
          await loadTasks()
        }
        // 无论是否有运行中任务都继续调度：有任务时高频轮询，无任务时低空调度
        scheduleNext(hasRunning ? undefined : IDLE_POLL_INTERVAL)
      }, delay)
    }
    scheduleNextRef.current = scheduleNext

    // 立即加载一次
    loadTasks()
    scheduleNext()

    return () => {
      if (timeoutRef.current) {
        clearTimeout(timeoutRef.current)
        timeoutRef.current = null
      }
      scheduleNextRef.current = null
    }
  }, [conversationId, loadTasks])

  const create = useCallback(async (mediaType, payload) => {
    setError(null)
    try {
      const task = await visualApi.create({
        media_type: mediaType,
        conversation_id: conversationId,
        ...payload
      })
      setTasks((prev) => [task, ...prev])
      // 创建新任务后立即尝试启动轮询（timeout 会在下一次 tick 检测到运行中任务）
      scheduleNextRef.current?.()
      return task
    } catch (err) {
      setError(err)
      throw err
    }
  }, [conversationId])

  const cancel = useCallback(async (id) => {
    try {
      await visualApi.cancel(id)
      updateTask(id, { status: 'cancelled' })
    } catch (err) {
      console.warn('取消任务失败:', err)
    }
  }, [updateTask])

  const removeTask = useCallback((id) => {
    setTasks((prev) => prev.filter((t) => t.id !== id))
  }, [])

  return {
    tasks,
    loading,
    error,
    loadTasks,
    create,
    cancel,
    removeTask
  }
}
