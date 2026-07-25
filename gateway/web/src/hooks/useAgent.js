import { useState, useCallback, useRef } from 'react'
import { agentApi } from '../api/client'

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
  // 按真实时间顺序维护的 Agent 执行步骤，用于线性展示 thinking / tool / answer
  const [steps, setSteps] = useState([])
  const abortRef = useRef(false)

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
    setSteps([])
    abortRef.current = false
  }, [])

  const execute = useCallback(({ conversationId, message, attachments = [], history = [], model, provider, baseUrl, apiKey, enableTools, enableWebSearch, searchProvider, assistantId }) => {
    reset()
    setStatus('executing')
    abortRef.current = false

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
          // 会话绑定的助手；空字符串时不传该字段，后端回退到会话值/全部已激活工具
          assistant_id: assistantId || undefined
        },
        (event) => {
          if (abortRef.current) return
          switch (event.event) {
            case 'tool_call': {
              const callStep = {
                step: event.data.step,
                tool: event.data.tool,
                arguments: event.data.arguments,
                status: 'running'
              }
              finalToolSteps.push(callStep)
              finalSteps = [...finalSteps, { type: 'tool_call', ...callStep }]
              setToolSteps(prev => [...prev, callStep])
              setCurrentStep(event.data.step)
              setSteps(prev => [...prev, { type: 'tool_call', ...callStep }])
              break
            }
            case 'tool_result': {
              const { step, tool, status, output, error_message } = event.data
              const resultIndex = finalToolSteps.findIndex(s => s.step === step)
              if (resultIndex >= 0) {
                finalToolSteps[resultIndex] = {
                  ...finalToolSteps[resultIndex],
                  status,
                  output,
                  error: error_message
                }
              }
              // 同步更新 finalSteps，确保 resolve 时一定拿到最新状态
              const stepIndex = finalSteps.findIndex(s => (s.type === 'tool_call' || s.type === 'tool_result') && s.step === step)
              if (stepIndex >= 0) {
                finalSteps = finalSteps.map((s, idx) =>
                  idx === stepIndex
                    ? { ...s, status, output, error: error_message }
                    : s
                )
              } else {
                finalSteps = [...finalSteps, { type: 'tool_result', step, tool, status, output, error: error_message }]
              }
              // 函数式更新 React state，避免闭包被覆盖
              setToolSteps(prev => prev.map(s =>
                s.step === step
                  ? { ...s, status, output, error: error_message }
                  : s
              ))
              setSteps(prev => {
                const idx = prev.findIndex(s => (s.type === 'tool_call' || s.type === 'tool_result') && s.step === step)
                if (idx >= 0) {
                  return prev.map((s, i) =>
                    i === idx
                      ? { ...s, status, output, error: error_message }
                      : s
                  )
                }
                return [...prev, { type: 'tool_result', step, tool, status, output, error: error_message }]
              })
              break
            }
            case 'reasoning': {
              const delta = event.data.delta || ''
              finalReasoning += delta
              const last = finalSteps[finalSteps.length - 1]
              if (last && last.type === 'thinking') {
                finalSteps = finalSteps.map((s, idx) =>
                  idx === finalSteps.length - 1
                    ? { ...s, content: s.content + delta }
                    : s
                )
              } else {
                finalSteps = [...finalSteps, { type: 'thinking', content: delta }]
              }
              setReasoningContent(prev => prev + delta)
              setSteps(prev => {
                const lastStep = prev[prev.length - 1]
                if (lastStep && lastStep.type === 'thinking') {
                  return prev.map((s, idx) =>
                    idx === prev.length - 1
                      ? { ...s, content: s.content + delta }
                      : s
                  )
                }
                return [...prev, { type: 'thinking', content: delta }]
              })
              break
            }
            case 'intermediate_answer': {
              const delta = event.data.delta || ''
              finalIntermediate += delta
              const last = finalSteps[finalSteps.length - 1]
              if (last && last.type === 'intermediate') {
                finalSteps = finalSteps.map((s, idx) =>
                  idx === finalSteps.length - 1
                    ? { ...s, content: s.content + delta }
                    : s
                )
              } else {
                finalSteps = [...finalSteps, { type: 'intermediate', content: delta }]
              }
              setIntermediateAnswer(prev => prev + delta)
              setSteps(prev => {
                const lastStep = prev[prev.length - 1]
                if (lastStep && lastStep.type === 'intermediate') {
                  return prev.map((s, idx) =>
                    idx === prev.length - 1
                      ? { ...s, content: s.content + delta }
                      : s
                  )
                }
                return [...prev, { type: 'intermediate', content: delta }]
              })
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
            case 'done':
              setStatus('done')
              if (finalError) {
                reject(new Error(finalError))
              } else {
                resolve({
                  answer: finalAnswer,
                  toolSteps: finalToolSteps,
                  reasoningContent: finalReasoning,
                  intermediateAnswer: finalIntermediate,
                  resources: finalResources,
                  toolSummary: finalToolSummary,
                  warning: finalWarning,
                  sessionId: event.data?.session_id || '',
                  steps: finalSteps
                })
              }
              break
          }
        }
      ).catch(err => {
        setStatus('error')
        setError(err.message || '请求失败')
        reject(err)
      })
    })
  }, [reset])

  const abort = useCallback(() => {
    abortRef.current = true
  }, [])

  return {
    execute,
    abort,
    reset,
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
    steps
  }
}
