import React from 'react'

// Keeps a crash in one page from blanking the whole app: shows the error in
// place of the page; resetKey (the route) clears it on navigation.
interface Props {
  resetKey: string
  children: React.ReactNode
}

interface State {
  error: Error | null
  key: string
}

export class PageErrorBoundary extends React.Component<Props, State> {
  state: State = { error: null, key: this.props.resetKey }

  static getDerivedStateFromError(error: Error): Partial<State> {
    return { error }
  }

  static getDerivedStateFromProps(props: Props, state: State): Partial<State> | null {
    return props.resetKey !== state.key ? { error: null, key: props.resetKey } : null
  }

  componentDidCatch(error: Error, info: React.ErrorInfo) {
    console.error('page crashed', error, info.componentStack)
  }

  render() {
    if (this.state.error) {
      return (
        <div role="alert" className="rounded-lg border border-red-200 bg-red-50 p-4 text-sm text-red-800 dark:border-red-800 dark:bg-red-900/20 dark:text-red-200">
          <p className="font-semibold">This page failed to load.</p>
          <p className="mt-1 break-words">{this.state.error.message}</p>
          <p className="mt-2 text-xs">Other pages still work; please report this error.</p>
        </div>
      )
    }
    return this.props.children
  }
}
