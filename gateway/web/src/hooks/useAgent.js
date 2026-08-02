import { useState, useCallback, useRef, useReducer, useEffect } from 'react'
import { agentApi } from '../api/client'

// C10：流式消息状态机，用于驱动持续更新的消息气泡与加载指示
function streamReducer(state, action) {
  switch (action.type) {
    case 'start':
      return { isStreaming: true, streamingMessage: null }
    case 'update':
      return { isStreaming: true, streamingMessage: action.message }
    case 'end':
    case 'reset':
      return { isStreaming: false, streamingMessage: null }
    default:
      return state
  }
}

// C10：服务端状态校准时，SSE 丢失/切后台/断网时兜底完成
const RECONCILE_INTERVAL_MS = 15000

export function useAgent() {
  const [status, setStatus] = useState('idle') // idle / executing / answering / done / error
  const [toolSteps, setToolSteps] = useState([])
  const [currentStep, setCurrentStep] = useState(0)
  const [answer, setAnswer] = useState('')
  const [reasoningContent, setReasoningContent] = useState('')
  const [intermediateAnswer, setIntermediateAnswer] = useState('')
  const [resources, setResources] = useState([])
  const [toolSummary, setToolSummary] = useState('')
  const [error, setError] = useState('')
  const [warning, setWarning] = useState('')
  // C4：自动压缩状态与结果（供 Chat 页 banner 展示）
  const [isCompacting, setIsCompacting] = useState(false)
  const [compactResult, setCompactResult] = useState(null)
  // 按真实时间顺序维护的 Agent 执行步骤，用于线性展示 thinking / tool / answer
  const [steps, setSteps] = useState([])
  // C6：steer / follow-up 排队消息，供输入栏上方排队面板展示
  const [steerQueue, setSteerQueue] = useState([])
  const [followupQueue, setFollowupQueue] = useState([])
  // C6：当前执行中的 Agent Session ID，用于 steer/follow-up 提交
  const [sessionId, setSessionId] = useState('')
  // C10：流式状态与当前增量消息
  const [streamState, dispatch] = useReducer(streamReducer, { isStreaming: false, streamingMessage: null })
  // C10：Agent 当前运行阶段（等待模型 / 工具执行 / 命令执行）
  const [agentPhase, setAgentPhase] = useState(null)
  // C10 T5：当前运行中 Session ID 集合，用于侧栏实时指示
  const [runningSessionIds, setRunningSessionIds] = useState(() => new Set())

  const abortRef = useRef(false)
  // AR-02：AbortController 真正断连，让服务端 ctx 取消停止后续工具调用与 token 消耗
  const abortControllerRef = useRef(null)
  // C10：运行中 session id，用于 reconcile 轮询
  const runningSessionIdRef = useRef('')
  // C10：当前流式消息文本，避免 reducer 异步合并导致追加丢失
  const streamingMessageRef = useRef(null)
  // C10：完成回调，轮询在发现服务端已结束时调用
  const finishRunRef = useRef(null)

  const reset = useCallback(() => {
    setStatus('idle')
    setToolSteps([])
    setCurrentStep(0)
    setAnswer('')
    setReasoningContent('')
    setIntermediateAnswer('')
    setResources([])
    setToolSummary('')
    setError('')
    setWarning('')
    setIsCompacting(false)
    setCompactResult(null)
    setSteps([])
    setSteerQueue([])
    setFollowupQueue([])
    setSessionId('')
    setAgentPhase(null)
    dispatch({ type: 'reset' })
    streamingMessageRef.current = null
    abortRef.current = false
    abortControllerRef.current = null
    runningSessionIdRef.current = ''
    finishRunRef.current = null
  }, [])

  // C10：把增量文本同步到 streamingMessage，供 Chat 持续展示
  const appendStreamingMessage = useCallback((role, delta) => {
    const prev = streamingMessageRef.current
    const next = prev && prev.role === role
      ? { role, content: prev.content + delta }
      : { role, content: delta }
    streamingMessageRef.current = next
    dispatch({ type: 'update', message: next })
  }, [])

  const execute = useCallback(({ conversationId, message, attachments = [], history = [], model, provider, baseUrl, apiKey, enableTools, enableWebSearch, searchProvider, assistantId, cwd, permissionMode }) => {
    reset()
    setStatus('executing')
    abortRef.current = false
    // AR-02：为本次执行创建 AbortController，abort() 调 controller.abort() 真正断连
    const controller = new AbortController()
    abortControllerRef.current = controller

    return new Promise((resolve, reject) => {
      let finalAnswer = ''
      let finalError = ''
      // 同步累积最终产物，resolve 时直接返回，避免依赖 React state 的异步更新
      const finalToolSteps = []
      let finalReasoning = ''
      let finalIntermediate = ''
      const finalResources = []
      let finalToolSummary = ''
      let finalWarning = ''
      let finalSteps = []
      let isCompleted = false

      // C10：统一完成路径，防止 reconcile / done / AbortError 重复 resolve/reject
      const finish = (err) => {
        if (isCompleted) return
        isCompleted = true
        finishRunRef.current = null
        streamingMessageRef.current = null
        if (abortControllerRef.current) {
          abortControllerRef.current.abort()
          abortControllerRef.current = null
        }
        setAgentPhase(null)
        dispatch({ type: 'end' })
        if (err) {
          setStatus('error')
          setError(err.message || String(err) || '请求失败')
          reject(err)
        } else {
          setStatus('done')
          resolve({
            answer: finalAnswer,
            toolSteps: finalToolSteps,
            reasoningContent: finalReasoning,
            intermediateAnswer: finalIntermediate,
            resources: finalResources,
            toolSummary: finalToolSummary,
            warning: finalWarning,
            sessionId: runningSessionIdRef.current || '',
            usage: null, // AR-07：用量可见性（tokens/cost/步数/上下文规模）
            steps: finalSteps
          })
        }
      }
      finishRunRef.current = finish

      agentApi.execute(
        {
          conversation_id: conversationId,
          message,
          attachments,
          history,
          model,
          provider,
          base_url: baseUrl,
          api_key: apiKey,
          enable_tools: enableTools,
          enable_web_search: enableWebSearch,
          search_provider: searchProvider,
          // AR-11：claw 本地工作目录（cwd），文件工具/Shell 据此解析；空时不传
          cwd: cwd || undefined,
          // 会话绑定的助手；空字符串时不传该字段，后端回退到会话值/全部已激活工具
          assistant_id: assistantId || undefined,
          // C1 权限模式（default/acceptEdits/plan）；空时不传，后端回退会话持久化值
          permission_mode: permissionMode || undefined
        },
        (event) => {
          if (abortRef.current) return
          // C6 / C10：任何事件带 session_id 时同步到状态与 ref，供 steer/follow-up / reconcile 使用
          if (event.data?.session_id) {
            setSessionId(event.data.session_id)
            runningSessionIdRef.current = event.data.session_id
            // C10 T5：乐观加入运行中集合，侧栏立即显示运行指示
            setRunningSessionIds((prev) => {
              if (prev.has(event.data.session_id)) return prev
              const next = new Set(prev)
              next.add(event.data.session_id)
              return next
            })
          }
          switch (event.event) {
            case 'agent_start': {
              // C10：兼容未来后端下发 agent_start 事件
              setAgentPhase({ kind: 'waiting_model' })
              dispatch({ type: 'start' })
              break
            }
            case 'tool_call': {
              const sessionId = event.data.session_id || ''
              const callStep = {
                step: event.data.step,
                tool: event.data.tool,
                arguments: event.data.arguments,
                status: 'running',
                sessionId
              }
              finalToolSteps.push(callStep)
              finalSteps = [...finalSteps, { type: 'tool_call', ...callStep }]
              setToolSteps(prev => [...prev, callStep])
              setCurrentStep(event.data.step)
              setSteps(prev => [...prev, { type: 'tool_call', ...callStep }])
              // C10：标记工具执行阶段
              setAgentPhase(prev => {
                const toolId = `${sessionId || 'main'}:${event.data.step}`
                const tools = prev?.kind === 'running_tools' ? [...prev.tools] : []
                if (!tools.some(t => t.id === toolId)) {
                  tools.push({ id: toolId, name: event.data.tool })
                }
                return { kind: 'running_tools', tools }
              })
              break
            }
            case 'tool_result': {
              const { step, tool, status, output, error_message } = event.data
              // Agent Team P5：子循环 step 编号从 1 重启，按 (sessionId, step) 匹配避免与主循环撞号
              const sessionId = event.data.session_id || ''
              const matchKey = (s) => s.step === step && (s.sessionId || '') === sessionId
              const resultIndex = finalToolSteps.findIndex(matchKey)
              if (resultIndex >= 0) {
                finalToolSteps[resultIndex] = {
                  ...finalToolSteps[resultIndex],
                  status,
                  output,
                  error: error_message
                }
              }
              const stepIndex = finalSteps.findIndex(s => (s.type === 'tool_call' || s.type === 'tool_result') && matchKey(s))
              if (stepIndex >= 0) {
                finalSteps = finalSteps.map((s, idx) =>
                  idx === stepIndex
                    ? { ...s, status, output, error: error_message }
                    : s
                )
              } else {
                finalSteps = [...finalSteps, { type: 'tool_result', step, tool, status, output, error: error_message, sessionId }]
              }
              setToolSteps(prev => prev.map(s =>
                matchKey(s)
                  ? { ...s, status, output, error: error_message }
                  : s
              ))
              setSteps(prev => {
                const idx = prev.findIndex(s => (s.type === 'tool_call' || s.type === 'tool_result') && matchKey(s))
                if (idx >= 0) {
                  return prev.map((s, i) =>
                    i === idx
                      ? { ...s, status, output, error: error_message }
                      : s
                  )
                }
                return [...prev, { type: 'tool_result', step, tool, status, output, error: error_message, sessionId }]
              })
              // C10：工具完成，从阶段中移除
              setAgentPhase(prev => {
                if (prev?.kind !== 'running_tools') return prev
                const toolId = `${sessionId || 'main'}:${step}`
                const tools = prev.tools.filter(t => t.id !== toolId)
                if (tools.length === 0) return { kind: 'waiting_model' }
                return { kind: 'running_tools', tools }
              })
              break
            }
            case 'approval_request': {
              // C1：工具审批请求--服务端阻塞等待用户决策。渲染审批卡，用户点击后经 /agent/approve 投递
              const ap = event.data
              const approval = {
                type: 'approval',
                tool_call_id: ap.tool_call_id,
                sessionId: ap.session_id || '',
                tool: ap.tool,
                arguments: ap.arguments,
                argumentsRaw: ap.arguments_raw,
                riskLevel: ap.risk_level,
                reason: ap.reason,
                status: 'pending'
              }
              finalSteps = [...finalSteps, approval]
              setSteps(prev => [...prev, approval])
              break
            }
            case 'approval_resolved': {
              // C1：服务端已收到决策，工具将执行或跳过--更新卡片状态（approved/denied）
              const { tool_call_id, decision } = event.data
              const resolved = decision === 'allow' ? 'approved' : 'denied'
              finalSteps = finalSteps.map(s =>
                s.type === 'approval' && s.tool_call_id === tool_call_id
                  ? { ...s, status: resolved }
                  : s
              )
              setSteps(prev => prev.map(s =>
                s.type === 'approval' && s.tool_call_id === tool_call_id
                  ? { ...s, status: resolved }
                  : s
              ))
              break
            }
            case 'plan_review': {
              // C3：plan 审批请求--ExitPlanMode 阻塞等用户接受/拒绝/细化
              const pr = event.data
              const review = {
                type: 'plan_review',
                tool_call_id: pr.tool_call_id,
                sessionId: pr.session_id || '',
                goal: pr.goal || '',
                planContent: pr.plan_content || '',
                planPath: pr.plan_path || '',
                status: 'pending'
              }
              finalSteps = [...finalSteps, review]
              setSteps(prev => [...prev, review])
              break
            }
            case 'plan_resolved': {
              // C3：plan 决策返回--更新卡片状态（accepted/rejected/refined）
              const { tool_call_id, decision } = event.data
              const resolved = ['accepted', 'rejected', 'refined'].includes(decision) ? decision : 'rejected'
              finalSteps = finalSteps.map(s =>
                s.type === 'plan_review' && s.tool_call_id === tool_call_id
                  ? { ...s, status: resolved }
                  : s
              )
              setSteps(prev => prev.map(s =>
                s.type === 'plan_review' && s.tool_call_id === tool_call_id
                  ? { ...s, status: resolved }
                  : s
              ))
              break
            }
            case 'reasoning': {
              const sessionId = event.data.session_id || ''
              const delta = event.data.delta || ''
              // Agent Team P5：子 agent 推理不污染主对话 reasoning（仅主循环 sessionId='' 累入）
              if (sessionId === '') finalReasoning += delta
              const last = finalSteps[finalSteps.length - 1]
              if (last && last.type === 'thinking' && (last.sessionId || '') === sessionId) {
                finalSteps = finalSteps.map((s, idx) =>
                  idx === finalSteps.length - 1
                    ? { ...s, content: s.content + delta }
                    : s
                )
              } else {
                finalSteps = [...finalSteps, { type: 'thinking', content: delta, sessionId }]
              }
              if (sessionId === '') {
                setReasoningContent(prev => prev + delta)
                appendStreamingMessage('reasoning', delta)
              }
              setSteps(prev => {
                const lastStep = prev[prev.length - 1]
                if (lastStep && lastStep.type === 'thinking' && (lastStep.sessionId || '') === sessionId) {
                  return prev.map((s, idx) =>
                    idx === prev.length - 1
                      ? { ...s, content: s.content + delta }
                      : s
                  )
                }
                return [...prev, { type: 'thinking', content: delta, sessionId }]
              })
              setAgentPhase({ kind: 'waiting_model' })
              break
            }
            case 'intermediate_answer': {
              const sessionId = event.data.session_id || ''
              const delta = event.data.delta || ''
              // Agent Team P5：子 agent 中间回答不污染主对话 intermediate（仅主循环累入）
              if (sessionId === '') finalIntermediate += delta
              const last = finalSteps[finalSteps.length - 1]
              if (last && last.type === 'intermediate' && (last.sessionId || '') === sessionId) {
                finalSteps = finalSteps.map((s, idx) =>
                  idx === finalSteps.length - 1
                    ? { ...s, content: s.content + delta }
                    : s
                )
              } else {
                finalSteps = [...finalSteps, { type: 'intermediate', content: delta, sessionId }]
              }
              if (sessionId === '') {
                setIntermediateAnswer(prev => prev + delta)
                appendStreamingMessage('intermediate', delta)
              }
              setSteps(prev => {
                const lastStep = prev[prev.length - 1]
                if (lastStep && lastStep.type === 'intermediate' && (lastStep.sessionId || '') === sessionId) {
                  return prev.map((s, idx) =>
                    idx === prev.length - 1
                      ? { ...s, content: s.content + delta }
                      : s
                  )
                }
                return [...prev, { type: 'intermediate', content: delta, sessionId }]
              })
              setAgentPhase({ kind: 'waiting_model' })
              break
            }
            case 'final_answer': {
              setStatus('answering')
              const delta = event.data.delta || ''
              finalAnswer += delta
              const last = finalSteps[finalSteps.length - 1]
              if (last && last.type === 'answer') {
                finalSteps = finalSteps.map((s, idx) =>
                  idx === finalSteps.length - 1
                    ? { ...s, content: s.content + delta }
                    : s
                )
              } else {
                finalSteps = [...finalSteps, { type: 'answer', content: delta }]
              }
              setAnswer(prev => prev + delta)
              appendStreamingMessage('assistant', delta)
              setSteps(prev => {
                const lastStep = prev[prev.length - 1]
                if (lastStep && lastStep.type === 'answer') {
                  return prev.map((s, idx) =>
                    idx === prev.length - 1
                      ? { ...s, content: s.content + delta }
                      : s
                  )
                }
                return [...prev, { type: 'answer', content: delta }]
              })
              setAgentPhase({ kind: 'waiting_model' })
              break
            }
            case 'tool_summary': {
              const summary = event.data.content || ''
              finalToolSummary = summary
              setToolSummary(summary)
              // tool_summary 是后端最终摘要，steps 中已保留具体 tool_call/tool_result，不再追加
              break
            }
            case 'resource': {
              const exists = finalResources.some(r => r.resource_id === event.data.resource_id)
              if (!exists) finalResources.push(event.data)
              const stepExists = finalSteps.some(s => s.type === 'resource' && s.resource_id === event.data.resource_id)
              if (!stepExists) {
                finalSteps = [...finalSteps, { type: 'resource', ...event.data }]
              }
              setResources(prev => {
                const exists = prev.some(r => r.resource_id === event.data.resource_id)
                if (exists) return prev
                return [...prev, event.data]
              })
              setSteps(prev => {
                const stepExists = prev.some(s => s.type === 'resource' && s.resource_id === event.data.resource_id)
                if (stepExists) return prev
                return [...prev, { type: 'resource', ...event.data }]
              })
              break
            }
            case 'warning': {
              finalWarning = event.data.message || ''
              finalSteps = [...finalSteps, { type: 'warning', message: finalWarning }]
              setWarning(finalWarning)
              setSteps(prev => [...prev, { type: 'warning', message: finalWarning }])
              break
            }
            case 'error': {
              setStatus('error')
              finalError = event.data.message || '未知错误'
              finalSteps = [...finalSteps, { type: 'error', message: finalError }]
              setError(finalError)
              setSteps(prev => [...prev, { type: 'error', message: finalError }])
              break
            }
            case 'cancelled': {
              // AR-02：服务端确认取消（ctx 取消后尽力下发，连接可能已断无法收到）
              setStatus('done')
              finalSteps = [...finalSteps, { type: 'cancelled' }]
              setSteps(prev => [...prev, { type: 'cancelled' }])
              finish()
              break
            }
            case 'steer_accepted': {
              // C6：服务端已消费 steer 消息，从本地待处理队列移除
              const text = event.data?.text || ''
              finalSteps = [...finalSteps, { type: 'steer_accepted', text }]
              setSteerQueue(prev => {
                const idx = prev.findIndex(q => q.text === text)
                if (idx >= 0) return prev.filter((_, i) => i !== idx)
                return prev
              })
              setSteps(prev => [...prev, { type: 'steer_accepted', text }])
              break
            }
            case 'followup_queued': {
              // C6：服务端已排队 follow-up 消息，从本地待处理队列移除
              const text = event.data?.text || ''
              finalSteps = [...finalSteps, { type: 'followup_queued', text }]
              setFollowupQueue(prev => {
                const idx = prev.findIndex(q => q.text === text)
                if (idx >= 0) return prev.filter((_, i) => i !== idx)
                return prev
              })
              setSteps(prev => [...prev, { type: 'followup_queued', text }])
              break
            }
            case 'compact_start': {
              // C4：服务端开始自动压缩上下文
              setIsCompacting(true)
              const startStep = { type: 'compact', status: 'running', result: null }
              finalSteps = [...finalSteps, startStep]
              setSteps(prev => [...prev, startStep])
              break
            }
            case 'compact_end': {
              // C4：压缩结束（reason=skipped 时无 data；有 before_tokens/after_tokens 时展示节省）
              setIsCompacting(false)
              const data = event.data || {}
              let result = null
              if (data.before_tokens !== undefined && data.after_tokens !== undefined) {
                result = {
                  beforeTokens: data.before_tokens,
                  afterTokens: data.after_tokens,
                  reason: data.reason || 'auto',
                  error: data.error || ''
                }
                setCompactResult(result)
              } else if (data.reason === 'skipped' || data.reason === 'error') {
                setCompactResult(null)
              }
              const matchKey = (s) => s.type === 'compact' && s.status === 'running'
              const endStep = { type: 'compact', status: 'done', result }
              const cIdx = finalSteps.findIndex(matchKey)
              if (cIdx >= 0) {
                finalSteps = finalSteps.map((s, idx) => idx === cIdx ? endStep : s)
              }
              setSteps(prev => {
                const idx = prev.findIndex(matchKey)
                if (idx >= 0) return prev.map((s, i) => i === idx ? endStep : s)
                return prev
              })
              break
            }
            case 'done':
              finish(finalError ? new Error(finalError) : null)
              break
          }
        }
      ).catch(err => {
        // AR-02：用户主动取消（AbortError）不算错误，置为 done/cancelled 状态
        if (err.name === 'AbortError' || abortRef.current) {
          finish()
          return
        }
        finish(err)
      })
    })
  }, [reset, appendStreamingMessage])

  const abort = useCallback(() => {
    abortRef.current = true
    // AR-02：真正断连，触发服务端 ctx 取消，停止后续工具调用与 token 消耗
    if (abortControllerRef.current) {
      abortControllerRef.current.abort()
      abortControllerRef.current = null
    }
  }, [])

  // C10：SSE 丢失/切后台/断网恢复时，向服务端校准运行状态，必要时强制完成
  useEffect(() => {
    if (!streamState.isStreaming) return
    const reconcile = async () => {
      const sid = runningSessionIdRef.current
      if (!sid || !finishRunRef.current) return
      try {
        const state = await agentApi.getSessionState(sid)
        if (!state.running) {
          finishRunRef.current()
        }
      } catch (e) {
        console.error('reconcile session state failed:', e)
      }
    }
    const interval = setInterval(reconcile, RECONCILE_INTERVAL_MS)
    const onVisible = () => {
      if (document.visibilityState === 'visible') reconcile()
    }
    const onOnline = () => reconcile()
    document.addEventListener('visibilitychange', onVisible)
    window.addEventListener('online', onOnline)
    return () => {
      clearInterval(interval)
      document.removeEventListener('visibilitychange', onVisible)
      window.removeEventListener('online', onOnline)
    }
  }, [streamState.isStreaming])

  // C10 T5：订阅运行中 Session 集合变化，侧栏实时指示
  useEffect(() => {
    return agentApi.subscribeRunningSessions(
      (ids) => setRunningSessionIds(new Set(ids)),
      (err) => console.error('running sessions SSE error:', err)
    )
  }, [])

  // C10 T5：当前流式任务结束时从运行中集合移除
  useEffect(() => {
    if (streamState.isStreaming) return
    const sid = runningSessionIdRef.current
    if (!sid) return
    setRunningSessionIds((prev) => {
      if (!prev.has(sid)) return prev
      const next = new Set(prev)
      next.delete(sid)
      return next
    })
  }, [streamState.isStreaming])

  // C1：提交工具审批决策（跨 HTTP 请求解锁阻塞的工具循环）。
  // 决策投递后服务端下发 approval_resolved，卡片状态随之更新；失败时服务端按超时 deny。
  const approveToolCall = useCallback((sessionId, toolCallId, decision, alwaysAllow) => {
    agentApi.approveToolCall(sessionId, toolCallId, decision, alwaysAllow).catch(err => {
      setError(err.message || '审批提交失败')
    })
  }, [])

  // C3：提交 plan 审批决策（接受/拒绝/细化 + 反馈）。
  // 接受后会话由前端切 acceptEdits（后端工具循环据决策恢复执行）。
  const submitPlanReview = useCallback((sessionId, toolCallId, decision, feedback) => {
    agentApi.submitPlanReview(sessionId, toolCallId, decision, feedback).catch(err => {
      setError(err.message || 'plan 审批提交失败')
    })
  }, [])

  // C6：提交 steer 消息到运行中的 session。图片不能 steer（调用方校验）。
  const submitSteer = useCallback((sessionId, text) => {
    const trimmed = text.trim()
    if (!trimmed) return
    setSteerQueue(prev => [...prev, { text: trimmed, createdAt: Date.now() }])
    agentApi.steer(sessionId, trimmed).catch(err => {
      setError(err.message || 'steer 发送失败')
      setSteerQueue(prev => prev.filter(q => q.text !== trimmed))
    })
  }, [])

  // C6：提交 follow-up 消息到当前 session，Agent 当前回合结束后自动继续执行。
  const submitFollowup = useCallback((sessionId, text) => {
    const trimmed = text.trim()
    if (!trimmed) return
    setFollowupQueue(prev => [...prev, { text: trimmed, createdAt: Date.now() }])
    agentApi.followup(sessionId, trimmed).catch(err => {
      setError(err.message || 'follow-up 发送失败')
      setFollowupQueue(prev => prev.filter(q => q.text !== trimmed))
    })
  }, [])

  // C6：从本地待处理队列中移除一条 steer / follow-up（用户手动取消或 recall）
  const removeSteer = useCallback((text) => {
    setSteerQueue(prev => prev.filter(q => q.text !== text))
  }, [])

  const removeFollowup = useCallback((text) => {
    setFollowupQueue(prev => prev.filter(q => q.text !== text))
  }, [])

  return {
    execute,
    abort,
    reset,
    approveToolCall,
    submitPlanReview,
    submitSteer,
    submitFollowup,
    removeSteer,
    removeFollowup,
    status,
    toolSteps,
    currentStep,
    answer,
    reasoningContent,
    intermediateAnswer,
    resources,
    toolSummary,
    error,
    warning,
    isCompacting,
    compactResult,
    steps,
    steerQueue,
    followupQueue,
    sessionId,
    // C10：新增流式/阶段状态
    isStreaming: streamState.isStreaming,
    streamingMessage: streamState.streamingMessage,
    agentPhase,
    // C10 T5：运行中 Session ID 集合，供侧栏显示实时指示
    runningSessionIds
  }
}
