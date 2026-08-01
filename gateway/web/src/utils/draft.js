// 草稿持久化：按会话保存输入文本与附件，刷新/切会话不丢失。
// 图片附件以 dataUrl 内联，体积可能较大；localStorage 配额超限时回退为仅存文本，避免抛错阻塞。

const key = (convId) => `claw_draft_${convId || 'default'}`

// loadDraft 读取草稿；无草稿或解析失败返回 null。
export function loadDraft(convId) {
  try {
    const raw = localStorage.getItem(key(convId))
    if (!raw) return null
    const data = JSON.parse(raw)
    return {
      input: typeof data.input === 'string' ? data.input : '',
      attachments: Array.isArray(data.attachments) ? data.attachments : [],
    }
  } catch {
    return null
  }
}

// saveDraft 保存草稿；配额超限时回退仅存文本。返回是否成功保存了附件。
export function saveDraft(convId, input, attachments) {
  const payload = JSON.stringify({ input, attachments })
  try {
    localStorage.setItem(key(convId), payload)
    return true
  } catch {
    // 配额超限（多为图片 dataUrl 过大）：回退仅存文本
    try {
      localStorage.setItem(key(convId), JSON.stringify({ input, attachments: [] }))
    } catch {
      // 连文本都存不下：静默放弃
    }
    return false
  }
}

// clearDraft 发送后清空草稿。
export function clearDraft(convId) {
  try {
    localStorage.removeItem(key(convId))
  } catch {
    // ignore
  }
}
