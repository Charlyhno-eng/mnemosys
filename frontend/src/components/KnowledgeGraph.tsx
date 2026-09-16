import { useEffect, useMemo, useRef, useState } from "react";
import type { Graph, GraphNode, PageTypeDefinition } from "../lib/api";
import { Icons } from "./Icons";

type SimNode = GraphNode & { x: number; y: number; vx: number; vy: number };
type View = { x: number; y: number; scale: number };

function nodeColor(node: GraphNode, pageTypes: PageTypeDefinition[]) {
  if (node.type === "directory") return { fill: "#e0aa5c", stroke: "#ffe0a6" };
  const fill = pageTypes.find((type) => type.id === node.pageType)?.color ?? pageTypes[0]?.color ?? "#62a6e8";
  return { fill, stroke: `${fill}dd` };
}

function normalize(value: string) {
  return value.normalize("NFD").replace(/[\u0300-\u036f]/g, "").toLocaleLowerCase().replace(/\.md$/, "");
}

function score(query: string, node: GraphNode) {
  const needle = normalize(query).trim();
  const name = normalize(node.name);
  const path = normalize(node.id);
  if (!needle) return 1;
  if (name === needle) return 100;
  if (name.startsWith(needle)) return 80;
  if (name.includes(needle)) return 65;
  if (path.includes(needle)) return 50;
  let cursor = 0;
  for (const character of name) if (character === needle[cursor]) cursor++;
  return cursor === needle.length ? 25 : 0;
}

function seededPosition(id: string, index: number, total: number) {
  let hash = 2166136261;
  for (const character of id) hash = Math.imul(hash ^ character.charCodeAt(0), 16777619);
  const angle = (index / Math.max(1, total)) * Math.PI * 2 + ((hash >>> 0) % 100) / 100;
  const radius = 90 + ((hash >>> 8) % 220);
  return { x: Math.cos(angle) * radius, y: Math.sin(angle) * radius };
}

export function KnowledgeGraph({ graph, pageTypes, onOpen }: { graph: Graph; pageTypes: PageTypeDefinition[]; onOpen: (node: GraphNode) => void }) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const viewRef = useRef<View>({ x: 0, y: 0, scale: 1 });
  const optionsRef = useRef({ query: "", folders: true, documents: true, hierarchy: true, links: true, labels: "auto", nodeSize: 1 });
  const openRef = useRef(onOpen);
  const [query, setQuery] = useState("");
  const [folders, setFolders] = useState(true);
  const [documents, setDocuments] = useState(true);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [hierarchy, setHierarchy] = useState(true);
  const [links, setLinks] = useState(true);
  const [labels, setLabels] = useState<"auto" | "always" | "hover">("auto");
  const [nodeSize, setNodeSize] = useState(1);
  openRef.current = onOpen;
  optionsRef.current = { query, folders, documents, hierarchy, links, labels, nodeSize };

  const searchResults = useMemo(() => query.trim() ? graph.nodes.map((node) => ({ node, score: score(query, node) })).filter((result) => result.score > 0).sort((a, b) => b.score - a.score || a.node.name.localeCompare(b.node.name)).slice(0, 8) : [], [graph.nodes, query]);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const context = canvas.getContext("2d");
    if (!context) return;
    const nodes: SimNode[] = graph.nodes.map((node, index) => ({ ...node, ...seededPosition(node.id, index, graph.nodes.length), vx: 0, vy: 0 }));
    const byID = new Map(nodes.map((node) => [node.id, node]));
    const edges = graph.edges.map((edge) => ({ ...edge, sourceNode: byID.get(edge.source), targetNode: byID.get(edge.target) })).filter((edge) => edge.sourceNode && edge.targetNode);
    let width = 1; let height = 1; let ratio = 1; let frame = 0; let tick = 0; let hovered: SimNode | null = null;
    let interaction: { kind: "pan" | "node"; node?: SimNode; startX: number; startY: number; originX: number; originY: number; moved: boolean } | null = null;

    const resize = () => {
      const bounds = canvas.getBoundingClientRect();
      width = Math.max(1, bounds.width); height = Math.max(1, bounds.height); ratio = window.devicePixelRatio || 1;
      canvas.width = Math.round(width * ratio); canvas.height = Math.round(height * ratio);
      if (viewRef.current.x === 0 && viewRef.current.y === 0) viewRef.current = { x: width / 2, y: height / 2, scale: Math.min(1, 700 / Math.max(700, nodes.length * 2)) };
    };
    const observer = new ResizeObserver(resize); observer.observe(canvas); resize();

    const visible = (node: SimNode) => node.type === "directory" ? optionsRef.current.folders : optionsRef.current.documents;
    const worldPoint = (event: PointerEvent | WheelEvent) => {
      const bounds = canvas.getBoundingClientRect(); const view = viewRef.current;
      return { x: (event.clientX - bounds.left - view.x) / view.scale, y: (event.clientY - bounds.top - view.y) / view.scale };
    };
    const hitTest = (event: PointerEvent) => {
      const point = worldPoint(event); const radius = 14 / viewRef.current.scale;
      let result: SimNode | null = null; let distance = Infinity;
      for (const node of nodes) {
        if (!visible(node)) continue;
        const current = Math.hypot(node.x - point.x, node.y - point.y);
        if (current < radius && current < distance) { result = node; distance = current; }
      }
      return result;
    };

    const simulate = () => {
      if (tick++ > 360) return;
      for (const node of nodes) { node.vx += -node.x * .0007; node.vy += -node.y * .0007; }
      for (const edge of edges) {
        const source = edge.sourceNode!; const target = edge.targetNode!;
        const dx = target.x - source.x; const dy = target.y - source.y; const distance = Math.max(1, Math.hypot(dx, dy));
        const desired = edge.type === "link" ? 105 : 75; const force = (distance - desired) * .0018;
        source.vx += dx / distance * force; source.vy += dy / distance * force; target.vx -= dx / distance * force; target.vy -= dy / distance * force;
      }
      const cells = new Map<string, SimNode[]>(); const cellSize = 42;
      for (const node of nodes) { const key = `${Math.floor(node.x / cellSize)}:${Math.floor(node.y / cellSize)}`; const cell = cells.get(key); if (cell) cell.push(node); else cells.set(key, [node]); }
      for (const node of nodes) {
        const cellX = Math.floor(node.x / cellSize); const cellY = Math.floor(node.y / cellSize);
        for (let x = cellX - 1; x <= cellX + 1; x++) for (let y = cellY - 1; y <= cellY + 1; y++) for (const other of cells.get(`${x}:${y}`) ?? []) {
          if (node === other) continue; const dx = node.x - other.x; const dy = node.y - other.y; const distanceSquared = dx * dx + dy * dy + .1;
          if (distanceSquared < 1600) { const force = .7 / distanceSquared; node.vx += dx * force; node.vy += dy * force; }
        }
        if (interaction?.kind !== "node" || interaction.node !== node) { node.vx *= .88; node.vy *= .88; node.x += node.vx; node.y += node.vy; }
      }
    };

    const draw = () => {
      simulate(); context.setTransform(ratio, 0, 0, ratio, 0, 0); context.clearRect(0, 0, width, height);
      const view = viewRef.current; context.save(); context.translate(view.x, view.y); context.scale(view.scale, view.scale);
      const activeQuery = optionsRef.current.query.trim();
      for (const edge of edges) {
        const source = edge.sourceNode!; const target = edge.targetNode!; if (!visible(source) || !visible(target)) continue;
        if ((edge.type === "hierarchy" && !optionsRef.current.hierarchy) || (edge.type === "link" && !optionsRef.current.links)) continue;
        context.beginPath(); context.moveTo(source.x, source.y); context.lineTo(target.x, target.y);
        context.strokeStyle = edge.type === "link" ? "rgba(151,125,224,.7)" : "rgba(79,105,130,.44)";
        context.lineWidth = (edge.type === "link" ? 1.35 : .8) / view.scale; context.setLineDash(edge.type === "link" ? [5 / view.scale, 4 / view.scale] : []); context.stroke();
      }
      context.setLineDash([]);
      for (const node of nodes) {
        if (!visible(node)) continue; const matches = !activeQuery || score(activeQuery, node) > 0; const radius = (node.type === "directory" ? 6.5 : 4.2) * optionsRef.current.nodeSize;
        const colors = nodeColor(node, pageTypes);
        context.globalAlpha = matches ? 1 : .14; context.beginPath(); context.arc(node.x, node.y, hovered === node ? radius * 1.55 : radius, 0, Math.PI * 2);
        context.fillStyle = colors.fill; context.fill();
        if (hovered === node || (activeQuery && matches)) { context.strokeStyle = colors.stroke; context.lineWidth = 2 / view.scale; context.stroke(); }
        const showLabel = optionsRef.current.labels === "always" || hovered === node || (activeQuery && matches) || (optionsRef.current.labels === "auto" && view.scale > .72);
        if (showLabel) {
          context.font = `${hovered === node ? 600 : 500} ${Math.max(9, 11 / Math.max(.8, view.scale))}px Inter, sans-serif`; context.fillStyle = "#c5d3e1"; context.textBaseline = "middle"; context.fillText(node.name, node.x + radius + 5 / view.scale, node.y);
        }
      }
      context.globalAlpha = 1; context.restore(); frame = requestAnimationFrame(draw);
    };

    canvas.onpointerdown = (event) => {
      canvas.setPointerCapture(event.pointerId); const node = hitTest(event);
      interaction = node ? { kind: "node", node, startX: event.clientX, startY: event.clientY, originX: node.x, originY: node.y, moved: false } : { kind: "pan", startX: event.clientX, startY: event.clientY, originX: viewRef.current.x, originY: viewRef.current.y, moved: false };
    };
    canvas.onpointermove = (event) => {
      hovered = hitTest(event); canvas.style.cursor = hovered ? "pointer" : interaction ? "grabbing" : "grab";
      if (!interaction) return; const dx = event.clientX - interaction.startX; const dy = event.clientY - interaction.startY; interaction.moved ||= Math.hypot(dx, dy) > 4;
      if (interaction.kind === "pan") { viewRef.current.x = interaction.originX + dx; viewRef.current.y = interaction.originY + dy; }
      else if (interaction.node) { interaction.node.x = interaction.originX + dx / viewRef.current.scale; interaction.node.y = interaction.originY + dy / viewRef.current.scale; interaction.node.vx = 0; interaction.node.vy = 0; }
    };
    canvas.onpointerup = (event) => { if (interaction?.kind === "node" && interaction.node && !interaction.moved) openRef.current(interaction.node); interaction = null; canvas.releasePointerCapture(event.pointerId); };
    canvas.onpointerleave = () => { hovered = null; };
    canvas.onwheel = (event) => {
      event.preventDefault(); const before = worldPoint(event); const view = viewRef.current; const nextScale = Math.min(3, Math.max(.2, view.scale * Math.exp(-event.deltaY * .001)));
      const bounds = canvas.getBoundingClientRect(); view.x = event.clientX - bounds.left - before.x * nextScale; view.y = event.clientY - bounds.top - before.y * nextScale; view.scale = nextScale;
    };
    frame = requestAnimationFrame(draw);
    return () => { cancelAnimationFrame(frame); observer.disconnect(); canvas.onpointerdown = null; canvas.onpointermove = null; canvas.onpointerup = null; canvas.onpointerleave = null; canvas.onwheel = null; };
  }, [graph, pageTypes]);

  const zoom = (factor: number) => { viewRef.current.scale = Math.min(3, Math.max(.2, viewRef.current.scale * factor)); };
  const recenter = () => { const canvas = canvasRef.current; if (canvas) viewRef.current = { x: canvas.clientWidth / 2, y: canvas.clientHeight / 2, scale: 1 }; };
  const tx = (english: string, _french: string) => english;
  if (!graph.nodes.length) return <div className="graph-empty"><strong>{tx("The graph is empty", "Le graphe est vide")}</strong><span>{tx("Create a folder or page to display its relationships.", "Créez un dossier ou une page pour faire apparaître ses relations.")}</span></div>;

  return <div className="graph-page">
    <div className="graph-heading"><div><span className="eyebrow">{tx("CONNECTIONS", "CONNEXIONS")}</span><h1>{tx("Knowledge graph", "Graphe des connaissances")}</h1><p>{tx("Move nodes, zoom and search throughout the vault.", "Déplacez les nœuds, zoomez et recherchez une page dans l’ensemble du vault.")}</p></div><div className="graph-legend"><span><i className="folder" />{tx("Folder", "Dossier")}</span>{pageTypes.map((type) => <span key={type.id}><i style={{ background: type.color }} />{type.label}</span>)}<span><i className="link" />{tx("Link", "Lien")}</span></div></div>
    <div className="graph-canvas">
      <canvas ref={canvasRef} aria-label="Interactive graph of folders and documents" />
      <div className="graph-tools"><div className="graph-search"><span>⌕</span><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={tx("Search for a node…", "Rechercher un nœud…")} />{query && <button onClick={() => setQuery("")} aria-label={tx("Clear", "Effacer")}>×</button>}</div><div className="graph-filters"><button className={folders ? "active" : ""} onClick={() => setFolders((value) => !value)}><i className="folder" />{tx("Folders", "Dossiers")}</button><button className={documents ? "active" : ""} onClick={() => setDocuments((value) => !value)}><i className="document" />{tx("Pages", "Pages")}</button><button className={settingsOpen ? "active graph-settings-button" : "graph-settings-button"} onClick={() => setSettingsOpen((open) => !open)} title={tx("Graph settings", "Paramètres du graphe")}><Icons.settings /></button></div></div>
      {settingsOpen && <div className="graph-settings-panel"><strong>{tx("Graph settings", "Paramètres du graphe")}</strong><label><span>{tx("Labels", "Étiquettes")}</span><select value={labels} onChange={(event) => setLabels(event.target.value as typeof labels)}><option value="auto">{tx("Automatic", "Automatiques")}</option><option value="always">{tx("Always", "Toujours")}</option><option value="hover">{tx("On hover", "Au survol")}</option></select></label><label><span>{tx("Node size", "Taille des nœuds")}</span><input type="range" min="0.7" max="1.8" step="0.1" value={nodeSize} onChange={(event) => setNodeSize(Number(event.target.value))} /></label><label className="graph-check"><input type="checkbox" checked={hierarchy} onChange={(event) => setHierarchy(event.target.checked)} />{tx("Folder hierarchy", "Hiérarchie des dossiers")}</label><label className="graph-check"><input type="checkbox" checked={links} onChange={(event) => setLinks(event.target.checked)} />{tx("Explicit links", "Liens explicites")}</label></div>}
      {query && <div className="graph-search-results">{searchResults.length ? searchResults.map(({ node }) => <button key={node.id} onClick={() => onOpen(node)}><i style={{ background: node.type === "directory" ? "#e0aa5c" : nodeColor(node, pageTypes).fill }} /><span><strong>{node.name}</strong><small>{node.id}</small></span></button>) : <span>{tx("No results.", "Aucun résultat.")}</span>}</div>}
      <div className="graph-zoom"><button onClick={() => zoom(1.2)} aria-label={tx("Zoom in", "Zoomer")}>+</button><button onClick={() => zoom(1 / 1.2)} aria-label={tx("Zoom out", "Dézoomer")}>−</button><button onClick={recenter} aria-label={tx("Recenter graph", "Recentrer le graphe")} title={tx("Recenter graph", "Recentrer le graphe")}>⌂</button></div>
      <div className="graph-count">{graph.nodes.length} {tx("nodes", "nœuds")} · {graph.edges.length} {tx("connections", "connexions")}</div>
    </div>
  </div>;
}
