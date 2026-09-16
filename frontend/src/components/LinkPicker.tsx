import { useEffect, useMemo, useRef, useState } from "react";
import type { Node } from "../lib/api";
import { Icons } from "./Icons";

function normalize(value: string) {
  return value.normalize("NFD").replace(/[\u0300-\u036f]/g, "").toLocaleLowerCase().replace(/\.md$/, "");
}

function similarity(query: string, candidate: string) {
  const needle = normalize(query).trim();
  const haystack = normalize(candidate);
  if (!needle) return 1;
  if (haystack === needle) return 100;
  if (haystack.startsWith(needle)) return 80 - haystack.length / 100;
  if (haystack.includes(needle)) return 60 - haystack.indexOf(needle) / 100;
  let cursor = 0;
  for (const character of haystack) if (character === needle[cursor]) cursor++;
  const subsequence = cursor === needle.length ? 25 : 0;
  const queryPairs = new Set(Array.from({ length: Math.max(0, needle.length - 1) }, (_, index) => needle.slice(index, index + 2)));
  if (!queryPairs.size) return subsequence;
  let common = 0;
  for (let index = 0; index < haystack.length - 1; index++) if (queryPairs.has(haystack.slice(index, index + 2))) common++;
  return subsequence + (2 * common) / (queryPairs.size + Math.max(1, haystack.length - 1)) * 20;
}

export function LinkPicker({ nodes, onSelect, onClose }: { nodes: Node[]; onSelect: (node: Node) => void; onClose: () => void }) {
  const [query, setQuery] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);
  useEffect(() => { inputRef.current?.focus(); }, []);
  useEffect(() => {
    const close = (event: KeyboardEvent) => { if (event.key === "Escape") onClose(); };
    window.addEventListener("keydown", close);
    return () => window.removeEventListener("keydown", close);
  }, [onClose]);
  const results = useMemo(() => nodes.map((node) => ({ node, score: Math.max(similarity(query, node.name), similarity(query, node.path)) })).filter(({ score }) => score > 4).sort((a, b) => b.score - a.score || a.node.path.localeCompare(b.node.path)).slice(0, 12), [nodes, query]);

  return <><button className="link-picker-scrim" type="button" aria-label="Fermer la recherche de lien" onClick={onClose} /><div className="link-picker" role="dialog" aria-label="Créer un lien interne">
    <div className="link-picker-search"><span>⌕</span><input ref={inputRef} value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Rechercher approximativement une page…" /></div>
    <div className="link-picker-results">{results.length ? results.map(({ node }) => <button type="button" key={node.path} onClick={() => onSelect(node)}><span className={node.type}>{node.type === "directory" ? <Icons.folder /> : <Icons.file />}</span><span><strong>{node.name.replace(/\.md$/, "")}</strong><small>{node.path}</small></span><b>↵</b></button>) : <div className="link-picker-empty">Aucune page ou dossier correspondant.</div>}</div>
    <div className="link-picker-help"><code>[[page]]</code><span>Recherche floue sur le nom et le chemin</span></div>
  </div></>;
}
