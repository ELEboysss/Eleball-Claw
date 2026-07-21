/**
 * SSE 流式对话解析器
 * 后端返回 OpenAI 兼容的 chat.completion.chunk 格式
 */

export async function* streamChat(url, body, token) {
  const response = await fetch(url, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'Authorization': token ? `Bearer ${token}` : ''
    },
    body: JSON.stringify(body)
  })

  if (!response.ok) {
    const errorText = await response.text().catch(() => '请求失败')
    // 网关/代理返回的 HTML 错误页（如 504）对普通用户不友好，替换为可读提示
    if (errorText && errorText.trim().startsWith('<')) {
      throw new Error(`网关超时或不可达（HTTP ${response.status}）：模型响应较慢，请稍后重试或切换为速度更快的模型。`)
    }
    throw new Error(errorText || `HTTP ${response.status}`)
  }

  const reader = response.body?.getReader()
  if (!reader) {
    throw new Error('无法读取响应流')
  }

  const decoder = new TextDecoder('utf-8')
  let buffer = ''

  try {
    while (true) {
      const { done, value } = await reader.read()
      if (done) break

      buffer += decoder.decode(value, { stream: true })
      const lines = buffer.split('\n')
      buffer = lines.pop() || ''

      let pendingError = false
      for (const line of lines) {
        const trimmed = line.trim()
        if (!trimmed) continue

        if (trimmed.startsWith('event: error')) {
          pendingError = true
          continue
        }
        if (trimmed.startsWith('data:')) {
          const data = trimmed.slice(5).trim()
          if (pendingError) {
            throw new Error(data || '模型调用失败')
          }
          if (data === '[DONE]') {
            return
          }
          if (data) {
            try {
              const chunk = JSON.parse(data)
              const delta = chunk.choices?.[0]?.delta || {}
              // 标准模型输出内容
              if (delta.content) {
                yield { type: 'content', content: delta.content }
              }
              // Kimi / DeepSeek 等模型的思考过程（reasoning_content）
              if (delta.reasoning_content) {
                yield { type: 'reasoning', content: delta.reasoning_content }
              }
              if (chunk.choices?.[0]?.finish_reason) {
                yield { type: 'done' }
              }
            } catch {
              // 忽略非 JSON 行
            }
          }
        }
      }
    }
  } finally {
    reader.releaseLock()
  }
}
