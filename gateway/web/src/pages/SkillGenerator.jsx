import { useState } from 'react'
import { Link } from 'react-router-dom'
import { Loader2, Sparkles, CheckCircle2 } from 'lucide-react'
import { moduleGeneratorApi } from '../api/client'
import { useAuth } from '../context/AuthContext'

// 写提示词造秘技（E4）：生成 Anthropic 标准 SKILL.md 的 prompt-only 秘技。
// 与「写脚本造秘技」的区别：纯指令/人格/方法论能力无需代码，一个 SKILL.md 即一个秘技
// （driver=none，body 即注入对话的 SystemPrompt）；生成后经 POST /claw-console/skills/generate
// 写入 marketplace/{slug}/SKILL.md 并定向同步出 SKU（skillmd-<slug>），集市可见可激活。
// SKILL.md 是业界 Agent Skills 开放标准，该文件可直接被 Claude Code / Cursor / DSH 等平台复用。

const BODY_PLACEHOLDER = `你是「xxx」专家，擅长……

## 原则
- 原则一
- 原则二

## 输出要求
- ……`

function SectionTitle({ icon: Icon, children, desc }) {
  return (
    <div className="flex items-start gap-2 mb-3">
      <Icon className="w-4 h-4 text-eleball-primary flex-shrink-0 mt-0.5" />
      <div>
        <h2 className="text-sm font-semibold text-eleball-text">{children}</h2>
        {desc && <p className="text-xs text-eleball-text-secondary mt-0.5">{desc}</p>}
      </div>
    </div>
  )
}

export default function SkillGenerator() {
  // 嵌入 DIY 工作室（Studio）内容区，页头/SEO 由 Studio 统一负责。
  const { user } = useAuth()

  const [name, setName] = useState('')
  const [skillId, setSkillId] = useState('')
  const [description, setDescription] = useState('')
  const [category, setCategory] = useState('')
  const [body, setBody] = useState('')

  const [generating, setGenerating] = useState(false)
  const [genError, setGenError] = useState(null)
  const [result, setResult] = useState(null)

  const onGenerate = async () => {
    if (!name.trim() || !description.trim() || !body.trim()) {
      setGenError({ message: '展示名、描述、SystemPrompt 正文均为必填' })
      return
    }
    setGenerating(true)
    setGenError(null)
    setResult(null)
    try {
      const data = await moduleGeneratorApi.generateSkill({
        skill_id: skillId.trim(),
        name: name.trim(),
        description: description.trim(),
        category: category.trim(),
        body,
        username: user?.nickname || user?.username || '',
      })
      setResult(data)
      // 生成成功后回填 skill_id，同会话再次提交即覆盖更新同一秘技
      if (data?.skill_id) setSkillId(data.skill_id)
    } catch (e) {
      setGenError({ message: e.message, data: e.data })
    } finally {
      setGenerating(false)
    }
  }

  return (
    <div>
      <div className="card mb-4">
        <SectionTitle
          icon={Sparkles}
          desc="纯指令/人格/方法论类能力无需代码：一个 Anthropic 标准 SKILL.md 即一个秘技，购买/激活后作为 SystemPrompt 注入对话。该文件可直接被 Claude Code / Cursor / DSH 等平台复用。"
        >
          写提示词造秘技（SKILL.md）
        </SectionTitle>

        <div className="space-y-3">
          <div className="grid sm:grid-cols-2 gap-3">
            <div>
              <label className="block text-xs font-medium text-eleball-text-secondary mb-1">
                展示名 <span className="text-red-500">*</span>
              </label>
              <input
                className="input text-sm w-full"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="如：文案专家"
              />
            </div>
            <div>
              <label className="block text-xs font-medium text-eleball-text-secondary mb-1">
                skill ID
                <span className="ml-1 text-eleball-text-tertiary font-normal">小写字母/数字/中划线；留空据展示名推导，纯中文名须手填</span>
              </label>
              <input
                className="input text-sm w-full font-mono"
                value={skillId}
                onChange={(e) => setSkillId(e.target.value)}
                placeholder="my-skill"
              />
            </div>
          </div>

          <div>
            <label className="block text-xs font-medium text-eleball-text-secondary mb-1">
              描述 <span className="text-red-500">*</span>
              <span className="ml-1 text-eleball-text-tertiary font-normal">何时使用这个秘技，影响模型触发与集市卡片展示</span>
            </label>
            <input
              className="input text-sm w-full"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="如：转化型营销文案专家——当用户想撰写、改写或优化营销文案时使用"
            />
          </div>

          <div>
            <label className="block text-xs font-medium text-eleball-text-secondary mb-1">
              分类
              <span className="ml-1 text-eleball-text-tertiary font-normal">可选，缺省「提示」</span>
            </label>
            <input
              className="input text-sm w-full"
              value={category}
              onChange={(e) => setCategory(e.target.value)}
              placeholder="提示"
            />
          </div>

          <div>
            <label className="block text-xs font-medium text-eleball-text-secondary mb-1">
              SystemPrompt 正文 <span className="text-red-500">*</span>
              <span className="ml-1 text-eleball-text-tertiary font-normal">SKILL.md 的 Markdown body，开头宜直接给出角色定位</span>
            </label>
            <textarea
              className="input text-sm w-full font-mono"
              rows={12}
              value={body}
              onChange={(e) => setBody(e.target.value)}
              placeholder={BODY_PLACEHOLDER}
            />
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-3 mt-4">
          <button
            type="button"
            onClick={onGenerate}
            disabled={generating}
            className="btn-primary text-sm px-5 py-2.5 disabled:opacity-50"
          >
            {generating ? <Loader2 className="w-4 h-4 animate-spin" /> : <Sparkles className="w-4 h-4" />}
            生成秘技
          </button>
          <span className="text-xs text-eleball-text-tertiary">
            生成后写入本地 marketplace 并立即上架集市；同 skill ID 再次生成即覆盖更新。
          </span>
        </div>

        {genError && (
          <div className="mt-4 text-sm px-3 py-2 rounded-xl bg-red-50 text-red-600">{genError.message}</div>
        )}

        {result && (
          <div className="mt-4 rounded-xl border border-emerald-300 bg-emerald-50 p-4">
            <div className="flex items-center gap-2 text-sm font-semibold text-emerald-700">
              <CheckCircle2 className="w-4 h-4" /> 秘技已生成并上架本地集市
            </div>
            <div className="mt-2 text-xs text-eleball-text-secondary space-y-1">
              <div>SKU：<span className="font-mono">{result.sku_id}</span></div>
              <div className="font-mono truncate">目录：{result.dir}</div>
            </div>
            <div className="mt-3 text-xs">
              <Link to="/agents" className="text-eleball-primary font-medium hover:underline">
                前往秘技集市激活使用 →
              </Link>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
