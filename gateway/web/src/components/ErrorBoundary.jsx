import { Component } from 'react'

// 渲染错误兜底：流式输出期间单条消息渲染抛错时不至于整页白屏。
// 默认 fallback 会把错误信息与组件堆栈直接展示在页面上，便于定位抛错组件；
// 可用 fallback prop 自定义（接收 error、info 与重置回调）。
export default class ErrorBoundary extends Component {
  constructor(props) {
    super(props)
    this.state = { hasError: false, error: null, info: null }
  }

  static getDerivedStateFromError(error) {
    return { hasError: true, error }
  }

  componentDidCatch(error, info) {
    console.error('ErrorBoundary 捕获渲染错误:', error, info)
    this.setState({ info })
  }

  handleReset = () => {
    this.setState({ hasError: false, error: null, info: null })
  }

  render() {
    if (this.state.hasError) {
      if (typeof this.props.fallback === 'function') {
        return this.props.fallback(this.state.error, this.handleReset, this.state.info)
      }
      const msg = this.state?.error?.message || String(this.state?.error || '')
      const stack = this.state?.info?.componentStack || ''
      return (
        <div className="flex flex-col items-center justify-center min-h-[60%] text-center p-6 max-w-2xl mx-auto">
          <p className="text-sm text-eleball-text-secondary mb-3">渲染出错，请重试</p>
          {msg && (
            <pre className="text-xs text-left text-red-600 bg-red-50 border border-red-200 rounded-lg p-3 mb-3 overflow-auto max-h-40 w-full whitespace-pre-wrap break-all font-mono">
              {msg}
            </pre>
          )}
          {stack && (
            <details className="text-left w-full mb-3">
              <summary className="text-xs text-eleball-text-tertiary cursor-pointer">组件堆栈</summary>
              <pre className="text-[11px] text-eleball-text-tertiary bg-eleball-surface-variant rounded-lg p-2 mt-1 overflow-auto max-h-40 whitespace-pre-wrap break-all font-mono">
                {stack}
              </pre>
            </details>
          )}
          <button onClick={this.handleReset} className="btn-primary px-4 py-1.5 rounded-full text-sm">
            重试
          </button>
        </div>
      )
    }
    return this.props.children
  }
}
