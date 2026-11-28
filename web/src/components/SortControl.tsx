import React from 'react';
import { ArrowDown, ArrowUp, ArrowUpDown } from 'lucide-react';

export type SortDir = 'desc' | 'asc';

export interface SortOption<K extends string> {
  key: K;
  label: string;
}

export function toggleSort<K extends string>(
  curKey: K,
  curDir: SortDir,
  nextKey: K
): { key: K; dir: SortDir } {
  if (curKey === nextKey) {
    return { key: nextKey, dir: curDir === 'desc' ? 'asc' : 'desc' };
  }
  return { key: nextKey, dir: 'desc' };
}

export function SortTh<K extends string>({
  colKey,
  label,
  activeKey,
  dir,
  align = 'right',
  onSort,
  className = '',
}: {
  colKey: K;
  label: string;
  activeKey: K;
  dir: SortDir;
  align?: 'left' | 'center' | 'right';
  onSort: (k: K) => void;
  className?: string;
}) {
  const active = activeKey === colKey;
  const justify =
    align === 'left' ? 'justify-start' : align === 'center' ? 'justify-center' : 'justify-end';
  return (
    <th className={`py-3 px-3 font-medium select-none ${className}`}>
      <button
        type="button"
        onClick={() => onSort(colKey)}
        className={`w-full inline-flex items-center gap-1 ${justify} hover:text-slate-900 dark:hover:text-white transition-colors ${
          active ? 'text-blue-600 dark:text-blue-400 font-semibold' : ''
        }`}
      >
        <span>{label}</span>
        {active ? (
          dir === 'desc' ? (
            <ArrowDown className="w-3.5 h-3.5 shrink-0" />
          ) : (
            <ArrowUp className="w-3.5 h-3.5 shrink-0" />
          )
        ) : (
          <ArrowUpDown className="w-3 h-3 opacity-40 shrink-0" />
        )}
      </button>
    </th>
  );
}

export function MobileSortBar<K extends string>({
  options,
  activeKey,
  dir,
  onSort,
}: {
  options: SortOption<K>[];
  activeKey: K;
  dir: SortDir;
  onSort: (k: K) => void;
}) {
  return (
    <div className="md:hidden flex items-center gap-1.5 overflow-x-auto pb-0.5">
      <span className="text-[11px] text-slate-400 shrink-0">排序:</span>
      {options.map((opt) => {
        const active = activeKey === opt.key;
        return (
          <button
            key={opt.key}
            type="button"
            onClick={() => onSort(opt.key)}
            className={`inline-flex items-center gap-1 px-2.5 py-1 rounded-full text-[11px] font-medium whitespace-nowrap transition-colors ${
              active
                ? 'bg-blue-600 text-white'
                : 'bg-[var(--bg-card)] text-slate-600 dark:text-slate-300 border border-[var(--border-subtle)]'
            }`}
          >
            <span>{opt.label}</span>
            {active &&
              (dir === 'desc' ? (
                <ArrowDown className="w-3 h-3" />
              ) : (
                <ArrowUp className="w-3 h-3" />
              ))}
          </button>
        );
      })}
    </div>
  );
}
