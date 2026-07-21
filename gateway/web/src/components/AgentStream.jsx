import { Bot, Wrench, CheckCircle, XCircle, Loader2, AlertTriangle } from 'lucide-react'

export default function AgentStream({ status, toolSteps, currentStep, answer, warning }) {
  if (status === 'idle') return null

  const isExecuting = status === 'executing'
  const hasRunningTool = toolSteps.some((s) => s.status === 'running')

  return (
    <div className="space-y-2 text-sm">
      {/* 工具调用列表 */}
      {toolSteps.length > 0 && (
        <div className="bg-eleball-surface-variant rounded-lg p-3 space-y-2">
          <div className="flex items-center gap-2 text-eleball-text-secondary">
            <Wrench className="w-4 h-4" />
            <span>工具调用</span>
          </div>
          <div className="space-y-1">
            {toolSteps.map((step) => {
              // 兼容旧数据：历史消息未保存 status 时，默认视为已完成
              const status = step.status || (status === 'done' ? 'succeeded' : '')
              return (
                <div
                  key={step.step}
                  className={`flex items-center gap-2 p-2 rounded ${
                    status === 'running'
                      ? 'bg-eleball-primary-light/30'
                      : status === 'succeeded'
                      ? 'bg-green-50'
                      : status === 'failed'
                      ? 'bg-red-50'
                      : 'bg-white'
                  }`}
                >
                  {status === 'running' && currentStep === step.step ? (
                    <Loader2 className="w-4 h-4 animate-spin text-eleball-primary" />
                  ) : status === 'succeeded' ? (
                    <CheckCircle className="w-4 h-4 text-green-600" />
                  ) : status === 'failed' ? (
                    <XCircle className="w-4 h-4 text-red-600" />
                  ) : (
                    <Bot className="w-4 h-4 text-eleball-text-tertiary" />
                  )}
                  <span className="font-medium">{step.tool}</span>
                  {step.error && <span className="text-red-600 text-xs ml-2">{step.error}</span>}
                </div>
              )
            })}
          </div>
        </div>
      )}

      {/* 执行中/思考中提示 */}
      {isExecuting && (
        <div className="flex items-center gap-2 text-eleball-text-secondary px-1">
          <Loader2 className="w-4 h-4 animate-spin text-eleball-primary" />
          <span>{hasRunningTool ? '正在执行工具，请稍等…' : 'Agent 正在思考…'}</span>
        </div>
      )}

      {/* 后端警告（如工具调用次数已达上限） */}
      {warning && (
        <div className="flex items-center gap-2 text-amber-600 text-xs px-1">
          <AlertTriangle className="w-3.5 h-3.5" />
          <span>{warning}</span>
        </div>
      )}

      {/* 流式生成的回答 */}
      {(status === 'answering' || status === 'done' || answer) && (
        <div className="flex items-start gap-2">
          <Bot className="w-4 h-4 mt-1 text-eleball-primary" />
          <div className="flex-1 whitespace-pre-wrap">{answer}</div>
        </div>
      )}
    </div>
  )
}
