import { useEffect, useState } from 'react';
import { ArrowUp, ChevronDown } from 'lucide-react';
import { t, useLanguage, formatNumber } from './i18n';

const step = 10;

// Long lists show ten at a time. Changing what the list is about (another
// folder, a new search) starts again from the first ten.
export function usePaged<T>(items: T[], reset: unknown = null) {
  const [count, setCount] = useState(step);
  useEffect(() => setCount(step), [reset]);
  return {
    shown: items.slice(0, count),
    more: () => setCount((c) => c + step),
    all: () => setCount(items.length),
    remaining: Math.max(0, items.length - count),
    expanded: count > step && items.length > step,
    total: items.length,
  };
}

export function ShowMore({ paged }: { paged: ReturnType<typeof usePaged<unknown>> }) {
  useLanguage();
  if (!paged.remaining && !paged.expanded) return null;
  return (
    <div className="show-more">
      {paged.remaining > 0 && (
        <>
          <button type="button" className="secondary" onClick={paged.more}>
            <ChevronDown size={16} />
            {t('list.showMore', { count: formatNumber(Math.min(step, paged.remaining)) })}
          </button>
          <button type="button" className="quiet" onClick={paged.all}>
            {t('list.showAll', { count: formatNumber(paged.total) })}
          </button>
        </>
      )}
      {paged.expanded && (
        <button
          type="button"
          className="quiet"
          onClick={() =>
            window.scrollTo({
              top: 0,
              behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth',
            })
          }
        >
          <ArrowUp size={16} />
          {t('app.backToTop')}
        </button>
      )}
    </div>
  );
}
