// ?concept=<name>: mock ideas for the app, drawn in the dev preview only.
// Nothing here ships or is translated.
import { TimelineConcept } from './timeline';

export function Concepts({ which }: { which: string }) {
  if (which === 'timeline') return <TimelineConcept />;
  return <p style={{ padding: 24 }}>No concept “{which}”. Try ?concept=timeline.</p>;
}
