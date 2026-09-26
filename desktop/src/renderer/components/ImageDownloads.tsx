import { useState } from 'react';
import type * as T from '../../shared/api';
import { cn } from '../lib/utils';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { Code } from './ui/card';

// The flag that turns each optional component on, for the row that is off.
const flags: Record<string, string> = {
  android: '--android',
  codex: '--codex',
  opencode: '--opencode',
  'dev-caches': '--dev-caches',
  incus: '--incus',
};
// keys maps an option to its field in ImageComponents, where the names differ.
const keys: Record<string, keyof T.ImageComponents> = { 'dev-caches': 'devCaches' };

function size(mb: number): string {
  return mb < 1000 ? `${mb} MB` : `${(mb / 1000).toFixed(1)} GB`;
}

// ImageDownloads says what making the base image here fetches, so the minutes
// it takes are a list of things rather than a spinner. Optional components are
// shown whether they're on or not: what they'd cost is the point of the choice.
export function ImageDownloads({ image }: { image?: T.ImageBuild }) {
  const [open, setOpen] = useState(false);
  if (!image) return null;
  const on = (option?: string) => !option || image.components[keys[option] ?? (option as keyof T.ImageComponents)] === true;
  const total = image.downloads.filter((d) => on(d.option)).reduce((sum, d) => sum + d.mb, 0);

  return (
    <div className="grid gap-2" data-image-downloads={total}>
      <Button size="sm" variant="ghost" className="justify-self-start" aria-expanded={open} onClick={() => setOpen(!open)}>
        What building it here downloads: about {size(total)}
      </Button>
      {open && (
        <div className="overflow-hidden rounded-xl border border-line">
          <table className="w-full text-[12.5px]">
            <tbody>
              {image.downloads.map((d) => (
                <tr key={d.name} className={cn('border-t border-line-faint first:border-t-0', !on(d.option) && 'text-faint')}>
                  <td className="py-1.5 pl-3 pr-2 align-top">
                    <span className={cn('whitespace-nowrap', on(d.option) ? 'text-secondary' : 'line-through')}>{d.name}</span>
                    {d.option && (
                      <Badge className="ml-1.5 align-[1px]" variant={on(d.option) ? 'brand' : undefined}>
                        optional
                      </Badge>
                    )}
                  </td>
                  <td className={cn('py-1.5 pr-2 text-right align-top tabular-nums', on(d.option) ? 'text-muted' : '')}>{size(d.mb)}</td>
                  <td className="py-1.5 pr-3 align-top text-subtle">
                    {d.purpose}
                    {d.option && !on(d.option) && <> Turn it on with <Code>agentbox image build {flags[d.option]}</Code>.</>}
                  </td>
                </tr>
              ))}
              <tr className="border-t border-line bg-surface-faint">
                <td className="py-1.5 pl-3 pr-2 text-tertiary">Total</td>
                <td className="py-1.5 pr-2 text-right tabular-nums text-tertiary">{size(total)}</td>
                <td className="py-1.5 pr-3 text-subtle">{image.hint}.</td>
              </tr>
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
