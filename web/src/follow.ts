import { useEffect, useRef } from 'react';

// Runs check while active, one call at a time: after 4 seconds, then less
// often up to once a minute, and no longer after half an hour, so a page left
// open does not keep asking.
export function useFollow(active: boolean, check: () => Promise<unknown>) {
  const latest = useRef(check);
  latest.current = check;
  useEffect(() => {
    if (!active) return;
    const started = Date.now();
    let wait = 4000;
    let timer = 0;
    let stopped = false;
    const next = () => {
      if (stopped || Date.now() - started > 30 * 60_000) return;
      timer = window.setTimeout(async () => {
        try {
          await latest.current();
        } catch {
          // Tried again at the next turn.
        }
        wait = Math.min(wait * 1.5, 60_000);
        next();
      }, wait);
    };
    next();
    return () => {
      stopped = true;
      window.clearTimeout(timer);
    };
  }, [active]);
}
