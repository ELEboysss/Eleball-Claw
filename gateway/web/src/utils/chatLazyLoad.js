// C10 T4：历史消息向上懒加载辅助函数
// 仅保留纯逻辑，UI 侧在 Chat.jsx 中通过 IntersectionObserver 接入

export const VISIBLE_PAGE_SIZE = 50

/**
 * 根据消息总数与当前可见数，计算渲染窗口起始索引
 * @param {number} totalCount
 * @param {number} visibleCount
 * @returns {{ startIndex: number, hasMore: boolean }}
 */
export function getVisibleRenderWindow(totalCount, visibleCount) {
  const clampedVisibleCount = Math.min(Math.max(visibleCount, 0), Math.max(totalCount, 0))
  const startIndex = Math.max(0, totalCount - clampedVisibleCount)
  return { startIndex, hasMore: startIndex > 0 }
}

/**
 * 计算下一次可见数量
 * @param {number} currentVisibleCount
 * @param {number} [pageSize]
 * @returns {number}
 */
export function getNextVisibleCount(currentVisibleCount, pageSize = VISIBLE_PAGE_SIZE) {
  return currentVisibleCount + pageSize
}

/**
 * 记录当前滚动位置到底部的距离，用于 prepend 消息后恢复位置
 * @param {number} scrollHeight
 * @param {number} scrollTop
 * @returns {number}
 */
export function captureScrollDistance(scrollHeight, scrollTop) {
  return scrollHeight - scrollTop
}

/**
 * 根据保存的到底部距离计算应恢复到的 scrollTop
 * @param {number} scrollHeight
 * @param {number} savedDistance
 * @returns {number}
 */
export function restoreScrollTop(scrollHeight, savedDistance) {
  return Math.max(0, scrollHeight - savedDistance)
}
