// ErrorBoundary keeps a part of the app that throws while rendering (a hover
// card, a right-panel tab) from blanking the whole window: it shows `fallback`
// (nothing by default) until it is remounted, e.g. by a new key.
import { Component, type ReactNode } from 'react';

type Props = { children?: ReactNode; fallback?: ReactNode };
type State = { failed: boolean };

export class ErrorBoundary extends Component<Props, State> {
  state: State = { failed: false };

  static getDerivedStateFromError(): State {
    return { failed: true };
  }

  componentDidCatch(error: unknown) {
    console.error('A part of the app failed to render:', error);
  }

  render(): ReactNode {
    return this.state.failed ? (this.props.fallback ?? null) : this.props.children;
  }
}
