// A message's markdown, with react-native-marked (marked's lexer, drawn as
// React Native views; it runs in Expo Go, unlike t3code's native
// react-native-enriched-markdown). Rich renders, the ```svg and
// ```html preview blocks the desktop draws in a chat, are drawn in a frame
// with no scripts and no network, as the web phone app does; a ```mermaid
// diagram needs its script, so it stays code here.
import { createElement, useMemo } from 'react';
import { Linking, Platform, ScrollView, StyleSheet, Text, View, type TextStyle, type ViewStyle } from 'react-native';
import { Renderer, useMarkdown, type MarkedStyles } from 'react-native-marked';
import { WebView } from 'react-native-webview';
import { useT } from '../../lib/i18n.tsx';
import { colors, mono } from '../../ui/theme.ts';
import { framePage, richKind } from './rich.ts';

function Frame({ kind, body }: { kind: 'svg' | 'html'; body: string }) {
  const html = framePage(kind, body);
  const height = kind === 'svg' ? 220 : 280;
  if (Platform.OS === 'web') {
    // Expo's web build: the browser's own frame, sandboxed.
    return (
      <View style={[styles.frame, { height }]}>
        {createElement('iframe', { srcDoc: html, sandbox: '', style: { border: 0, width: '100%', height: '100%', background: '#fff' }, title: kind })}
      </View>
    );
  }
  return (
    <View style={[styles.frame, { height }]}>
      <WebView source={{ html }} originWhitelist={['about:blank']} javaScriptEnabled={false} scrollEnabled={kind === 'html'} style={{ backgroundColor: '#fff' }} />
    </View>
  );
}

class ChatRenderer extends Renderer {
  private readonly diagramNote: string;
  constructor(diagramNote: string) {
    super({ selectable: true });
    this.diagramNote = diagramNote;
  }

  code(text: string, language?: string, containerStyle?: ViewStyle, textStyle?: TextStyle) {
    const kind = richKind(language);
    if (kind === 'svg' || kind === 'html') return <Frame key={this.getKey()} kind={kind} body={text} />;
    return (
      <View key={this.getKey()} style={[containerStyle, styles.code]}>
        {kind === 'mermaid' && <Text style={styles.codeNote}>{this.diagramNote}</Text>}
        <ScrollView horizontal showsHorizontalScrollIndicator={false}>
          <Text selectable style={[textStyle, mono, styles.codeText]}>
            {text}
          </Text>
        </ScrollView>
      </View>
    );
  }

  link(children: string | React.ReactNode[], href: string, style?: TextStyle) {
    return (
      <Text key={this.getKey()} style={style} onPress={() => void Linking.openURL(href)} accessibilityRole="link">
        {children}
      </Text>
    );
  }
}

const markedStyles: MarkedStyles = {
  text: { color: colors.primary, fontSize: 15, lineHeight: 22 },
  paragraph: { paddingVertical: 3 },
  strong: { fontWeight: '700', color: colors.title },
  em: { fontStyle: 'italic' },
  link: { color: colors.brand300, textDecorationLine: 'underline' },
  codespan: { ...mono, fontSize: 13, color: colors.secondary, backgroundColor: colors.raised },
  code: { backgroundColor: 'rgba(0,0,0,0.35)', borderRadius: 10, padding: 10, marginVertical: 6 },
  codeText: { color: colors.secondary, fontSize: 12.5, lineHeight: 18 },
  h1: { color: colors.title, fontSize: 20, fontWeight: '700', marginVertical: 6 },
  h2: { color: colors.title, fontSize: 18, fontWeight: '700', marginVertical: 5 },
  h3: { color: colors.title, fontSize: 16, fontWeight: '600', marginVertical: 4 },
  blockquote: { borderLeftWidth: 3, borderLeftColor: colors.lineStrong, paddingLeft: 10 },
  li: { color: colors.primary, fontSize: 15, lineHeight: 22 },
  hr: { backgroundColor: colors.lineStrong, height: StyleSheet.hairlineWidth, marginVertical: 8 },
  table: { borderColor: colors.lineStrong },
  tableCell: { padding: 6 },
};

export function Markdown({ text }: { text: string }) {
  const t = useT();
  const renderer = useMemo(() => new ChatRenderer(t('mobile.chat.diagram')), [t]);
  const nodes = useMarkdown(text, { renderer, styles: markedStyles, colorScheme: 'dark', theme: { colors: { code: 'rgba(0,0,0,0.35)', link: colors.brand300, text: colors.primary, border: colors.lineStrong } } });
  return <View style={styles.root}>{nodes}</View>;
}

const styles = StyleSheet.create({
  root: { minWidth: 0 },
  frame: { borderRadius: 12, overflow: 'hidden', marginVertical: 6, backgroundColor: '#fff' },
  code: { gap: 6 },
  codeNote: { color: colors.subtle, fontSize: 11.5 },
  codeText: { fontSize: 12.5, lineHeight: 18, color: colors.secondary },
});
