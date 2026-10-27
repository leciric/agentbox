import { useQuery } from '@tanstack/react-query';
import { ChevronLeft, ChevronRight } from 'lucide-react';
import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { api } from '../../lib/api';
import { formatSize } from '../../lib/files';
import { useT } from '../../lib/i18n';
import { errorMessage } from '../../lib/utils';
import { BarButton, DataGrid, type GridCell } from './DataGrid';

const pageSize = 500;

// SqliteView browses a database in the worktree, read-only through the
// daemon (worktreefs.SQLite): a table or view chosen from the list, its rows a
// page at a time. fallback is what shows when the database can't be read.
export function SqliteView({ chatRef, path, fallback }: { chatRef: string; path: string; fallback: (error: string) => ReactNode }) {
  const t = useT();
  const tables = useQuery({
    queryKey: ['sqlite', chatRef, path],
    queryFn: () => api.sqlite(chatRef, path),
    retry: false,
  });
  const [table, setTable] = useState('');
  const [offset, setOffset] = useState(0);
  useEffect(() => {
    setTable('');
    setOffset(0);
  }, [path]);
  const list = tables.data?.tables ?? [];
  const chosen = list.some((tb) => tb.name === table) ? table : (list.find((tb) => !tb.view) ?? list[0])?.name ?? '';
  const rows = useQuery({
    queryKey: ['sqlite', chatRef, path, chosen, offset],
    queryFn: () => api.sqlite(chatRef, path, chosen, offset, pageSize),
    enabled: !!chosen,
    retry: false,
    placeholderData: (previous) => (previous?.table === chosen ? previous : undefined),
  });
  const data = rows.data;
  const widths = useMemo(() => {
    if (!data) return [];
    return data.columns.map((name, c) => {
      let w = name.length;
      for (const row of data.rows.slice(0, 200)) w = Math.max(w, show(row[c]).text.length);
      return Math.min(40, Math.max(4, w));
    });
  }, [data]);

  if (tables.isPending) return <div className="skeleton m-4 h-3.5 w-1/2 rounded" />;
  if (tables.error) return fallback(errorMessage(tables.error));
  if (list.length === 0) return <div className="flex h-full items-center justify-center p-6 text-[12.5px] text-subtle">{t('files.sqlite.empty')}</div>;

  const total = data?.total ?? -1;
  const shown = data?.rows.length ?? 0;
  const more = total >= 0 ? offset + shown < total : shown === pageSize;
  return (
    <div className="flex h-full flex-col" data-file-sqlite>
      <div className="flex h-8 shrink-0 items-center gap-1.5 border-b border-line-faint px-2 text-[12px] text-subtle">
        <select
          className="min-w-0 max-w-[60%] truncate rounded-md border border-line bg-transparent px-1.5 py-0.5 text-secondary outline-none focus:border-line-vivid"
          value={chosen}
          aria-label={t('files.sqlite.table')}
          onChange={(e) => {
            setTable(e.target.value);
            setOffset(0);
          }}
        >
          {list.map((tb) => (
            <option key={tb.name} value={tb.name} className="bg-composer">
              {tb.view ? t('files.sqlite.view', { name: tb.name }) : tb.name}
            </option>
          ))}
        </select>
        <span className="flex-1" />
        <span className="truncate tabular-nums" data-sqlite-range>
          {shown === 0
            ? t('files.sqlite.noRows')
            : total >= 0
              ? t('files.sqlite.range', { from: (offset + 1).toLocaleString(), to: (offset + shown).toLocaleString(), total: total.toLocaleString() })
              : t('files.sqlite.rangeOpen', { from: (offset + 1).toLocaleString(), to: (offset + shown).toLocaleString() })}
        </span>
        <BarButton label={t('files.sqlite.previous')} disabled={offset === 0 || rows.isFetching} onClick={() => setOffset(Math.max(0, offset - pageSize))}>
          <ChevronLeft />
        </BarButton>
        <BarButton label={t('files.sqlite.next')} disabled={!more || rows.isFetching} onClick={() => setOffset(offset + pageSize)}>
          <ChevronRight />
        </BarButton>
      </div>
      <div className="min-h-0 flex-1">
        {rows.error ? (
          <div className="flex h-full items-center justify-center p-6 text-center text-[12.5px] text-subtle">{t('files.sqlite.failed', { error: errorMessage(rows.error) })}</div>
        ) : !data ? (
          <div className="skeleton m-4 h-3.5 w-2/3 rounded" />
        ) : (
          <DataGrid
            key={`${chosen}:${offset}`}
            columns={data.columns}
            widths={widths}
            rowCount={data.rows.length}
            firstRow={offset + 1}
            label={chosen}
            cell={(r, c) => show(data.rows[r][c])}
          />
        )}
      </div>
    </div>
  );
}

// show is a cell as the daemon sends it (T.SQLiteQuery) for the grid.
function show(v: unknown): GridCell {
  if (v === null || v === undefined) return { text: 'NULL', kind: 'null' };
  if (typeof v === 'number') return { text: String(v), kind: 'number' };
  if (typeof v === 'object' && 'blob' in v) return { text: `BLOB ${formatSize(Number((v as { blob: number }).blob))}`, kind: 'blob' };
  return { text: String(v) };
}
