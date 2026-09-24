import { useMutation } from '@tanstack/react-query';
import { CornerUpLeft, GitPullRequest, LoaderCircle, Send } from 'lucide-react';
import { useState, useSyncExternalStore } from 'react';
import type * as T from '../../shared/api';
import * as A from '../../shared/api';
import { api } from '../lib/api';
import { cn, errorMessage, timeAgo } from '../lib/utils';
import { Markdown } from './chat/Markdown';
import { Button } from './ui/button';
import { Textarea } from './ui/input';

// An agent's thread: everything it has reported — it was made, it finished, it
// asked its project's chat something, that question was answered — as the rail
// shows it when you open a row.
//
// This used to be prose in the project's chat, written for the lead to read and
// rendered as a grey box in the middle of the conversation. The chat is now
// what you and the lead said to each other; what the agents report lives beside
// it, where a finish can be a line you skim and a question can carry the box
// you answer it in.

// eventsByAgent groups a project's events by the agent that reported them.
// They arrive newest first; a thread reads oldest first.
export function eventsByAgent(events: T.AgentEvent[]): Map<string, T.AgentEvent[]> {
  const byRef = new Map<string, T.AgentEvent[]>();
  for (const ev of events) {
    const list = byRef.get(ev.ref);
    if (list) list.unshift(ev);
    else byRef.set(ev.ref, [ev]);
  }
  return byRef;
}

export function unreadCount(events: T.AgentEvent[], seenAt: string | undefined): number {
  if (!seenAt) return events.length;
  const since = Date.parse(seenAt);
  return events.filter((ev) => Date.parse(ev.at) > since).length;
}

// latestLine is the one line a closed row gets: what the agent last reported,
// and the least of what came with it. urgent is a question nobody has answered,
// which is the one thing here that is waiting on somebody.
export function latestLine(events: T.AgentEvent[], questions: Map<string, T.Question>): { text: string; urgent: boolean } | undefined {
  const ev = events.at(-1);
  if (!ev) return undefined;
  switch (ev.kind) {
    case A.AgentAsked: {
      const q = questions.get(ev.question ?? '');
      if (!q || q.status === 'answered') return { text: 'asked a question', urgent: false };
      if (q.status === 'cancelled') return { text: 'gave up waiting for an answer', urgent: false };
      return { text: q.status === 'escalated' ? 'asking you something' : 'asking the chat something', urgent: true };
    }
    case A.AgentAnswered: {
      const q = questions.get(ev.question ?? '');
      return { text: q?.answeredBy === 'user' ? 'answered by you' : 'answered by the chat', urgent: false };
    }
    case A.AgentCreated:
      return { text: 'created', urgent: false };
    default: {
      const parts = ['finished'];
      const c = ev.changes;
      if (c && c.files > 0) parts.push(`${c.files} file${c.files === 1 ? '' : 's'}`);
      else if (c) parts.push('no changes');
      if (ev.pr) parts.push(`PR #${ev.pr.number}`);
      return { text: parts.join(' · '), urgent: false };
    }
  }
}

// AgentThread is the opened row: the events in order, and the box to write back
// to the agent in.
export function AgentThread({
  agent,
  name,
  running,
  events,
  questions,
}: {
  agent: string; // the agent's ref
  name: string;
  running: boolean;
  events: T.AgentEvent[];
  questions: Map<string, T.Question>;
}) {
  return (
    <div className="ml-4 border-l border-line pb-2 pl-2.5 pr-1 pt-0.5" data-thread={agent}>
      <div className="grid gap-1.5">
        {events.map((ev) => (
          <EventCard key={ev.id} event={ev} question={ev.question ? questions.get(ev.question) : undefined} />
        ))}
      </div>
      <ReplyBox agent={agent} name={name} running={running} />
    </div>
  );
}

const kindLabels: Record<string, string> = {
  [A.AgentCreated]: 'Created',
  [A.AgentFinished]: 'Finished',
  [A.AgentAsked]: 'Asked',
  [A.AgentAnswered]: 'Answered',
};

function EventCard({ event, question }: { event: T.AgentEvent; question?: T.Question }) {
  return (
    <div className="min-w-0 rounded-lg border border-line-faint bg-rail p-2" data-thread-event={event.kind}>
      <div className="flex items-baseline gap-1.5 text-[10px] uppercase tracking-[0.07em]">
        <span className={cn('font-semibold', event.kind === A.AgentAsked ? 'text-amber-300/90' : 'text-subtle')}>{kindLabels[event.kind] ?? event.kind}</span>
        <span className="ml-auto shrink-0 normal-case tracking-normal text-faint">{timeAgo(event.at)}</span>
      </div>

      {event.summary && (
        <blockquote className="mt-1.5 border-l-2 border-line-strong pl-2">
          <Markdown text={event.summary} className="text-[12px] leading-relaxed text-tertiary" />
          {event.cut && <p className="mt-1 text-[10.5px] text-faint">The start of a longer summary.</p>}
        </blockquote>
      )}

      {event.changes && <ChangesLine changes={event.changes} />}
      {event.pr && <PullRequestLink pr={event.pr} />}
      {question && (event.kind === A.AgentAnswered ? <AnswerBlock question={question} /> : <QuestionBlock question={question} />)}
    </div>
  );
}

function ChangesLine({ changes }: { changes: T.AgentChanges }) {
  if (changes.files === 0) return <p className="mt-1.5 text-[11px] text-faint">Changed nothing.</p>;
  return (
    <p className="mt-1.5 flex flex-wrap items-center gap-x-1.5 text-[11px] tabular-nums text-subtle" data-thread-changes>
      <span>
        {changes.files} file{changes.files === 1 ? '' : 's'}
      </span>
      <span className="text-emerald-400/80">+{changes.insertions}</span>
      <span className="text-rose-400/80">−{changes.deletions}</span>
      {changes.dirty && <span className="text-amber-300/80">uncommitted</span>}
    </p>
  );
}

function PullRequestLink({ pr }: { pr: T.PullRequest }) {
  return (
    <a
      href={pr.url}
      data-thread-pr={pr.number}
      className="mt-1.5 flex items-center gap-1.5 text-[11.5px] text-muted transition hover:text-primary"
      onClick={(event) => {
        event.preventDefault();
        void window.agentbox.openExternal(pr.url);
      }}
    >
      <GitPullRequest className="size-3.5 shrink-0" />
      <span className="min-w-0 flex-1 truncate">
        #{pr.number}
        {pr.title ? ` ${pr.title}` : ''}
      </span>
    </a>
  );
}

// QuestionBlock is the question as it stands now, with the box to answer it in
// while it is still waiting. Answering goes through the same route the command
// line uses, so an answer given here reaches the agent that is blocked on it.
// Once it is answered, the answer is the next event in the thread rather than
// something repeated here.
function QuestionBlock({ question }: { question: T.Question }) {
  const [answer, setAnswer] = useState('');
  const send = useMutation({
    mutationFn: () => api.answerQuestion(question.project, question.id, answer.trim()),
    onSuccess: () => setAnswer(''),
  });
  const waiting = question.status === 'pending' || question.status === 'escalated';
  return (
    <div className="mt-1.5" data-thread-question={question.id}>
      <p className="whitespace-pre-wrap break-words text-[12.5px] leading-relaxed text-secondary">{question.question}</p>
      {question.context && <p className="mt-1 break-words text-[11px] leading-relaxed text-subtle">What it was doing: {question.context}</p>}
      {question.escalation && <p className="mt-1 break-words text-[11px] leading-relaxed text-amber-300/80">The chat passed it to you: {question.escalation}</p>}
      {question.status === 'cancelled' && <p className="mt-1.5 text-[11px] text-faint">Nobody answered in time; the agent decided for itself.</p>}

      {waiting && (
        <form
          className="mt-1.5 grid gap-1.5"
          onSubmit={(event) => {
            event.preventDefault();
            if (answer.trim() && !send.isPending) send.mutate();
          }}
        >
          <Textarea
            aria-label={`Answer ${question.agent}`}
            className="min-h-16 text-[12px]"
            placeholder="Say what it should do…"
            value={answer}
            onChange={(event) => setAnswer(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) event.currentTarget.form?.requestSubmit();
            }}
          />
          <Button type="submit" size="sm" variant="primary" className="justify-self-end" disabled={!answer.trim() || send.isPending}>
            {send.isPending ? <LoaderCircle className="animate-spin" /> : <Send />}
            Answer
          </Button>
          {send.error && <p className="text-[11px] text-rose-300">{errorMessage(send.error)}</p>}
        </form>
      )}
    </div>
  );
}

// AnswerBlock is what the question got, on the event that records the
// answering. The question itself is a few lines above, on the event that
// records the asking, so only the answer is worth the room.
function AnswerBlock({ question }: { question: T.Question }) {
  return (
    <div className="mt-1.5" data-thread-answer={question.id}>
      <p className="text-[10px] uppercase tracking-[0.07em] text-faint">By {question.answeredBy === 'user' ? 'you' : 'the chat'}</p>
      <p className="mt-0.5 whitespace-pre-wrap break-words text-[12.5px] leading-relaxed text-secondary">{question.answer}</p>
    </div>
  );
}

// ReplyBox writes to the agent itself, the way the chat's own composer writes
// to the lead. What the agent says back arrives as the next thing in this
// thread, when its turn ends.
function ReplyBox({ agent, name, running }: { agent: string; name: string; running: boolean }) {
  const [text, setText] = useState('');
  const [sent, setSent] = useState(false);
  const send = useMutation({
    mutationFn: () => api.sendChat(agent, text.trim()),
    onSuccess: () => {
      setText('');
      setSent(true);
      setTimeout(() => setSent(false), 4000);
    },
  });
  return (
    <form
      className="mt-1.5 grid gap-1.5"
      onSubmit={(event) => {
        event.preventDefault();
        if (text.trim() && !send.isPending) send.mutate();
      }}
    >
      <Textarea
        aria-label={`Reply to ${name}`}
        className="min-h-14 text-[12px]"
        disabled={!running}
        placeholder={running ? `Reply to ${name}…` : `${name} isn't running`}
        value={text}
        onChange={(event) => setText(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) event.currentTarget.form?.requestSubmit();
        }}
      />
      <div className="flex items-center gap-2">
        <span className="min-w-0 flex-1 truncate text-[11px] text-faint">
          {send.error ? <span className="text-rose-300">{errorMessage(send.error)}</span> : sent ? `Sent to ${name}` : ''}
        </span>
        <Button type="submit" size="sm" disabled={!running || !text.trim() || send.isPending}>
          {send.isPending ? <LoaderCircle className="animate-spin" /> : <CornerUpLeft />}
          Reply
        </Button>
      </div>
    </form>
  );
}

// What you have already read, per agent, kept in this browser: it is about
// this window's attention, not about the project, so it doesn't belong in the
// daemon's state.
const seenKey = 'agentbox.threads.seen';
const seenListeners = new Set<() => void>();
let seenCache: Record<string, string> = readSeen();

function readSeen(): Record<string, string> {
  try {
    return (JSON.parse(localStorage.getItem(seenKey) ?? '{}') as Record<string, string>) ?? {};
  } catch {
    return {};
  }
}

export function markSeen(ref: string, at: string): void {
  if (seenCache[ref] === at) return;
  seenCache = { ...seenCache, [ref]: at };
  localStorage.setItem(seenKey, JSON.stringify(seenCache));
  for (const listener of seenListeners) listener();
}

export function useSeen(): Record<string, string> {
  return useSyncExternalStore((listener) => {
    seenListeners.add(listener);
    return () => {
      seenListeners.delete(listener);
    };
  }, () => seenCache);
}
