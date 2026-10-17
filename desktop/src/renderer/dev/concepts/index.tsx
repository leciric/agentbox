// Concepts are mock-ups for choosing a design, in the dev preview only
// (?concept=…): nothing here ships, and their strings aren't translated.
import { PipConcept } from './pip';

export { installPip } from './pip';

export function Concepts({ which }: { which: string }) {
  switch (which) {
    case 'pip':
      return <PipConcept />;
    default:
      return <div style={{ padding: 24, font: '13px var(--font-sans)', color: 'var(--color-muted)' }}>No concept “{which}”. Try ?concept=pip.</div>;
  }
}
