import { useCallback, useEffect, useMemo, useRef, useState, type DragEvent } from "react";
import { useSearchParams } from "react-router-dom";
import { Dialog } from "./components/Dialog";
import { DocumentTree, type TreeAction } from "./components/DocumentTree";
import { Icons } from "./components/Icons";
import { KnowledgeGraph } from "./components/KnowledgeGraph";
import { LinkPicker } from "./components/LinkPicker";
import { MarkdownPreview } from "./components/MarkdownPreview";
import { api, type DirectoryListing, type Graph, type GraphNode, type Node, type StorageSettings } from "./lib/api";

type Modal =
  | { kind: "create"; type: Node["type"]; parent: string }
  | { kind: "rename" | "move" | "delete"; node: Node }
  | null;

type Notice = { message: string; tone: "success" | "error" };
type EditHistory = { past: string[]; present: string; future: string[]; lastTypingAt: number };

const parentPath = (path: string) => path.includes("/") ? path.slice(0, path.lastIndexOf("/")) : "";
const baseName = (path: string) => path.slice(path.lastIndexOf("/") + 1);
const joinPath = (parent: string, name: string) => parent ? `${parent}/${name}` : name;

function folders(nodes: Node[]): Node[] {
  return nodes.flatMap((node) => node.type === "directory" ? [node, ...folders(node.children ?? [])] : []);
}

function documents(nodes: Node[]): Node[] {
  return nodes.flatMap((node) => node.type === "document" ? [node] : documents(node.children ?? []));
}

function entries(nodes: Node[]): Node[] {
  return nodes.flatMap((node) => [node, ...entries(node.children ?? [])]);
}

function resolveWikiNode(nodes: Node[], rawTarget: string): Node | null {
  const target = rawTarget.split("#", 1)[0].trim();
  const all = entries(nodes);
  const exact = all.find((node) => node.path === target || (node.type === "document" && node.path === `${target}.md`));
  if (exact) return exact;
  const normalized = target.toLocaleLowerCase();
  const matches = all.filter((node) => node.name.toLocaleLowerCase() === normalized || node.name.replace(/\.md$/, "").toLocaleLowerCase() === normalized);
  return matches.length === 1 ? matches[0] : null;
}

function documentProperty(content: string, key: "name" | "description", fallback = "") {
  if (!content.startsWith("---\n")) return fallback;
  const closing = content.indexOf("\n---", 4);
  if (closing < 0) return fallback;
  const match = content.slice(4, closing).match(new RegExp(`^${key}\\s*:\\s*(.*)$`, "m"));
  if (!match) return fallback;
  try { return JSON.parse(match[1]); } catch { return match[1].replace(/^['"]|['"]$/g, ""); }
}

function findNode(nodes: Node[], path: string | null): Node | null {
  if (!path) return null;
  for (const node of nodes) {
    if (node.path === path) return node;
    const child = findNode(node.children ?? [], path);
    if (child) return child;
  }
  return null;
}

function filterTree(nodes: Node[], query: string): Node[] {
  const normalized = query.trim().toLocaleLowerCase();
  if (!normalized) return nodes;
  return nodes.flatMap((node) => {
    if (node.type === "document") return node.name.toLocaleLowerCase().includes(normalized) ? [node] : [];
    const children = filterTree(node.children ?? [], query);
    return node.name.toLocaleLowerCase().includes(normalized) || children.length ? [{ ...node, children }] : [];
  });
}

export function App() {
  const [searchParams, setSearchParams] = useSearchParams();
  const selectedPath = searchParams.get("doc");
  const spacePath = searchParams.get("space");
  const graphOpen = searchParams.get("view") === "graph";
  const [tree, setTree] = useState<Node[]>([]);
  const [graph, setGraph] = useState<Graph>({ nodes: [], edges: [] });
  const [content, setContent] = useState("");
  const [savedContent, setSavedContent] = useState("");
  const [loadedPath, setLoadedPath] = useState<string | null>(null);
  const [mode, setMode] = useState<"edit" | "preview">("preview");
  const [loading, setLoading] = useState(true);
  const [saveStatus, setSaveStatus] = useState<"idle" | "saving" | "saved" | "error">("idle");
  const [notice, setNotice] = useState<Notice | null>(null);
  const [modal, setModal] = useState<Modal>(null);
  const [modalValue, setModalValue] = useState("");
  const [modalFolder, setModalFolder] = useState("");
  const [busy, setBusy] = useState(false);
  const [query, setQuery] = useState("");
  const [rootDrop, setRootDrop] = useState(false);
  const [homeQuery, setHomeQuery] = useState("");
  const [uploading, setUploading] = useState(false);
  const [imageDrag, setImageDrag] = useState(false);
  const [storage, setStorage] = useState<StorageSettings | null>(null);
  const [storageOpen, setStorageOpen] = useState(false);
  const [storagePath, setStoragePath] = useState("");
  const [storageBusy, setStorageBusy] = useState(false);
  const [directoryListing, setDirectoryListing] = useState<DirectoryListing | null>(null);
  const [directoryBusy, setDirectoryBusy] = useState(false);
  const [linkPickerOpen, setLinkPickerOpen] = useState(false);
  const [historyVersion, setHistoryVersion] = useState(0);
  const editorRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const historyRef = useRef<EditHistory>({ past: [], present: "", future: [], lastTypingAt: 0 });

  const allFolders = useMemo(() => folders(tree), [tree]);
  const allEntries = useMemo(() => entries(tree), [tree]);
  const visibleTree = useMemo(() => filterTree(tree, query), [tree, query]);
  const spaceNode = useMemo(() => findNode(tree, spacePath), [tree, spacePath]);
  const homeNodes = spaceNode?.type === "directory" ? spaceNode.children ?? [] : tree;
  const homeDocuments = useMemo(() => documents(spaceNode?.type === "directory" ? [spaceNode] : tree), [spaceNode, tree]);
  const homeResults = useMemo(() => {
    const normalized = homeQuery.trim().toLocaleLowerCase();
    return normalized ? homeDocuments.filter((node) => node.path.toLocaleLowerCase().includes(normalized)) : [];
  }, [homeDocuments, homeQuery]);
  const dirty = loadedPath === selectedPath && content !== savedContent;
  const properties = useMemo(() => ({ name: documentProperty(content, "name", selectedPath ? baseName(selectedPath).replace(/\.md$/, "") : ""), description: documentProperty(content, "description") }), [content, selectedPath]);
  const backlinks = useMemo(() => selectedPath ? graph.edges.filter((edge) => edge.type === "link" && edge.target === selectedPath).map((edge) => allEntries.find((node) => node.path === edge.source)).filter((node): node is Node => Boolean(node)) : [], [allEntries, graph.edges, selectedPath]);

  const flash = useCallback((message: string, tone: Notice["tone"] = "success") => setNotice({ message, tone }), []);
  const refreshTree = useCallback(async () => {
    const [nextTree, nextGraph] = await Promise.all([api.tree(), api.graph()]);
    setTree(nextTree);
    setGraph(nextGraph);
  }, []);

  useEffect(() => { refreshTree().catch((error) => flash(error.message, "error")).finally(() => setLoading(false)); }, [refreshTree, flash]);
  useEffect(() => {
    api.storage().then((settings) => {
      setStorage(settings);
      setStoragePath(settings.path);
    }).catch((error) => flash(error instanceof Error ? error.message : "Configuration du stockage indisponible.", "error"));
  }, [flash]);
  useEffect(() => {
    if (!notice) return;
    const timer = window.setTimeout(() => setNotice(null), 3500);
    return () => window.clearTimeout(timer);
  }, [notice]);

  useEffect(() => {
    let active = true;
    setLinkPickerOpen(false);
    if (!selectedPath) { setLoadedPath(null); resetHistory(""); setSavedContent(""); return; }
    setLoadedPath(null);
    api.get(selectedPath).then((document) => {
      if (!active) return;
      resetHistory(document.content);
      setSavedContent(document.content);
      setLoadedPath(document.path);
      setSaveStatus("idle");
      setMode("preview");
    }).catch((error) => { if (active) flash(error.message, "error"); });
    return () => { active = false; };
  }, [selectedPath, flash]);

  const save = useCallback(async (path = selectedPath, value = content) => {
    if (!path || loadedPath !== path) return false;
    setSaveStatus("saving");
    try {
      await api.update(path, value);
      setSavedContent(value);
      setSaveStatus("saved");
      void api.graph().then(setGraph).catch(() => undefined);
      return true;
    } catch (error) {
      setSaveStatus("error");
      flash(error instanceof Error ? error.message : "Impossible d’enregistrer.", "error");
      return false;
    }
  }, [content, flash, loadedPath, selectedPath]);

  useEffect(() => {
    if (!dirty || !selectedPath) return;
    const timer = window.setTimeout(() => void save(selectedPath, content), 800);
    return () => window.clearTimeout(timer);
  }, [content, dirty, save, selectedPath]);

  useEffect(() => {
    const shortcut = (event: KeyboardEvent) => {
      const editing = Boolean(selectedPath && mode === "edit" && !modal && !storageOpen);
      if (editing && (event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "z") { event.preventDefault(); event.shiftKey ? redo() : undo(); return; }
      if (editing && (event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "y") { event.preventDefault(); redo(); return; }
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "s") { event.preventDefault(); void save(); }
      if (!modal && !storageOpen && (event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "n") { event.preventDefault(); openCreate("document"); }
    };
    window.addEventListener("keydown", shortcut);
    return () => window.removeEventListener("keydown", shortcut);
  });

  async function select(path: string) {
    if (path === selectedPath) return;
    if (dirty && selectedPath && !(await save(selectedPath, content))) return;
    setSearchParams({ doc: path });
  }

  function openCreate(type: Node["type"], parent = selectedPath ? parentPath(selectedPath) : spacePath ?? "") {
    setModalValue(""); setModalFolder(parent); setModal({ kind: "create", type, parent });
  }

  function openAction(node: Node, action: TreeAction) {
    if (action === "new-document" || action === "new-directory") { openCreate(action === "new-document" ? "document" : "directory", node.path); return; }
    setModalValue(action === "rename" ? baseName(node.path).replace(/\.md$/, "") : "");
    setModalFolder(parentPath(node.path));
    setModal({ kind: action, node });
  }

  async function moveNode(node: Node, folder: string): Promise<boolean> {
    const destination = joinPath(folder, baseName(node.path));
    if (destination === node.path) return true;
    if (node.type === "directory" && (folder === node.path || folder.startsWith(`${node.path}/`))) { flash("Un dossier ne peut pas être déplacé dans lui-même.", "error"); return false; }
    try {
      if (dirty && selectedPath && (selectedPath === node.path || selectedPath.startsWith(`${node.path}/`)) && !(await save(selectedPath, content))) return false;
      await api.move(node.path, destination);
      await refreshTree();
      if (selectedPath && (selectedPath === node.path || selectedPath.startsWith(`${node.path}/`))) setSearchParams({ doc: destination + selectedPath.slice(node.path.length) });
      flash(`« ${node.name.replace(/\.md$/, "")} » déplacé.`);
      return true;
    } catch (error) { flash(error instanceof Error ? error.message : "Déplacement impossible.", "error"); return false; }
  }

  async function submitModal() {
    if (!modal) return;
    setBusy(true);
    try {
      if (modal.kind === "create") {
        let name = modalValue.trim();
        if (!name) return;
        if (name.includes("/") || name.includes("\\")) { flash("Le nom ne doit pas contenir de séparateur de dossier.", "error"); return; }
        if (dirty && selectedPath && !(await save(selectedPath, content))) return;
        if (modal.type === "document" && !name.endsWith(".md")) name += ".md";
        const path = joinPath(modalFolder, name);
        await api.create(path, modal.type, modal.type === "document" ? `# ${name.replace(/\.md$/, "")}\n` : "");
        await refreshTree();
        if (modal.type === "document") setSearchParams({ doc: path });
        flash(modal.type === "document" ? "Document créé." : "Dossier créé.");
      } else if (modal.kind === "rename") {
        let name = modalValue.trim();
        if (!name) return;
        if (name.includes("/") || name.includes("\\")) { flash("Le nom ne doit pas contenir de séparateur de dossier.", "error"); return; }
        if (modal.node.type === "document" && !name.endsWith(".md")) name += ".md";
        const destination = joinPath(parentPath(modal.node.path), name);
        if (destination !== modal.node.path) {
          if (dirty && selectedPath && (selectedPath === modal.node.path || selectedPath.startsWith(`${modal.node.path}/`)) && !(await save(selectedPath, content))) return;
          await api.move(modal.node.path, destination);
          await refreshTree();
          if (selectedPath && (selectedPath === modal.node.path || selectedPath.startsWith(`${modal.node.path}/`))) setSearchParams({ doc: destination + selectedPath.slice(modal.node.path.length) });
          flash("Élément renommé.");
        }
      } else if (modal.kind === "move") {
        if (!(await moveNode(modal.node, modalFolder))) return;
      } else {
        await api.remove(modal.node.path);
        await refreshTree();
        if (selectedPath && (selectedPath === modal.node.path || selectedPath.startsWith(`${modal.node.path}/`))) setSearchParams({});
        flash(modal.node.type === "directory" ? "Dossier supprimé." : "Document supprimé.");
      }
      setModal(null);
    } catch (error) { flash(error instanceof Error ? error.message : "Action impossible.", "error"); }
    finally { setBusy(false); }
  }

  function dropAtRoot(event: DragEvent) {
    event.preventDefault(); setRootDrop(false);
    const path = event.dataTransfer.getData("application/x-mnemosys-path");
    const type = event.dataTransfer.getData("application/x-mnemosys-type") as Node["type"];
    if (path) void moveNode({ path, type, name: baseName(path) }, "");
  }

  function resetHistory(value: string) {
    historyRef.current = { past: [], present: value, future: [], lastTypingAt: 0 };
    setContent(value);
    setHistoryVersion((version) => version + 1);
  }

  function changeContent(next: string, typing = false) {
    const history = historyRef.current;
    if (next === history.present) return;
    const now = Date.now();
    if (!typing || now - history.lastTypingAt > 700) history.past.push(history.present);
    if (history.past.length > 100) history.past.shift();
    history.present = next;
    history.future = [];
    history.lastTypingAt = typing ? now : 0;
    setContent(next);
    setHistoryVersion((version) => version + 1);
  }

  function undo() {
    const history = historyRef.current;
    const previous = history.past.pop();
    if (previous === undefined) return;
    history.future.push(history.present);
    history.present = previous;
    history.lastTypingAt = 0;
    setContent(previous);
    setHistoryVersion((version) => version + 1);
  }

  function redo() {
    const history = historyRef.current;
    const next = history.future.pop();
    if (next === undefined) return;
    history.past.push(history.present);
    history.present = next;
    history.lastTypingAt = 0;
    setContent(next);
    setHistoryVersion((version) => version + 1);
  }

  function wrapSelection(open: string, close: string, placeholder: string) {
    const editor = editorRef.current;
    if (!editor) return;
    const start = editor.selectionStart;
    const end = editor.selectionEnd;
    const selected = content.slice(start, end) || placeholder;
    const next = content.slice(0, start) + open + selected + close + content.slice(end);
    changeContent(next);
    window.requestAnimationFrame(() => {
      editor.focus();
      editor.setSelectionRange(start + open.length, start + open.length + selected.length);
    });
  }

  function prefixSelection(prefix: string | ((index: number) => string), placeholder = "Élément") {
    const editor = editorRef.current;
    if (!editor) return;
    const selectionStart = editor.selectionStart;
    const selectionEnd = editor.selectionEnd;
    const start = content.lastIndexOf("\n", Math.max(0, selectionStart - 1)) + 1;
    const nextBreak = content.indexOf("\n", selectionEnd);
    const end = nextBreak === -1 ? content.length : nextBreak;
    const selected = content.slice(start, end) || placeholder;
    const formatted = selected.split("\n").map((line, index) => `${typeof prefix === "function" ? prefix(index) : prefix}${line}`).join("\n");
    changeContent(content.slice(0, start) + formatted + content.slice(end));
    window.requestAnimationFrame(() => { editor.focus(); editor.setSelectionRange(start, start + formatted.length); });
  }

  function insertSnippet(snippet: string) {
    const editor = editorRef.current;
    if (!editor) return;
    const start = editor.selectionStart;
    const end = editor.selectionEnd;
    changeContent(content.slice(0, start) + snippet + content.slice(end));
    window.requestAnimationFrame(() => { editor.focus(); editor.setSelectionRange(start, start + snippet.length); });
  }

  function setDocumentProperty(key: "name" | "description", value: string) {
    let next = historyRef.current.present;
    if (!next.startsWith("---\n") || next.indexOf("\n---", 4) < 0) {
      const name = selectedPath ? baseName(selectedPath).replace(/\.md$/, "") : "Untitled";
      next = `---\nname: ${JSON.stringify(name)}\ndescription: ""\n---\n\n${next}`;
    }
    const expression = new RegExp(`^${key}\\s*:.*$`, "m");
    if (expression.test(next)) next = next.replace(expression, `${key}: ${JSON.stringify(value)}`);
    else next = next.replace("\n---", `\n${key}: ${JSON.stringify(value)}\n---`);
    changeContent(next, true);
  }

  function openWikiLink(target: string) {
    const node = resolveWikiNode(tree, target);
    if (!node) { flash(`Lien introuvable : ${target}`, "error"); return; }
    if (node.type === "directory") setSearchParams({ space: node.path });
    else void select(node.path);
  }

  function openGraphNode(node: GraphNode) {
    if (node.type === "directory") setSearchParams({ space: node.id });
    else void select(node.id);
  }

  function insertWikiLink(node: Node) {
    const current = historyRef.current.present;
    const token = `[[${node.path}]]`;
    const editor = editorRef.current;
    if (mode === "edit" && editor) {
      const start = editor.selectionStart;
      const end = editor.selectionEnd;
      changeContent(current.slice(0, start) + token + current.slice(end));
      window.requestAnimationFrame(() => { editor.focus(); editor.setSelectionRange(start + token.length, start + token.length); });
    } else {
      const separator = current.endsWith("\n\n") ? "" : current.endsWith("\n") ? "\n" : "\n\n";
      changeContent(`${current}${separator}${token}\n`);
      setMode("edit");
      window.requestAnimationFrame(() => window.requestAnimationFrame(() => editorRef.current?.focus()));
    }
    setLinkPickerOpen(false);
  }

  async function uploadImages(files: FileList | File[]) {
    const images = Array.from(files).filter((file) => ["image/png", "image/jpeg", "image/gif", "image/webp"].includes(file.type));
    if (!images.length) { flash("Déposez une image PNG, JPEG, WebP ou GIF.", "error"); return; }
    setUploading(true);
    try {
      const uploaded = await Promise.all(images.map(async (file) => ({ file, result: await api.upload(file) })));
      const markdown = uploaded.map(({ file, result }) => `![${file.name.replace(/[\]\n\r]/g, "")}](${result.url})`).join("\n\n");
      const current = historyRef.current.present;
      const next = (() => {
        const start = editorRef.current?.selectionStart ?? current.length;
        const end = editorRef.current?.selectionEnd ?? current.length;
        return current.slice(0, start) + markdown + current.slice(end);
      })();
      changeContent(next);
      setMode("edit");
      flash(images.length > 1 ? `${images.length} images ajoutées.` : "Image ajoutée.");
      window.requestAnimationFrame(() => editorRef.current?.focus());
    } catch (error) { flash(error instanceof Error ? error.message : "Import de l’image impossible.", "error"); }
    finally { setUploading(false); setImageDrag(false); }
  }

  function dropImages(event: DragEvent<HTMLDivElement>) {
    if (!event.dataTransfer.files.length) return;
    event.preventDefault();
    void uploadImages(event.dataTransfer.files);
  }

  async function configureStorage() {
    if (!storagePath.trim()) return;
    setStorageBusy(true);
    try {
      if (dirty && selectedPath && !(await save(selectedPath, content))) return;
      const settings = await api.configureStorage(storagePath.trim());
      setStorage(settings);
      setStoragePath(settings.path);
      setStorageOpen(false);
      setSearchParams({});
      await refreshTree();
      flash("Dossier de stockage configuré.");
    } catch (error) { flash(error instanceof Error ? error.message : "Ce dossier ne peut pas être utilisé.", "error"); }
    finally { setStorageBusy(false); }
  }

  async function browseStorage(path?: string) {
    setDirectoryBusy(true);
    try {
      const listing = await api.directories(path);
      setDirectoryListing(listing);
      setStoragePath(listing.path);
    } catch (error) { flash(error instanceof Error ? error.message : "Impossible d’ouvrir ce dossier.", "error"); }
    finally { setDirectoryBusy(false); }
  }

  function openStoragePicker() {
    setStorageOpen(true);
    void browseStorage(storage?.configured ? storage.path : undefined);
  }

  const crumbs = graphOpen ? ["Graphe"] : selectedPath?.split("/") ?? (spacePath ? spacePath.split("/") : []);
  const statusLabel = saveStatus === "saving" ? "Enregistrement…" : saveStatus === "error" ? "Erreur d’enregistrement" : dirty ? "Modifications en attente" : "Enregistré";

  return <main className="app-shell">
    <aside className="sidebar">
      <button className="brand" onClick={() => setSearchParams({})}><span className="brand-mark"><img src="/mnemosys-logo.png" alt="" /></span><span><strong>Mnemosys</strong><span>Mémoire d’équipe</span></span></button>
      <div className="quick-actions">
        <button className="button primary grow" onClick={() => openCreate("document")}><Icons.plus />Nouvelle page</button>
        <button className="icon-button framed" onClick={() => openCreate("directory")} title="Nouveau dossier" aria-label="Nouveau dossier"><Icons.folder /></button>
      </div>
      <div className="search-box"><span>⌕</span><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Filtrer les pages…" aria-label="Filtrer les pages" />{query && <button onClick={() => setQuery("")} aria-label="Effacer"><Icons.x /></button>}</div>
      <button className={`home-link ${!selectedPath && !spacePath && !graphOpen ? "active" : ""}`} onClick={() => setSearchParams({})}><Icons.book />Vue d’ensemble</button>
      <button className={`home-link ${graphOpen ? "active" : ""}`} onClick={() => setSearchParams({ view: "graph" })}><Icons.graph />Graphe</button>
      <div className="sidebar-label"><span>ESPACE</span><span>{tree.length}</span></div>
      <nav className={rootDrop ? "root-drop" : ""} aria-label="Arborescence documentaire" onDragOver={(event) => { if (event.target === event.currentTarget) { event.preventDefault(); setRootDrop(true); } }} onDragLeave={() => setRootDrop(false)} onDrop={dropAtRoot}>
        {loading ? <div className="tree-loading"><span /><span /><span /></div> : <DocumentTree nodes={visibleTree} selectedPath={selectedPath} onSelect={(path) => void select(path)} onAction={openAction} onMove={(node, folder) => void moveNode(node, folder)} />}
      </nav>
    </aside>

    <section className="workspace">
      <header className="topbar">
        <div className="breadcrumbs"><button onClick={() => setSearchParams({})}>Accueil</button>{crumbs.map((part, index) => <span key={`${part}-${index}`}><b>/</b><span>{part.replace(/\.md$/, "")}</span></span>)}</div>
        <div className="topbar-actions">
          {storage && !storage.configured && <button className="settings-reminder" onClick={openStoragePicker}><span>!</span>Pensez à choisir votre dossier d’enregistrement</button>}
          <button className={`icon-button settings-button ${storage && !storage.configured ? "needs-attention" : ""}`} onClick={openStoragePicker} title="Paramètres" aria-label="Ouvrir les paramètres"><Icons.settings /></button>
        </div>
      </header>

      {graphOpen ? <KnowledgeGraph graph={graph} onOpen={openGraphNode} /> : selectedPath ? <>
        <div className="editor-toolbar">
          <div className="toolbar-left">
            <div className="mode-switch"><button className={mode === "preview" ? "active" : ""} onClick={() => setMode("preview")}><Icons.eye />Aperçu</button><button className={mode === "edit" ? "active" : ""} onClick={() => setMode("edit")}><Icons.edit />Modifier</button></div>
            <div className="link-control"><button className={`link-button ${linkPickerOpen ? "active" : ""}`} onClick={() => setLinkPickerOpen((open) => !open)}><Icons.link />Relier</button>{linkPickerOpen && <LinkPicker nodes={allEntries.filter((node) => node.path !== selectedPath)} onSelect={insertWikiLink} onClose={() => setLinkPickerOpen(false)} />}</div>
            {mode === "edit" && <div className="format-tools" aria-label="Mise en forme Markdown">
              <button onClick={undo} disabled={historyRef.current.past.length === 0} title="Annuler (Ctrl+Z)">↶</button>
              <button onClick={redo} disabled={historyRef.current.future.length === 0} title="Rétablir (Ctrl+Maj+Z)">↷</button>
              <span className="tool-separator" data-history-version={historyVersion} />
              <select defaultValue="" aria-label="Titre" title="Titre" onChange={(event) => { if (event.target.value) prefixSelection(`${event.target.value} `, "Titre"); event.target.value = ""; }}><option value="" disabled>Texte</option><option value="#">Titre 1</option><option value="##">Titre 2</option><option value="###">Titre 3</option></select>
              <button onClick={() => wrapSelection("**", "**", "texte en gras")} title="Gras Markdown"><strong>B</strong></button>
              <button onClick={() => wrapSelection("*", "*", "texte en italique")} title="Italique Markdown"><em>I</em></button>
              <button onClick={() => wrapSelection("++", "++", "texte souligné")} title="Souligné Markdown"><u>U</u></button>
              <button onClick={() => wrapSelection("~~", "~~", "texte barré")} title="Barré"><s>S</s></button>
              <button onClick={() => wrapSelection("`", "`", "code")} title="Code en ligne">&lt;/&gt;</button>
              <label className="color-tool" title="Couleur du texte"><input type="color" defaultValue="#6ea8f5" onChange={(event) => wrapSelection(`<span style="color: ${event.target.value}">`, "</span>", "texte coloré")} /><span>A</span></label>
              <span className="tool-separator" />
              <button onClick={() => prefixSelection("- ")} title="Liste à puces">•≡</button>
              <button onClick={() => prefixSelection((index) => `${index + 1}. `)} title="Liste numérotée">1.</button>
              <button onClick={() => prefixSelection("- [ ] ", "Tâche")} title="Liste de tâches">☑</button>
              <button onClick={() => prefixSelection("> ", "Citation")} title="Citation">❯</button>
              <button onClick={() => wrapSelection("[", "](https://)", "texte du lien")} title="Lien">↗</button>
              <button onClick={() => wrapSelection("\n```\n", "\n```\n", "code")} title="Bloc de code">{`{ }`}</button>
              <button onClick={() => insertSnippet("\n| Colonne 1 | Colonne 2 | Colonne 3 |\n| --- | --- | --- |\n| Valeur | Valeur | Valeur |\n")} title="Tableau">▦</button>
              <select defaultValue="" aria-label="Annotation" title="Annotation" onChange={(event) => { const label = event.target.value; if (label) insertSnippet(`\n> **${label}**\n> Votre annotation ici.\n`); event.target.value = ""; }}><option value="" disabled>Note</option><option value="Note">Note</option><option value="Information">Information</option><option value="Attention">Attention</option><option value="Conseil">Conseil</option></select>
              <button onClick={() => insertSnippet("\n---\n")} title="Séparateur horizontal">—</button>
              <button onClick={() => fileInputRef.current?.click()} title="Ajouter une image" disabled={uploading}>▧</button>
              <input ref={fileInputRef} className="visually-hidden" type="file" accept="image/png,image/jpeg,image/gif,image/webp" multiple onChange={(event) => { if (event.target.files) void uploadImages(event.target.files); event.target.value = ""; }} />
            </div>}
          </div>
          <div className={`save-state ${saveStatus === "error" ? "error" : ""}`}><span className={saveStatus === "saving" ? "saving-spinner" : ""}>{saveStatus !== "saving" && <Icons.check />}</span>{uploading ? "Import de l’image…" : statusLabel}</div>
        </div>
        {loadedPath !== selectedPath ? <div className="document-loading"><span className="saving-spinner" />Chargement du document…</div> : <>
          <div className={`document-properties ${mode}`}><span>Propriétés</span>{mode === "edit" ? <><label>Nom<input value={properties.name} onChange={(event) => setDocumentProperty("name", event.target.value)} /></label><label>Description<input value={properties.description} onChange={(event) => setDocumentProperty("description", event.target.value)} placeholder="Ajouter une description…" /></label></> : <><strong>{properties.name}</strong>{properties.description && <small>{properties.description}</small>}</>}</div>
          {mode === "edit" ? <div className={`editor-wrap ${imageDrag ? "image-drag" : ""}`} onDragOver={(event) => { if (event.dataTransfer.types.includes("Files")) { event.preventDefault(); setImageDrag(true); } }} onDragLeave={() => setImageDrag(false)} onDrop={dropImages}><textarea ref={editorRef} value={content} onChange={(event) => changeContent(event.target.value, true)} aria-label="Contenu Markdown" spellCheck placeholder="Commencez à écrire en Markdown…" /><div className="editor-hint">Markdown · Enregistrement automatique · Déposez une image ou un GIF</div>{imageDrag && <div className="image-drop-overlay"><span>↓</span><strong>Déposez l’image ici</strong><small>PNG, JPEG, WebP ou GIF · 10 Mo maximum</small></div>}</div> : <div className="preview-pane"><MarkdownPreview content={content} onOpenWikiLink={openWikiLink} /></div>}
          <div className="backlinks"><div><Icons.link /><span><strong>Liens vers cette page</strong><small>{backlinks.length ? `${backlinks.length} page${backlinks.length > 1 ? "s" : ""} fait référence à ce document` : "Aucune page ne fait encore référence à ce document"}</small></span></div>{backlinks.length > 0 && <div className="backlink-list">{backlinks.map((node) => <button key={node.path} onClick={() => void select(node.path)}><Icons.file /><span>{node.name.replace(/\.md$/, "")}</span><small>{node.path}</small></button>)}</div>}</div>
        </>}
      </> : <Dashboard nodes={homeNodes} results={homeResults} query={homeQuery} space={spaceNode} onQuery={setHomeQuery} onOpenDocument={(path) => void select(path)} onOpenFolder={(path) => { setHomeQuery(""); setSearchParams({ space: path }); }} onCreateDocument={() => openCreate("document")} onCreateFolder={() => openCreate("directory")} />}
    </section>

    {notice && <div className={`toast ${notice.tone}`}><span>{notice.tone === "success" ? <Icons.check /> : "!"}</span>{notice.message}<button onClick={() => setNotice(null)} aria-label="Fermer"><Icons.x /></button></div>}

    <Dialog open={storageOpen} title="Paramètres" description="Personnalisez le fonctionnement de Mnemosys." confirmLabel="Utiliser ce dossier" busy={storageBusy} onClose={() => setStorageOpen(false)} onConfirm={() => void configureStorage()}>
      <div className="settings-section-heading"><span><Icons.folder /></span><div><strong>Stockage des documents</strong><small>Choisissez le dossier qui accueillera Mnemosys-Vault.</small></div>{storage?.configured && <b>Configuré</b>}</div>
      <div className="folder-browser">
        <div className="folder-browser-current"><button type="button" onClick={() => directoryListing?.parent && void browseStorage(directoryListing.parent)} disabled={!directoryListing?.parent || directoryBusy} title="Dossier parent">←</button><span><small>Dossier sélectionné</small><strong title={directoryListing?.path}>{directoryListing?.path ?? "Chargement…"}</strong></span></div>
        <div className="folder-browser-list">{directoryBusy ? <div className="folder-browser-state"><span className="saving-spinner" />Chargement…</div> : directoryListing?.directories.length ? directoryListing.directories.map((directory) => <button type="button" key={directory.path} onClick={() => void browseStorage(directory.path)}><Icons.folder /><span>{directory.name}</span><b>›</b></button>) : <div className="folder-browser-state">Ce dossier ne contient aucun sous-dossier.</div>}</div>
      </div>
      <div className="storage-help"><strong>Un dossier <code>Mnemosys-Vault</code> sera créé dans l’emplacement sélectionné.</strong><span>Vos pages, dossiers et médias resteront regroupés à l’intérieur. Ce réglage est conservé dans <code>config/config.toml</code>.</span>{storage?.configured && <span>Vault actuel : <code>{storage.vaultPath}</code></span>}</div>
    </Dialog>

    <Dialog open={modal?.kind === "create"} title={modal?.kind === "create" && modal.type === "directory" ? "Nouveau dossier" : "Nouvelle page"} description="Choisissez un nom simple et son emplacement." confirmLabel="Créer" busy={busy} onClose={() => setModal(null)} onConfirm={() => void submitModal()}>
      <label className="field"><span>Nom</span><input autoFocus value={modalValue} onChange={(event) => setModalValue(event.target.value)} placeholder={modal?.kind === "create" && modal.type === "directory" ? "ex. Produit" : "ex. Guide de démarrage"} /></label>
      <FolderField folders={allFolders} value={modalFolder} onChange={setModalFolder} />
    </Dialog>
    <Dialog open={modal?.kind === "rename"} title="Renommer" description={modal?.kind === "rename" ? modal.node.path : ""} confirmLabel="Renommer" busy={busy} onClose={() => setModal(null)} onConfirm={() => void submitModal()}>
      <label className="field"><span>Nouveau nom</span><input autoFocus value={modalValue} onChange={(event) => setModalValue(event.target.value)} /></label>
    </Dialog>
    <Dialog open={modal?.kind === "move"} title="Déplacer" description={modal?.kind === "move" ? `Déplacer « ${modal.node.name.replace(/\.md$/, "")} » vers…` : ""} confirmLabel="Déplacer" busy={busy} onClose={() => setModal(null)} onConfirm={() => void submitModal()}>
      <FolderField folders={allFolders.filter((folder) => modal?.kind !== "move" || (folder.path !== modal.node.path && !folder.path.startsWith(`${modal.node.path}/`)))} value={modalFolder} onChange={setModalFolder} />
    </Dialog>
    <Dialog open={modal?.kind === "delete"} title={modal?.kind === "delete" && modal.node.type === "directory" ? "Supprimer ce dossier ?" : "Supprimer cette page ?"} description="Cette action est définitive." confirmLabel="Supprimer" danger busy={busy} onClose={() => setModal(null)} onConfirm={() => void submitModal()}>
      <div className="delete-summary"><span>{modal?.kind === "delete" && modal.node.type === "directory" ? <Icons.folder /> : <Icons.file />}</span><div><strong>{modal?.kind === "delete" ? modal.node.name.replace(/\.md$/, "") : ""}</strong><small>{modal?.kind === "delete" ? modal.node.path : ""}</small></div></div>
      {modal?.kind === "delete" && modal.node.type === "directory" && <p className="delete-warning">Tous les documents et sous-dossiers qu’il contient seront également supprimés.</p>}
    </Dialog>
  </main>;
}

function FolderField({ folders, value, onChange }: { folders: Node[]; value: string; onChange: (value: string) => void }) {
  return <label className="field"><span>Emplacement</span><select value={value} onChange={(event) => onChange(event.target.value)}><option value="">Espace principal</option>{folders.map((folder) => <option key={folder.path} value={folder.path}>{folder.path}</option>)}</select></label>;
}

function Dashboard({ nodes, results, query, space, onQuery, onOpenDocument, onOpenFolder, onCreateDocument, onCreateFolder }: {
  nodes: Node[];
  results: Node[];
  query: string;
  space: Node | null;
  onQuery: (value: string) => void;
  onOpenDocument: (path: string) => void;
  onOpenFolder: (path: string) => void;
  onCreateDocument: () => void;
  onCreateFolder: () => void;
}) {
  return <div className="dashboard">
    <div className="dashboard-heading">
      <span className="eyebrow">BASE DE CONNAISSANCES</span>
      <h1>{space ? space.name : "Bonjour, que cherchez-vous ?"}</h1>
      <p>{space ? `${documents(space.children ?? []).length} document(s) dans cet espace` : "Parcourez les espaces de votre équipe ou recherchez directement une documentation."}</p>
      <div className="home-search"><span>⌕</span><input autoFocus value={query} onChange={(event) => onQuery(event.target.value)} placeholder="Rechercher par titre ou chemin…" />{query && <button onClick={() => onQuery("")} aria-label="Effacer"><Icons.x /></button>}</div>
    </div>

    {query ? <section className="search-results">
      <div className="section-title"><h2>Résultats</h2><span>{results.length}</span></div>
      {results.length ? <div className="result-list">{results.map((document) => <button key={document.path} onClick={() => onOpenDocument(document.path)}><span className="result-icon"><Icons.file /></span><span><strong>{document.name.replace(/\.md$/, "")}</strong><small>{document.path}</small></span><b>→</b></button>)}</div> : <div className="no-results"><span>⌕</span><strong>Aucun document trouvé</strong><p>Essayez avec un autre terme.</p></div>}
    </section> : <>
      <div className="section-title"><h2>{space ? "Contenu" : "Vos documentations"}</h2><span>{nodes.length}</span></div>
      {nodes.length ? <div className="space-grid">{nodes.map((node) => {
        const count = node.type === "directory" ? documents(node.children ?? []).length : 1;
        return <button className="space-card" key={node.path} onClick={() => node.type === "directory" ? onOpenFolder(node.path) : onOpenDocument(node.path)}>
          <span className={`space-card-icon ${node.type}`} >{node.type === "directory" ? <Icons.folder /> : <Icons.file />}</span>
          <span className="space-card-copy"><strong>{node.name.replace(/\.md$/, "")}</strong><small>{node.type === "directory" ? `${count} document${count > 1 ? "s" : ""}` : node.path}</small></span>
          <span className="space-card-arrow">→</span>
        </button>;
      })}</div> : <div className="dashboard-empty"><div className="welcome-icon"><Icons.book /></div><h2>Cet espace est vide</h2><p>Créez une première page ou organisez la documentation avec un dossier.</p><div><button className="button primary" onClick={onCreateDocument}><Icons.plus />Créer une page</button><button className="button secondary" onClick={onCreateFolder}><Icons.folder />Créer un dossier</button></div></div>}
    </>}
  </div>;
}
