import { useEffect, useState } from "react";
import type { Rule } from "../api";
import { api } from "../api";
import { LoadError, useLoad } from "../components/common";

const kindLabels: Record<Rule["kind"], string> = {
  LEGACY: "Recovered from legacy",
  DERIVED: "Modern rule on legacy data",
  ASSUMPTION: "Assumption",
};

function shortFile(path: string): string {
  return path.split("/").pop() ?? path;
}

export function LegacyRules({ focus }: { focus: string | null }) {
  const { data, error, reload } = useLoad(() => api.rules(), "rules");
  const [kind, setKind] = useState<Rule["kind"] | "">("");

  useEffect(() => {
    if (focus && data) document.getElementById(focus)?.scrollIntoView({ block: "start" });
  }, [focus, data]);

  if (error && !data) return <LoadError message={error} retry={reload} />;
  if (!data) return <p className="loading">Loading rules</p>;
  const shown = data.filter((r) => !kind || r.kind === kind);
  const count = (k: Rule["kind"]) => data.filter((r) => r.kind === k).length;

  return (
    <div className="rules-page">
      <h1>Legacy rules</h1>
      <p className="panel-lede">
        Each workflow rule in ConsultFlow, where it came from in the VistA GMRC source, and where it lives now. Line
        references are checked against the legacy files by an automated test.
      </p>
      <div className="kind-filter" role="group" aria-label="Filter by origin">
        <button type="button" aria-pressed={kind === ""} onClick={() => setKind("")}>
          All {data.length}
        </button>
        {(Object.keys(kindLabels) as Rule["kind"][]).map((k) => (
          <button key={k} type="button" aria-pressed={kind === k} onClick={() => setKind(k)} data-kind={k}>
            {kindLabels[k]} {count(k)}
          </button>
        ))}
      </div>
      <ol className="rules">
        {shown.map((r) => (
          <li key={r.id} id={r.id} className={focus === r.id ? "focused" : undefined}>
            <header>
              <h2>{r.title}</h2>
              <span className="rule-id">{r.id}</span>
              <span className="kind" data-kind={r.kind}>
                {kindLabels[r.kind]}
              </span>
            </header>
            <p className="behavior">{r.behavior}</p>
            <div className="rule-columns">
              <div>
                <h3>Legacy source</h3>
                <ul className="refs">
                  {r.legacy.map((ref) => (
                    <li key={`${ref.file}:${ref.line}`}>
                      <span className="ref-loc" title={ref.file}>
                        {shortFile(ref.file)}, line {ref.line}
                      </span>
                      <pre>
                        <code>{ref.snippet}</code>
                      </pre>
                    </li>
                  ))}
                </ul>
              </div>
              <div>
                <h3>Modern implementation</h3>
                <ul className="modern">
                  {r.modern.map((m) => (
                    <li key={m}>
                      <code>{m}</code>
                    </li>
                  ))}
                </ul>
              </div>
            </div>
          </li>
        ))}
      </ol>
    </div>
  );
}
