/**
 * 文件与多模态内容处理工具
 * 参考 OpenAI / Kimi 的 content parts 格式，将前端文件转为模型可识别的消息内容。
 */

const IMAGE_MIME_TYPES = [
  'image/png',
  'image/jpeg',
  'image/jpg',
  'image/webp',
  'image/gif'
]

const TEXT_MIME_TYPES = [
  'text/plain',
  'text/markdown',
  'text/x-markdown',
  'text/html',
  'text/css',
  'text/javascript',
  'application/javascript',
  'application/json',
  'application/xml',
  'text/yaml',
  'text/csv'
]

const TEXT_EXTENSIONS = [
  '.txt', '.md', '.markdown', '.html', '.htm', '.css', '.js', '.jsx',
  '.ts', '.tsx', '.json', '.xml', '.yaml', '.yml', '.csv', '.py',
  '.go', '.java', '.c', '.cpp', '.h', '.hpp', '.rs', '.swift',
  '.kt', '.sql', '.sh', '.bash', '.zsh', '.ps1', '.log'
]

/**
 * 判断是否为支持的图片类型
 */
export function isImageFile(file) {
  return IMAGE_MIME_TYPES.includes(file.type) || /\.(png|jpe?g|webp|gif)$/i.test(file.name)
}

/**
 * 判断是否为可直接读取文本的文件
 */
export function isTextFile(file) {
  if (TEXT_MIME_TYPES.includes(file.type)) return true
  const ext = file.name.slice(file.name.lastIndexOf('.')).toLowerCase()
  return TEXT_EXTENSIONS.includes(ext)
}

/**
 * 读取文件为 Base64 Data URL
 */
export function readFileAsDataURL(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(reader.result)
    reader.onerror = () => reject(new Error('读取文件失败'))
    reader.readAsDataURL(file)
  })
}

/**
 * 读取文本文件内容
 */
export function readFileAsText(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(reader.result)
    reader.onerror = () => reject(new Error('读取文本失败'))
    reader.readAsText(file)
  })
}

/**
 * 压缩图片为指定最大宽/高，返回 base64 data URL
 * @param {string} dataUrl 原始图片 data URL
 * @param {number} maxSize 最大边长（像素）
 * @param {number} quality 压缩质量 0-1
 */
export function compressImage(dataUrl, maxSize = 1024, quality = 0.8) {
  return new Promise((resolve, reject) => {
    const img = new Image()
    img.onload = () => {
      let { width, height } = img
      if (width > maxSize || height > maxSize) {
        if (width > height) {
          height = Math.round((height * maxSize) / width)
          width = maxSize
        } else {
          width = Math.round((width * maxSize) / height)
          height = maxSize
        }
      }
      const canvas = document.createElement('canvas')
      canvas.width = width
      canvas.height = height
      const ctx = canvas.getContext('2d')
      ctx.drawImage(img, 0, 0, width, height)
      resolve(canvas.toDataURL('image/jpeg', quality))
    }
    img.onerror = () => reject(new Error('图片压缩失败'))
    img.src = dataUrl
  })
}

/**
 * 将前端文件对象转换为 content part
 * @param {File} file
 * @returns {Promise<{part: object, attachment: object}>}
 */
export async function fileToContentPart(file) {
  if (isImageFile(file)) {
    let dataUrl = await readFileAsDataURL(file)
    // 对大图做前端压缩，控制请求体体积
    if (file.size > 512 * 1024) {
      dataUrl = await compressImage(dataUrl, 1024, 0.75)
    }
    return {
      part: {
        type: 'image_url',
        image_url: { url: dataUrl }
      },
      attachment: {
        id: generateAttachmentId(),
        type: 'image',
        name: file.name,
        mimeType: file.type || 'image/jpeg',
        dataUrl
      }
    }
  }

  if (isTextFile(file)) {
    const text = await readFileAsText(file)
    return {
      part: {
        type: 'file',
        file: {
          name: file.name,
          mimeType: file.type || 'text/plain',
          text
        }
      },
      attachment: {
        id: generateAttachmentId(),
        type: 'file',
        name: file.name,
        mimeType: file.type || 'text/plain',
        text
      }
    }
  }

  // 其他二进制文件：以 base64 data URL 形式内联
  const dataUrl = await readFileAsDataURL(file)
  return {
    part: {
      type: 'file',
      file: {
        name: file.name,
        mimeType: file.type || 'application/octet-stream',
        data: dataUrl
      }
    },
    attachment: {
      id: generateAttachmentId(),
      type: 'file',
      name: file.name,
      mimeType: file.type || 'application/octet-stream',
      dataUrl
    }
  }
}

/**
 * 将用户输入文本与附件组合为 OpenAI 兼容的 content 字段
 * @param {string} text
 * @param {Array} attachments
 * @returns {string | Array}
 */
export function buildMessageContent(text, attachments = []) {
  if (!attachments || attachments.length === 0) {
    return text
  }
  const parts = []
  // 图片/文件等非文本内容放在前面，与 OpenAI / Kimi 多模态官方示例顺序一致
  for (const attachment of attachments) {
    if (attachment.type === 'image' && attachment.dataUrl) {
      parts.push({
        type: 'image_url',
        image_url: { url: attachment.dataUrl }
      })
    } else if (attachment.type === 'file') {
      const filePart = { name: attachment.name, mimeType: attachment.mimeType }
      if (attachment.text !== undefined) {
        filePart.text = attachment.text
      }
      if (attachment.dataUrl !== undefined) {
        filePart.data = attachment.dataUrl
      }
      parts.push({ type: 'file', file: filePart })
    }
  }
  if (text?.trim()) {
    parts.push({ type: 'text', text: text.trim() })
  }
  return parts
}

/**
 * 将持久化的消息 content 转换为可展示的文本摘要（用于生成标题等）
 */
export function contentToText(content) {
  if (typeof content === 'string') return content
  if (Array.isArray(content)) {
    return content
      .filter((p) => p.type === 'text')
      .map((p) => p.text)
      .join(' ')
  }
  return ''
}

function generateAttachmentId() {
  return `att_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`
}

/**
 * 下载文本内容为文件
 */
export function downloadTextFile(content, filename, mimeType = 'text/plain') {
  const blob = new Blob([content], { type: mimeType })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}

/**
 * 尝试解析 JSON 字符串，失败返回 null
 */
export function safeParseJSON(text) {
  try {
    return JSON.parse(text)
  } catch {
    return null
  }
}
