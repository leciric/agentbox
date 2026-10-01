// ReportDialog is "Report a problem": the user's message, and everything the
// report would carry beside it, each part shown in full as it will be sent
// (already redacted by the daemon: internal/daemon/report.go) and left out
// with its checkbox. Nothing is sent until they press Send.
import { useMutation, useQuery } from '@tanstack/react-query';
import { Check, ChevronRight, Copy, FolderOpen, LoaderCircle } from 'lucide-react';
import { useEffect, useState } from 'react';
import type * as T from '../../shared/api';
import { api } from '../lib/api';
import { cn, errorMessage } from '../lib/utils';
import { Button } from './ui/button';
import { Notice } from './ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Label, Textarea } from './ui/input';

// draft is the report as it would be sent: the daemon's sections and the
// app's, redacted together.
async function draft(): Promise<T.ReportDraft> {
  const sections = await window.agentbox.report.sections().catch(() => []);
  return api.reportDraft({ sections });
}

export function ReportDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const [message, setMessage] = useState('');
  const [left, setLeft] = useState<Set<string>>(new Set());
  const report = useQuery({ queryKey: ['report-draft'], queryFn: draft, enabled: open, gcTime: 0, staleTime: 0, retry: false });
  const send = useMutation({
    mutationFn: (d: T.ReportDraft) =>
      api.sendReport({ kind: 'problem', message, sections: d.sections.filter((s) => !left.has(s.id)) }),
  });
  useEffect(() => {
    if (!open) return;
    setMessage('');
    setLeft(new Set());
    send.reset();
    // send is stable enough: reset only when the dialog opens again.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const sent = send.data;
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl" data-report-dialog>
        <DialogHeader>
          <DialogTitle>{sent ? 'Report sent' : 'Report a problem'}</DialogTitle>
          <DialogDescription>
            {sent
              ? 'Thank you. AgentBox’s developers have your report.'
              : 'Tell AgentBox’s developers what went wrong. It goes to them with the parts below, which you can read in full and leave out. Tokens, keys, email addresses and home folders are taken out of all of it, your message too.'}
          </DialogDescription>
        </DialogHeader>
        {sent ? (
          <SentReport id={sent.id} />
        ) : (
          <>
            <div className="grid gap-1.5">
              <Label htmlFor="report-message">What happened, and what did you expect?</Label>
              <Textarea
                id="report-message"
                autoFocus
                maxLength={5000}
                value={message}
                onChange={(e) => setMessage(e.target.value)}
                placeholder="Creating an agent stopped at “Starting the machine” and never finished…"
                className="min-h-28"
              />
            </div>
            <div className="grid gap-2">
              <p className="text-[13px] font-medium text-tertiary">What’s sent with it</p>
              {report.isPending && (
                <p className="flex items-center gap-2 text-[13px] text-subtle">
                  <LoaderCircle className="size-4 animate-spin" /> Collecting the logs…
                </p>
              )}
              {report.error && (
                <Notice>
                  AgentBox’s daemon didn’t answer, and it’s what puts a report together and sends it: {errorMessage(report.error)}. The
                  app’s own logs are in its logs folder.
                </Notice>
              )}
              {report.data && (
                <ul className="grid gap-1.5" data-report-sections>
                  <li className="rounded-lg border border-line bg-sunken px-3 py-2 text-[12px] text-subtle">
                    Always: this installation’s random ID <span className="font-mono text-tertiary">{report.data.install}</span>, AgentBox{' '}
                    {report.data.version}, {report.data.os}/{report.data.arch}. To{' '}
                    <span className="font-mono text-tertiary">{report.data.endpoint}</span>.
                  </li>
                  {report.data.sections.map((s) => (
                    <SectionRow
                      key={s.id}
                      section={s}
                      included={!left.has(s.id)}
                      onIncluded={(on) =>
                        setLeft((prev) => {
                          const next = new Set(prev);
                          if (on) next.delete(s.id);
                          else next.add(s.id);
                          return next;
                        })
                      }
                    />
                  ))}
                </ul>
              )}
            </div>
            {send.error && <Notice>{errorMessage(send.error)}</Notice>}
          </>
        )}
        <DialogFooter className="items-center">
          {!('web' in window.agentbox) && (
            <Button variant="ghost" className="mr-auto" onClick={() => void window.agentbox.report.openLogs()}>
              <FolderOpen /> App logs
            </Button>
          )}
          {sent ? (
            <Button variant="primary" onClick={() => onOpenChange(false)}>
              Done
            </Button>
          ) : (
            <>
              <Button variant="ghost" onClick={() => onOpenChange(false)}>
                Cancel
              </Button>
              <Button
                variant="primary"
                data-report-send
                disabled={!report.data || message.trim() === '' || send.isPending}
                onClick={() => report.data && send.mutate(report.data)}
              >
                {send.isPending && <LoaderCircle className="animate-spin" />}
                Send report
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// SectionRow is one part of the report: its checkbox, and its whole text
// behind a disclosure.
function SectionRow({ section, included, onIncluded }: { section: T.ReportSection; included: boolean; onIncluded: (on: boolean) => void }) {
  const [shown, setShown] = useState(false);
  const lines = section.content.split('\n').length;
  return (
    <li className={cn('min-w-0 rounded-lg border border-line', !included && 'opacity-60')} data-report-section={section.id}>
      <div className="flex items-center gap-3 px-3 py-2">
        <input
          type="checkbox"
          aria-label={`Send ${section.title}`}
          checked={included}
          onChange={(e) => onIncluded(e.target.checked)}
          className="size-4 shrink-0 accent-brand-500"
        />
        <button
          type="button"
          aria-expanded={shown}
          onClick={() => setShown((s) => !s)}
          className="flex min-w-0 flex-1 items-center gap-2 text-left text-[13px] text-primary"
        >
          <ChevronRight className={cn('size-4 shrink-0 text-subtle transition-transform', shown && 'rotate-90')} />
          <span className="min-w-0 truncate">{section.title}</span>
          <span className="ml-auto shrink-0 font-mono text-[11px] text-faint">
            {lines} {lines === 1 ? 'line' : 'lines'} · {size(section.content.length)}
          </span>
        </button>
      </div>
      {shown && (
        <pre className="max-h-64 min-w-0 overflow-auto whitespace-pre-wrap [overflow-wrap:anywhere] border-t border-line bg-sunken px-3 py-2 font-mono text-[11px] leading-relaxed text-secondary">
          {section.content}
        </pre>
      )}
    </li>
  );
}

function SentReport({ id }: { id: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="flex items-center gap-3 rounded-lg border border-line bg-sunken px-3 py-2 text-[13px]" data-report-sent>
      <span className="text-subtle">Its ID, to quote if you write to us about it:</span>
      <span className="font-mono text-primary">{id}</span>
      <Button
        size="sm"
        variant="ghost"
        className="ml-auto"
        onClick={() => {
          window.agentbox.copyText(id);
          setCopied(true);
        }}
      >
        {copied ? <Check /> : <Copy />} {copied ? 'Copied' : 'Copy'}
      </Button>
    </div>
  );
}

function size(n: number): string {
  return n < 1024 ? `${n} B` : `${(n / 1024).toFixed(1)} KB`;
}
