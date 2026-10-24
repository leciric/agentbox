// A part of a screen that failed to render, shown as an error in its place
// rather than taking the app down: React unmounts the whole tree on an error
// no boundary catches, which a release build turns into a crash. The root
// has one (app/_layout.tsx), and so does each row of a list, so one item the
// phone can't show costs that row only.
import { Component, type ReactNode } from 'react';
import { StyleSheet, Text, View } from 'react-native';
import { colors, mono } from './theme.ts';

interface Props {
  // fallback renders the error; a short line saying what failed by default.
  fallback?: (error: Error, retry: () => void) => ReactNode;
  // label says what failed, in the default fallback.
  label?: string;
  // resetKey renders the children again when it changes: new data may be fine.
  resetKey?: unknown;
  onError?: (error: Error) => void;
  children?: ReactNode;
}

interface State {
  error: Error | null;
  key: unknown;
}

export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null, key: this.props.resetKey };

  static getDerivedStateFromError(error: unknown): Partial<State> {
    return { error: error instanceof Error ? error : new Error(String(error)) };
  }

  static getDerivedStateFromProps(props: Props, state: State): Partial<State> | null {
    return Object.is(props.resetKey, state.key) ? null : { error: null, key: props.resetKey };
  }

  componentDidCatch(error: Error): void {
    console.error(`[ErrorBoundary] ${this.props.label ?? 'render'}:`, error);
    this.props.onError?.(error);
  }

  retry = () => this.setState({ error: null });

  render() {
    const { error } = this.state;
    if (!error) return this.props.children;
    if (this.props.fallback) return this.props.fallback(error, this.retry);
    return <ErrorLine label={this.props.label} error={error} />;
  }
}

export function ErrorLine({ label, error }: { label?: string; error: Error }) {
  return (
    <View style={styles.line} accessibilityRole="alert">
      <Text style={styles.text} numberOfLines={2}>
        {label ? `${label}: ` : ''}
        <Text style={mono}>{error.message || error.name}</Text>
      </Text>
    </View>
  );
}

const styles = StyleSheet.create({
  line: { borderRadius: 12, paddingHorizontal: 12, paddingVertical: 8, backgroundColor: colors.roseSoft },
  text: { color: colors.rose, fontSize: 12 },
});
