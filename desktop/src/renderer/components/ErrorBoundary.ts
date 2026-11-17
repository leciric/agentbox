// ErrorBoundary keeps a part of the app that throws while rendering (a hover
// card, a right-panel tab, or the whole window, from main.tsx) from blanking
// everything around it: it shows `fallback` (nothing by default) until it is
// remounted, e.g. by a new key. A function fallback gets the error and a
// retry that renders the children again.
import { Component, type ReactNode } from 'react';

type Props = { children?: ReactNode; fallback?: ReactNode | ((error: unknown, retry: () => void) => ReactNode) };
type State = { failed: boolean; error?: unknown };

export class ErrorBoundary extends Component<Props, State> {
  state: State = { failed: false };

  static getDerivedStateFromError(error?: unknown): State {
    return error === undefined ? { failed: true } : { failed: true, error };
  }

  componentDidCatch(error: unknown) {
    console.error('A part of the app failed to render:', error);
  }

  retry = () => this.setState({ failed: false, error: undefined });

  render(): ReactNode {
    if (!this.state.failed) return this.props.children;
    const { fallback } = this.props;
    return typeof fallback === 'function' ? fallback(this.state.error, this.retry) : (fallback ?? null);
  }
}
