import { useEffect } from 'react'

/**
 * 轻量 SEO hook：设置页面 title 与 meta description。
 * 无新依赖，直接操作 document。各页在组件顶部调用。
 * @param {string} title 页面标题（不含站名，会自动拼接 " - Eleball"，首页除外）
 * @param {string} [description] 页面描述，留空则不改 meta
 * @param {boolean} [isHome=false] 首页标志：首页 title 不追加站名后缀
 */
export default function useSEO(title, description, isHome = false) {
  useEffect(() => {
    document.title = isHome ? title : `${title} - Eleball`

    if (description) {
      let meta = document.querySelector('meta[name="description"]')
      if (!meta) {
        meta = document.createElement('meta')
        meta.setAttribute('name', 'description')
        document.head.appendChild(meta)
      }
      meta.setAttribute('content', description)
    }
  }, [title, description, isHome])
}
