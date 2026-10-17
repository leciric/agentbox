// ?concept=<name>: mock layouts and ideas for the app, to choose from. Mock
// only: nothing here ships or is translated.
import { EvidenceConcept } from './evidence';
import { TimelineConcept } from './timeline';

export function Concepts({ which }: { which: string }) {
  return (
    <div style={{ font: '13px var(--font-sans)' }}>
      {which === 'evidence' && <EvidenceConcept />}
      {which === 'timeline' && <TimelineConcept />}
    </div>
  );
}
