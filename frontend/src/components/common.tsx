import { useCallback, useEffect, useEffectEvent, useState } from "react";
import type { Flag } from "../api";

/**
 * Load data whenever `key` changes; reload() refetches. Responses that arrive
 * after the key has changed are discarded.
 */
export function useLoad<T>(load: () => Promise<T>, key: string) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [version, setVersion] = useState(0);
  const fetchData = useEffectEvent(load);

  useEffect(() => {
    let current = true;
    fetchData().then(
      (d) => {
        if (!current) return;
        setData(d);
        setError(null);
      },
      (e: unknown) => {
        if (current) setError(e instanceof Error ? e.message : String(e));
      },
    );
    return () => {
      current = false;
    };
  }, [key, version]);

  const reload = useCallback(() => setVersion((v) => v + 1), []);
  return { data, error, reload, setData };
}

export function LoadError({ message, retry }: { message: string; retry: () => void }) {
  return (
    <div className="load-error" role="alert">
      <p>Could not reach the ConsultFlow API: {message}</p>
      <button type="button" onClick={retry}>
        Try again
      </button>
    </div>
  );
}

export function RuleLink({ id }: { id: string }) {
  return (
    <a className="rule-link" href={`#/rules?rule=${id}`} title="Show the legacy source for this rule">
      {id}
    </a>
  );
}

export function FlagList({ flags }: { flags: Flag[] }) {
  if (flags.length === 0) return null;
  return (
    <ul className="flags">
      {flags.map((f) => (
        <li key={f.code} data-severity={f.severity}>
          <strong>{f.title}</strong>
          <span>{f.detail}</span>
          <RuleLink id={f.rule} />
        </li>
      ))}
    </ul>
  );
}
