import { useCallback, useMemo, useRef, useState } from 'react'
import { getJSON, setJSON } from '../utils/storage'

const HISTORY_KEY = 'chat_input_history'
const MAX_HISTORY = 50

/**
 * 对话输入历史管理（C5）
 * - 仅保存非空且非命令的文本输入
 * - ArrowUp / ArrowDown 在历史中前后翻页
 * - 支持在当前草稿与历史之间切换
 */
export function useInputHistory(userId) {
  const key = userId ? `${HISTORY_KEY}_${userId}` : HISTORY_KEY
  const [history, setHistory] = useState(() => getJSON(key, []))
  const indexRef = useRef(-1)
  const draftRef = useRef('')

  const saveHistory = useCallback((next) => {
    setHistory(next)
    setJSON(key, next)
  }, [key])

  const push = useCallback((text) => {
    const trimmed = text.trim()
    if (!trimmed || trimmed.startsWith('/') || trimmed.startsWith('@')) return
    const next = [trimmed, ...history.filter((h) => h !== trimmed)].slice(0, MAX_HISTORY)
    saveHistory(next)
  }, [history, saveHistory])

  const resetIndex = useCallback(() => {
    indexRef.current = -1
    draftRef.current = ''
  }, [])

  /**
   * @param {string} currentInput 当前输入框值
   * @param {1 | -1} direction 1=更旧, -1=更新
   * @returns {string | null} 新输入值，null 表示无变化
   */
  const navigate = useCallback((currentInput, direction) => {
    if (history.length === 0) return null
    if (indexRef.current === -1) {
      draftRef.current = currentInput
    }
    const nextIndex = indexRef.current + direction
    if (nextIndex < -1 || nextIndex >= history.length) return null
    indexRef.current = nextIndex
    if (nextIndex === -1) {
      return draftRef.current
    }
    return history[nextIndex]
  }, [history])

  return useMemo(() => ({
    history,
    push,
    navigate,
    resetIndex
  }), [history, push, navigate, resetIndex])
}
