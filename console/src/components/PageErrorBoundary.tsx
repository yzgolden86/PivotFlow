import { Component, type ErrorInfo, type ReactNode } from 'react'
import { ErrorState } from '../pages/shared'

type Props = {
  /** 变化时清掉错误。传当前路由即可：换页必须能重新渲染。 */
  resetKey: string
  children: ReactNode
}

type State = { error: Error | null }

/**
 * 页面级错误边界。
 *
 * 为什么必须有：React 在没有错误边界时，渲染期抛异常会**卸载整棵树**，
 * 于是不只是出问题的那个页面白屏，之后点任何导航都是白屏，只能刷新。
 * 实测踩过一次 —— 渠道页因为 `models` 是 null 在 useMemo 里 .filter 抛
 * TypeError，整个控制台从渠道页起全白。
 *
 * 所以这里做两件事：把崩溃限制在当前页面内（侧栏和顶栏还在，能点走），
 * 以及**路由一变就复位**，否则错误页本身会把控制台锁死。
 */
export default class PageErrorBoundary extends Component<Props, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidUpdate(previous: Props) {
    if (this.state.error && previous.resetKey !== this.props.resetKey) {
      this.setState({ error: null })
    }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // 保留控制台报错：崩溃本身要能在 devtools 里看到完整堆栈，
    // 而不是被错误边界吞掉。
    console.error('页面渲染失败', error, info.componentStack)
  }

  render() {
    if (this.state.error) {
      return (
        <ErrorState
          message={this.state.error.message || '页面渲染失败'}
          retry={() => this.setState({ error: null })}
        />
      )
    }
    return this.props.children
  }
}
