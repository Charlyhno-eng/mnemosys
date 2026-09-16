import { useCallback, useEffect, useMemo, useRef, useState, type DragEvent } from "react";
import { useSearchParams } from "react-router-dom";
import { Dialog } from "./components/Dialog";
import { DocumentTree, type TreeAction } from "./components/DocumentTree";
import { Icons } from "./components/Icons";
import { KnowledgeGraph } from "./components/KnowledgeGraph";
import { LinkPicker } from "./components/LinkPicker";
import { MarkdownPreview } from "./components/MarkdownPreview";
import { api, type ApplicationSettings, type DirectoryListing, type Graph, type GraphNode, type Node, type PageType, type PageTypeDefinition, type StorageSettings } from "./lib/api";

type Modal =
  | { kind: "create"; type: Node["type"]; parent: string }
  | { kind: "rename" | "move" | "delete"; node: Node }
  | null;

type Notice = { message: string; tone: "success" | "error" };
type EditHistory = { past: string[]; present: string; future: string[]; lastTypingAt: number };

const fallbackPageTypes: PageTypeDefinition[] = [
  { id: "general", label: "General", description: "General-purpose documentation.", color: "#62a6e8", builtIn: true },
  { id: "business", label: "Business documentation", description: "Business rules, processes and functional knowledge.", color: "#4bc49a", builtIn: true },
  { id: "technical", label: "Technical documentation", description: "Architecture, implementation, APIs and operations.", color: "#a78bfa", builtIn: true },
  { id: "incident", label: "Incident documentation", description: "Timeline, impact, root cause and corrective actions.", color: "#f16f78", builtIn: true },
];

const pageTypeLabel = (type: PageTypeDefinition) => type.label;
const pageTypeDescription = (type: PageTypeDefinition) => type.description;

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

function documentProperty(content: string, key: "name" | "description" | "page_type", fallback = "") {
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
  const [modalPageType, setModalPageType] = useState<PageType>("general");
  const [pageTypeHelpOpen, setPageTypeHelpOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [query, setQuery] = useState("");
  const [rootDrop, setRootDrop] = useState(false);
  const [homeQuery, setHomeQuery] = useState("");
  const [uploading, setUploading] = useState(false);
  const [imageDrag, setImageDrag] = useState(false);
  const [storage, setStorage] = useState<StorageSettings | null>(null);
  const [applicationSettings, setApplicationSettings] = useState<ApplicationSettings>({ pageTypes: fallbackPageTypes });
  const [draftPageTypes, setDraftPageTypes] = useState<PageTypeDefinition[]>(fallbackPageTypes);
  const [newPageTypeName, setNewPageTypeName] = useState("");
  const [storageOpen, setStorageOpen] = useState(false);
  const [storagePath, setStoragePath] = useState("");
  const [storageBusy, setStorageBusy] = useState(false);
  const [directoryListing, setDirectoryListing] = useState<DirectoryListing | null>(null);
  const [directoryBusy, setDirectoryBusy] = useState(false);
  const [linkPickerOpen, setLinkPickerOpen] = useState(false);
  const [historyVersion, setHistoryVersion] = useState(0);
  const [updatedAt, setUpdatedAt] = useState<string | null>(null);
  const [documentID, setDocumentID] = useState<string | null>(null);
  const editorRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const historyRef = useRef<EditHistory>({ past: [], present: "", future: [], lastTypingAt: 0 });
  const pageTypes = applicationSettings.pageTypes;
  const tx = useCallback((english: string, _french: string) => english, []);

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
  const properties = useMemo(() => ({ name: documentProperty(content, "name", selectedPath ? baseName(selectedPath).replace(/\.md$/, "") : ""), description: documentProperty(content, "description"), pageType: documentProperty(content, "page_type", "general") as PageType }), [content, selectedPath]);
  const backlinks = useMemo(() => selectedPath ? graph.edges.filter((edge) => edge.type === "link" && edge.target === selectedPath).map((edge) => allEntries.find((node) => node.path === edge.source)).filter((node): node is Node => Boolean(node)) : [], [allEntries, graph.edges, selectedPath]);

  const flash = useCallback((message: string, tone: Notice["tone"] = "success") => setNotice({ message, tone }), []);
  const refreshTree = useCallback(async () => {
    const [nextTree, nextGraph] = await Promise.all([api.tree(), api.graph()]);
    setTree(nextTree);
    setGraph(nextGraph);
  }, []);

  useEffect(() => { refreshTree().catch((error) => flash(error.message, "error")).finally(() => setLoading(false)); }, [refreshTree, flash]);
  useEffect(() => {
    Promise.all([api.storage(), api.applicationSettings()]).then(([storageSettings, appSettings]) => {
      setStorage(storageSettings);
      setStoragePath(storageSettings.path);
      setApplicationSettings(appSettings);
      setDraftPageTypes(appSettings.pageTypes);
      document.documentElement.lang = "en";
    }).catch((error) => flash(error instanceof Error ? error.message : "Settings are unavailable.", "error"));
  }, [flash]);
  useEffect(() => {
    if (!notice) return;
    const timer = window.setTimeout(() => setNotice(null), 3500);
    return () => window.clearTimeout(timer);
  }, [notice]);

  useEffect(() => {
    let active = true;
    setLinkPickerOpen(false);
    if (!selectedPath) { setLoadedPath(null); resetHistory(""); setSavedContent(""); setUpdatedAt(null); setDocumentID(null); return; }
    setLoadedPath(null);
    api.get(selectedPath).then((document) => {
      if (!active) return;
      resetHistory(document.content);
      setSavedContent(document.content);
      setLoadedPath(document.path);
      setUpdatedAt(document.updatedAt);
      setDocumentID(document.id);
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
      void api.get(path).then((document) => { setUpdatedAt(document.updatedAt); setDocumentID(document.id); }).catch(() => undefined);
      setSaveStatus("saved");
      void api.graph().then(setGraph).catch(() => undefined);
      return true;
    } catch (error) {
      setSaveStatus("error");
      flash(error instanceof Error ? error.message : tx("Unable to save.", "Impossible d’enregistrer."), "error");
      return false;
    }
  }, [content, flash, loadedPath, selectedPath, tx]);

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
      if (storageOpen && event.key === "Escape" && !storageBusy) setStorageOpen(false);
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
    setModalValue(""); setModalFolder(parent); setModalPageType("general"); setPageTypeHelpOpen(false); setModal({ kind: "create", type, parent });
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
    if (node.type === "directory" && (folder === node.path || folder.startsWith(`${node.path}/`))) { flash(tx("A folder cannot be moved into itself.", "Un dossier ne peut pas être déplacé dans lui-même."), "error"); return false; }
    try {
      if (dirty && selectedPath && (selectedPath === node.path || selectedPath.startsWith(`${node.path}/`)) && !(await save(selectedPath, content))) return false;
      await api.move(node.path, destination);
      await refreshTree();
      if (selectedPath && (selectedPath === node.path || selectedPath.startsWith(`${node.path}/`))) setSearchParams({ doc: destination + selectedPath.slice(node.path.length) });
      flash(`${tx("Moved", "Déplacé")} “${node.name.replace(/\.md$/, "")}”.`);
      return true;
    } catch (error) { flash(error instanceof Error ? error.message : tx("Unable to move.", "Déplacement impossible."), "error"); return false; }
  }

  async function submitModal() {
    if (!modal) return;
    setBusy(true);
    try {
      if (modal.kind === "create") {
        let name = modalValue.trim();
        if (!name) return;
        if (name.includes("/") || name.includes("\\")) { flash(tx("The name must not contain a folder separator.", "Le nom ne doit pas contenir de séparateur de dossier."), "error"); return; }
        if (dirty && selectedPath && !(await save(selectedPath, content))) return;
        if (modal.type === "document" && !name.endsWith(".md")) name += ".md";
        const path = joinPath(modalFolder, name);
        await api.create(path, modal.type, modal.type === "document" ? `# ${name.replace(/\.md$/, "")}\n` : "", modal.type === "document" ? modalPageType : undefined);
        await refreshTree();
        if (modal.type === "document") setSearchParams({ doc: path });
        flash(modal.type === "document" ? tx("Document created.", "Document créé.") : tx("Folder created.", "Dossier créé."));
      } else if (modal.kind === "rename") {
        let name = modalValue.trim();
        if (!name) return;
        if (name.includes("/") || name.includes("\\")) { flash(tx("The name must not contain a folder separator.", "Le nom ne doit pas contenir de séparateur de dossier."), "error"); return; }
        if (modal.node.type === "document" && !name.endsWith(".md")) name += ".md";
        const destination = joinPath(parentPath(modal.node.path), name);
        if (destination !== modal.node.path) {
          if (dirty && selectedPath && (selectedPath === modal.node.path || selectedPath.startsWith(`${modal.node.path}/`)) && !(await save(selectedPath, content))) return;
          await api.move(modal.node.path, destination);
          await refreshTree();
          if (selectedPath && (selectedPath === modal.node.path || selectedPath.startsWith(`${modal.node.path}/`))) setSearchParams({ doc: destination + selectedPath.slice(modal.node.path.length) });
          flash(tx("Item renamed.", "Élément renommé."));
        }
      } else if (modal.kind === "move") {
        if (!(await moveNode(modal.node, modalFolder))) return;
      } else {
        await api.remove(modal.node.path);
        await refreshTree();
        if (selectedPath && (selectedPath === modal.node.path || selectedPath.startsWith(`${modal.node.path}/`))) setSearchParams({});
        flash(modal.node.type === "directory" ? tx("Folder deleted.", "Dossier supprimé.") : tx("Document deleted.", "Document supprimé."));
      }
      setModal(null);
    } catch (error) { flash(error instanceof Error ? error.message : tx("Action failed.", "Action impossible."), "error"); }
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

  function prefixSelection(prefix: string | ((index: number) => string), placeholder = "Item") {
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
    const stableID = target.toLowerCase().startsWith("id:") ? target.slice(3) : target;
    const stableNode = graph.nodes.find((node) => node.documentId?.toLowerCase() === stableID.toLowerCase());
    if (stableNode) { openGraphNode(stableNode); return; }
    const node = resolveWikiNode(tree, target);
    if (!node) { flash(`${tx("Link not found", "Lien introuvable")} : ${target}`, "error"); return; }
    if (node.type === "directory") setSearchParams({ space: node.path });
    else void select(node.path);
  }

  function openGraphNode(node: GraphNode) {
    if (node.type === "directory") setSearchParams({ space: node.id });
    else void select(node.id);
  }

  function insertWikiLink(node: Node) {
    const current = historyRef.current.present;
    const stableID = node.type === "document" ? graph.nodes.find((entry) => entry.id === node.path)?.documentId : undefined;
    const token = stableID ? `[[id:${stableID}|${node.name.replace(/\.md$/, "")}]]` : `[[${node.path}]]`;
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
    if (!images.length) { flash(tx("Drop a PNG, JPEG, WebP or GIF image.", "Déposez une image PNG, JPEG, WebP ou GIF."), "error"); return; }
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
      flash(images.length > 1 ? `${images.length} ${tx("images added.", "images ajoutées.")}` : tx("Image added.", "Image ajoutée."));
      window.requestAnimationFrame(() => editorRef.current?.focus());
    } catch (error) { flash(error instanceof Error ? error.message : tx("Unable to upload image.", "Import de l’image impossible."), "error"); }
    finally { setUploading(false); setImageDrag(false); }
  }

  function dropImages(event: DragEvent<HTMLDivElement>) {
    if (!event.dataTransfer.files.length) return;
    event.preventDefault();
    void uploadImages(event.dataTransfer.files);
  }

  async function configureSettings() {
    if (!storagePath.trim()) return;
    setStorageBusy(true);
    try {
      if (dirty && selectedPath && !(await save(selectedPath, content))) return;
      const [storageSettings, appSettings] = await Promise.all([
        storage?.path === storagePath.trim() ? Promise.resolve(storage) : api.configureStorage(storagePath.trim()),
        api.configureApplication({ pageTypes: draftPageTypes }),
      ]);
      setStorage(storageSettings);
      setStoragePath(storageSettings.path);
      setApplicationSettings(appSettings);
      setStorageOpen(false);
      setSearchParams({});
      await refreshTree();
      flash("Settings saved.");
    } catch (error) { flash(error instanceof Error ? error.message : tx("Settings could not be saved.", "Impossible d’enregistrer les paramètres."), "error"); }
    finally { setStorageBusy(false); }
  }

  async function browseStorage(path?: string) {
    setDirectoryBusy(true);
    try {
      const listing = await api.directories(path);
      setDirectoryListing(listing);
      setStoragePath(listing.path);
    } catch (error) { flash(error instanceof Error ? error.message : tx("Unable to open this folder.", "Impossible d’ouvrir ce dossier."), "error"); }
    finally { setDirectoryBusy(false); }
  }

  function openStoragePicker() {
    setDraftPageTypes(applicationSettings.pageTypes.map((type) => ({ ...type })));
    setNewPageTypeName("");
    setStorageOpen(true);
    void browseStorage(storage?.configured ? storage.path : undefined);
  }

  function addPageType() {
    const label = newPageTypeName.trim();
    if (!label) return;
    const base = label.normalize("NFD").replace(/[\u0300-\u036f]/g, "").toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "").slice(0, 28) || "custom";
    let id = base;
    let suffix = 2;
    while (draftPageTypes.some((type) => type.id === id)) id = `${base.slice(0, 28)}-${suffix++}`;
    const colors = ["#43bfd0", "#e5a950", "#d477d9", "#8bc34a", "#ff8a65"];
    setDraftPageTypes((types) => [...types, { id, label, description: "Custom documentation type.", color: colors[(types.length - 4) % colors.length], builtIn: false }]);
    setNewPageTypeName("");
  }

  const crumbs = graphOpen ? [tx("Graph", "Graphe")] : selectedPath?.split("/") ?? (spacePath ? spacePath.split("/") : []);
  const statusLabel = saveStatus === "saving" ? tx("Saving…", "Enregistrement…") : saveStatus === "error" ? tx("Save error", "Erreur d’enregistrement") : dirty ? tx("Unsaved changes", "Modifications en attente") : tx("Saved", "Enregistré");

  return <main className="app-shell">
    <aside className="sidebar">
      <button className="brand" onClick={() => setSearchParams({})}><span className="brand-mark"><img src="/mnemosys-logo.png?v=2" alt="" /></span><span><strong>Mnemosys</strong><span>{tx("Team memory", "Mémoire d’équipe")}</span></span></button>
      <div className="quick-actions">
        <button className="button primary grow" onClick={() => openCreate("document")}><Icons.plus />{tx("New page", "Nouvelle page")}</button>
        <button className="icon-button framed" onClick={() => openCreate("directory")} title={tx("New folder", "Nouveau dossier")} aria-label={tx("New folder", "Nouveau dossier")}><Icons.folder /></button>
      </div>
      <div className="search-box"><span>⌕</span><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={tx("Filter pages…", "Filtrer les pages…")} aria-label={tx("Filter pages", "Filtrer les pages")} />{query && <button onClick={() => setQuery("")} aria-label={tx("Clear", "Effacer")}><Icons.x /></button>}</div>
      <button className={`home-link ${!selectedPath && !spacePath && !graphOpen ? "active" : ""}`} onClick={() => setSearchParams({})}><Icons.book />{tx("Overview", "Vue d’ensemble")}</button>
      <button className={`home-link ${graphOpen ? "active" : ""}`} onClick={() => setSearchParams({ view: "graph" })}><Icons.graph />{tx("Graph", "Graphe")}</button>
      <div className="sidebar-label"><span>{tx("SPACE", "ESPACE")}</span><span>{tree.length}</span></div>
      <nav className={rootDrop ? "root-drop" : ""} data-drop-label={tx("Move to root", "Déplacer à la racine")} aria-label={tx("Documentation tree", "Arborescence documentaire")} onDragOver={(event) => { if (event.target === event.currentTarget) { event.preventDefault(); setRootDrop(true); } }} onDragLeave={() => setRootDrop(false)} onDrop={dropAtRoot}>
        {loading ? <div className="tree-loading"><span /><span /><span /></div> : <DocumentTree nodes={visibleTree} selectedPath={selectedPath} onSelect={(path) => void select(path)} onAction={openAction} onMove={(node, folder) => void moveNode(node, folder)} />}
      </nav>
    </aside>

    <section className="workspace">
      <header className="topbar">
        <div className="breadcrumbs"><button onClick={() => setSearchParams({})}>{tx("Home", "Accueil")}</button>{crumbs.map((part, index) => <span key={`${part}-${index}`}><b>/</b><span>{part.replace(/\.md$/, "")}</span></span>)}</div>
        <div className="topbar-actions">
          {storage && !storage.configured && <button className="settings-reminder" onClick={openStoragePicker}><span>!</span>{tx("Choose your storage folder", "Pensez à choisir votre dossier d’enregistrement")}</button>}
          <button className={`icon-button settings-button ${storage && !storage.configured ? "needs-attention" : ""}`} onClick={openStoragePicker} title={tx("Settings", "Paramètres")} aria-label={tx("Open settings", "Ouvrir les paramètres")}><Icons.settings /></button>
        </div>
      </header>

      {graphOpen ? <KnowledgeGraph graph={graph} pageTypes={pageTypes} onOpen={openGraphNode} /> : selectedPath ? <>
        <div className="editor-toolbar">
          <div className="toolbar-left">
            <div className="mode-switch"><button className={mode === "preview" ? "active" : ""} onClick={() => setMode("preview")}><Icons.eye />{tx("Preview", "Aperçu")}</button><button className={mode === "edit" ? "active" : ""} onClick={() => setMode("edit")}><Icons.edit />{tx("Edit", "Modifier")}</button></div>
            <div className="link-control"><button className={`link-button ${linkPickerOpen ? "active" : ""}`} onClick={() => setLinkPickerOpen((open) => !open)}><Icons.link />{tx("Link", "Relier")}</button>{linkPickerOpen && <LinkPicker nodes={allEntries.filter((node) => node.path !== selectedPath)} onSelect={insertWikiLink} onClose={() => setLinkPickerOpen(false)} />}</div>
            {mode === "edit" && <div className="format-tools" aria-label="Markdown formatting">
              <button onClick={undo} disabled={historyRef.current.past.length === 0} title="Undo (Ctrl+Z)">↶</button>
              <button onClick={redo} disabled={historyRef.current.future.length === 0} title="Redo (Ctrl+Shift+Z)">↷</button>
              <span className="tool-separator" data-history-version={historyVersion} />
              <select defaultValue="" aria-label="Heading" title="Heading" onChange={(event) => { if (event.target.value) prefixSelection(`${event.target.value} `, "Heading"); event.target.value = ""; }}><option value="" disabled>Text</option><option value="#">Heading 1</option><option value="##">Heading 2</option><option value="###">Heading 3</option></select>
              <button onClick={() => wrapSelection("**", "**", "bold text")} title="Bold Markdown"><strong>B</strong></button>
              <button onClick={() => wrapSelection("*", "*", "italic text")} title="Italic Markdown"><em>I</em></button>
              <button onClick={() => wrapSelection("++", "++", "underlined text")} title="Underline Markdown"><u>U</u></button>
              <button onClick={() => wrapSelection("~~", "~~", "struck-through text")} title="Strikethrough"><s>S</s></button>
              <button onClick={() => wrapSelection("`", "`", "code")} title="Inline code">&lt;/&gt;</button>
              <label className="color-tool" title="Text color"><input type="color" defaultValue="#6ea8f5" onChange={(event) => wrapSelection(`<span style="color: ${event.target.value}">`, "</span>", "colored text")} /><span>A</span></label>
              <span className="tool-separator" />
              <button onClick={() => prefixSelection("- ")} title="Bulleted list">•≡</button>
              <button onClick={() => prefixSelection((index) => `${index + 1}. `)} title="Numbered list">1.</button>
              <button onClick={() => prefixSelection("- [ ] ", "Task")} title="Task list">☑</button>
              <button onClick={() => prefixSelection("> ", "Quote")} title="Quote">❯</button>
              <button onClick={() => wrapSelection("[", "](https://)", "link text")} title="Link">↗</button>
              <button onClick={() => wrapSelection("\n```\n", "\n```\n", "code")} title="Code block">{`{ }`}</button>
              <button onClick={() => insertSnippet("\n| Column 1 | Column 2 | Column 3 |\n| --- | --- | --- |\n| Value | Value | Value |\n")} title="Table">▦</button>
              <select defaultValue="" aria-label="Callout" title="Callout" onChange={(event) => { const label = event.target.value; if (label) insertSnippet(`\n> **${label}**\n> Add your callout here.\n`); event.target.value = ""; }}><option value="" disabled>Note</option><option value="Note">Note</option><option value="Information">Information</option><option value="Warning">Warning</option><option value="Tip">Tip</option></select>
              <button onClick={() => insertSnippet("\n---\n")} title="Horizontal rule">—</button>
              <button onClick={() => fileInputRef.current?.click()} title="Add an image" disabled={uploading}>▧</button>
              <input ref={fileInputRef} className="visually-hidden" type="file" accept="image/png,image/jpeg,image/gif,image/webp" multiple onChange={(event) => { if (event.target.files) void uploadImages(event.target.files); event.target.value = ""; }} />
            </div>}
          </div>
          <div className={`save-state ${saveStatus === "error" ? "error" : ""}`}><span className={saveStatus === "saving" ? "saving-spinner" : ""}>{saveStatus !== "saving" && <Icons.check />}</span>{uploading ? tx("Uploading image…", "Import de l’image…") : statusLabel}</div>
        </div>
        {loadedPath !== selectedPath ? <div className="document-loading"><span className="saving-spinner" />{tx("Loading document…", "Chargement du document…")}</div> : <>
          <div className={`document-properties ${mode}`}><span>{tx("Properties", "Propriétés")}</span>{mode === "edit" ? <><label>{tx("Name", "Nom")}<input value={properties.name} onChange={(event) => setDocumentProperty("name", event.target.value)} /></label><label>Description<input value={properties.description} onChange={(event) => setDocumentProperty("description", event.target.value)} placeholder={tx("Add a description…", "Ajouter une description…")} /></label></> : <><strong>{properties.name}</strong>{properties.description && <small>{properties.description}</small>}</>}<span className="page-type-badge" style={{ color: pageTypes.find((type) => type.id === properties.pageType)?.color ?? "#62a6e8", background: `${pageTypes.find((type) => type.id === properties.pageType)?.color ?? "#62a6e8"}18` }}>{pageTypes.find((type) => type.id === properties.pageType) ? pageTypeLabel(pageTypes.find((type) => type.id === properties.pageType)!) : pageTypeLabel(pageTypes[0])}</span>{documentID && <code className="document-id" title="Stable page identifier">{documentID}</code>}{updatedAt && <time dateTime={updatedAt}>{tx("Updated", "Modifiée le")} {new Intl.DateTimeFormat("en-GB", { dateStyle: "medium", timeStyle: "short" }).format(new Date(updatedAt))}</time>}</div>
          {mode === "edit" ? <div className={`editor-wrap ${imageDrag ? "image-drag" : ""}`} onDragOver={(event) => { if (event.dataTransfer.types.includes("Files")) { event.preventDefault(); setImageDrag(true); } }} onDragLeave={() => setImageDrag(false)} onDrop={dropImages}><textarea ref={editorRef} value={content} onChange={(event) => changeContent(event.target.value, true)} aria-label={tx("Markdown content", "Contenu Markdown")} spellCheck placeholder={tx("Start writing in Markdown…", "Commencez à écrire en Markdown…")} /><div className="editor-hint">{tx("Markdown · Autosave · Drop an image or GIF", "Markdown · Enregistrement automatique · Déposez une image ou un GIF")}</div>{imageDrag && <div className="image-drop-overlay"><span>↓</span><strong>{tx("Drop the image here", "Déposez l’image ici")}</strong><small>{tx("PNG, JPEG, WebP or GIF · 10 MB maximum", "PNG, JPEG, WebP ou GIF · 10 Mo maximum")}</small></div>}</div> : <div className="preview-pane"><MarkdownPreview content={content} onOpenWikiLink={openWikiLink} /></div>}
          <div className="backlinks"><div><Icons.link /><span><strong>{tx("Links to this page", "Liens vers cette page")}</strong><small>{backlinks.length ? `${backlinks.length} ${tx(backlinks.length > 1 ? "pages reference this document" : "page references this document", backlinks.length > 1 ? "pages font référence à ce document" : "page fait référence à ce document")}` : tx("No page references this document yet", "Aucune page ne fait encore référence à ce document")}</small></span></div>{backlinks.length > 0 && <div className="backlink-list">{backlinks.map((node) => <button key={node.path} onClick={() => void select(node.path)}><Icons.file /><span>{node.name.replace(/\.md$/, "")}</span><small>{node.path}</small></button>)}</div>}</div>
        </>}
      </> : <Dashboard nodes={homeNodes} results={homeResults} query={homeQuery} space={spaceNode} onQuery={setHomeQuery} onOpenDocument={(path) => void select(path)} onOpenFolder={(path) => { setHomeQuery(""); setSearchParams({ space: path }); }} onCreateDocument={() => openCreate("document")} onCreateFolder={() => openCreate("directory")} />}
    </section>

    {notice && <div className={`toast ${notice.tone}`}><span>{notice.tone === "success" ? <Icons.check /> : "!"}</span>{notice.message}<button onClick={() => setNotice(null)} aria-label="Close"><Icons.x /></button></div>}

    {storageOpen && <div className="settings-drawer-backdrop" onMouseDown={() => { if (!storageBusy) setStorageOpen(false); }}>
      <form className="settings-drawer" role="dialog" aria-modal="true" aria-labelledby="settings-title" onMouseDown={(event) => event.stopPropagation()} onSubmit={(event) => { event.preventDefault(); void configureSettings(); }}>
        <header className="settings-drawer-header"><div><h2 id="settings-title">Settings</h2><p>Customize how Mnemosys works.</p></div><button className="icon-button" type="button" aria-label="Close settings" onClick={() => setStorageOpen(false)} disabled={storageBusy}><Icons.x /></button></header>
        <div className="settings-drawer-body">
          <div className="settings-section-heading"><span><Icons.file /></span><div><strong>Page types</strong><small>Built-in types are protected; add your own types here.</small></div></div>
          <div className="page-type-settings">{draftPageTypes.map((type, index) => <div key={type.id}><input type="color" value={type.color} aria-label="Type color" onChange={(event) => setDraftPageTypes((types) => types.map((entry, entryIndex) => entryIndex === index ? { ...entry, color: event.target.value } : entry))} /><span><strong>{pageTypeLabel(type)}</strong><small>{pageTypeDescription(type)}</small></span>{!type.builtIn && <button type="button" aria-label="Delete type" onClick={() => setDraftPageTypes((types) => types.filter((entry) => entry.id !== type.id))}><Icons.trash /></button>}</div>)}</div>
          <div className="add-page-type"><input value={newPageTypeName} onChange={(event) => setNewPageTypeName(event.target.value)} placeholder="New type name" maxLength={80} /><button className="button secondary" type="button" onClick={addPageType} disabled={!newPageTypeName.trim()}><Icons.plus />Add</button></div>
          <div className="settings-section-heading"><span><Icons.folder /></span><div><strong>Document storage</strong><small>Choose the folder that will contain Mnemosys-Vault.</small></div>{storage?.configured && <b>Configured</b>}</div>
          <div className="folder-browser">
            <div className="folder-browser-current"><button type="button" onClick={() => directoryListing?.parent && void browseStorage(directoryListing.parent)} disabled={!directoryListing?.parent || directoryBusy} title="Parent folder">←</button><span><small>Selected folder</small><strong title={directoryListing?.path}>{directoryListing?.path ?? "Loading…"}</strong></span></div>
            <div className="folder-browser-list">{directoryBusy ? <div className="folder-browser-state"><span className="saving-spinner" />Loading…</div> : directoryListing?.directories.length ? directoryListing.directories.map((directory) => <button type="button" key={directory.path} onClick={() => void browseStorage(directory.path)}><Icons.folder /><span>{directory.name}</span><b>›</b></button>) : <div className="folder-browser-state">This folder has no subfolders.</div>}</div>
          </div>
          <div className="storage-help"><strong>A Mnemosys-Vault folder will be created in the selected location.</strong><span>Pages, folders, media and settings remain grouped. Settings are stored in config/config.toml.</span>{storage?.configured && <span>Current vault: <code>{storage.vaultPath}</code></span>}</div>
        </div>
        <footer className="settings-drawer-footer"><button className="button secondary" type="button" onClick={() => setStorageOpen(false)} disabled={storageBusy}>Cancel</button><button className="button primary" type="submit" disabled={storageBusy}>{storageBusy ? "Saving…" : "Save settings"}</button></footer>
      </form>
    </div>}

    <Dialog open={modal?.kind === "create"} title={modal?.kind === "create" && modal.type === "directory" ? tx("New folder", "Nouveau dossier") : tx("New page", "Nouvelle page")} description={tx("Choose a simple name and location.", "Choisissez un nom simple et son emplacement.")} confirmLabel={tx("Create", "Créer")} busy={busy} onClose={() => setModal(null)} onConfirm={() => void submitModal()}>
      <label className="field"><span>{tx("Name", "Nom")}</span><input autoFocus value={modalValue} onChange={(event) => setModalValue(event.target.value)} placeholder={modal?.kind === "create" && modal.type === "directory" ? tx("e.g. Product", "ex. Produit") : tx("e.g. Getting started", "ex. Guide de démarrage")} /></label>
      {modal?.kind === "create" && modal.type === "document" && <div className="page-type-field"><label className="field"><span>{tx("Page type", "Type de page")} <button type="button" className="help-button" aria-label={tx("Show page type help", "Afficher l’aide sur les types de page")} aria-expanded={pageTypeHelpOpen} onClick={() => setPageTypeHelpOpen((open) => !open)}>?</button></span><select value={modalPageType} onChange={(event) => setModalPageType(event.target.value)}>{pageTypes.map((type) => <option key={type.id} value={type.id}>{pageTypeLabel(type)}</option>)}</select></label>{pageTypeHelpOpen && <div className="page-type-help">{pageTypes.map((type) => <div key={type.id}><i style={{ background: type.color }} /><span><strong>{pageTypeLabel(type)}</strong><small>{pageTypeDescription(type)}</small></span></div>)}</div>}</div>}
      <FolderField folders={allFolders} value={modalFolder} onChange={setModalFolder} />
    </Dialog>
    <Dialog open={modal?.kind === "rename"} title={tx("Rename", "Renommer")} description={modal?.kind === "rename" ? modal.node.path : ""} confirmLabel={tx("Rename", "Renommer")} busy={busy} onClose={() => setModal(null)} onConfirm={() => void submitModal()}>
      <label className="field"><span>{tx("New name", "Nouveau nom")}</span><input autoFocus value={modalValue} onChange={(event) => setModalValue(event.target.value)} /></label>
    </Dialog>
    <Dialog open={modal?.kind === "move"} title={tx("Move", "Déplacer")} description={modal?.kind === "move" ? `${tx("Move", "Déplacer")} “${modal.node.name.replace(/\.md$/, "")}” ${tx("to…", "vers…")}` : ""} confirmLabel={tx("Move", "Déplacer")} busy={busy} onClose={() => setModal(null)} onConfirm={() => void submitModal()}>
      <FolderField folders={allFolders.filter((folder) => modal?.kind !== "move" || (folder.path !== modal.node.path && !folder.path.startsWith(`${modal.node.path}/`)))} value={modalFolder} onChange={setModalFolder} />
    </Dialog>
    <Dialog open={modal?.kind === "delete"} title={modal?.kind === "delete" && modal.node.type === "directory" ? tx("Delete this folder?", "Supprimer ce dossier ?") : tx("Delete this page?", "Supprimer cette page ?")} description={tx("This action cannot be undone.", "Cette action est définitive.")} confirmLabel={tx("Delete", "Supprimer")} danger busy={busy} onClose={() => setModal(null)} onConfirm={() => void submitModal()}>
      <div className="delete-summary"><span>{modal?.kind === "delete" && modal.node.type === "directory" ? <Icons.folder /> : <Icons.file />}</span><div><strong>{modal?.kind === "delete" ? modal.node.name.replace(/\.md$/, "") : ""}</strong><small>{modal?.kind === "delete" ? modal.node.path : ""}</small></div></div>
      {modal?.kind === "delete" && modal.node.type === "directory" && <p className="delete-warning">{tx("All documents and subfolders it contains will also be deleted.", "Tous les documents et sous-dossiers qu’il contient seront également supprimés.")}</p>}
    </Dialog>
  </main>;
}

function FolderField({ folders, value, onChange }: { folders: Node[]; value: string; onChange: (value: string) => void }) {
  return <label className="field"><span>Location</span><select value={value} onChange={(event) => onChange(event.target.value)}><option value="">Main space</option>{folders.map((folder) => <option key={folder.path} value={folder.path}>{folder.path}</option>)}</select></label>;
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
  const tx = (english: string, _french: string) => english;
  return <div className="dashboard">
    <div className="dashboard-heading">
      <span className="eyebrow">{tx("KNOWLEDGE BASE", "BASE DE CONNAISSANCES")}</span>
      <h1>{space ? space.name : tx("Hello, what are you looking for?", "Bonjour, que cherchez-vous ?")}</h1>
      <p>{space ? `${documents(space.children ?? []).length} ${tx("document(s) in this space", "document(s) dans cet espace")}` : tx("Browse your team spaces or search the documentation directly.", "Parcourez les espaces de votre équipe ou recherchez directement une documentation.")}</p>
      <div className="home-search"><span>⌕</span><input autoFocus value={query} onChange={(event) => onQuery(event.target.value)} placeholder={tx("Search by title or path…", "Rechercher par titre ou chemin…")} />{query && <button onClick={() => onQuery("")} aria-label={tx("Clear", "Effacer")}><Icons.x /></button>}</div>
    </div>

    {query ? <section className="search-results">
      <div className="section-title"><h2>{tx("Results", "Résultats")}</h2><span>{results.length}</span></div>
      {results.length ? <div className="result-list">{results.map((document) => <button key={document.path} onClick={() => onOpenDocument(document.path)}><span className="result-icon"><Icons.file /></span><span><strong>{document.name.replace(/\.md$/, "")}</strong><small>{document.path}</small></span><b>→</b></button>)}</div> : <div className="no-results"><span>⌕</span><strong>{tx("No documents found", "Aucun document trouvé")}</strong><p>{tx("Try another search term.", "Essayez avec un autre terme.")}</p></div>}
    </section> : <>
      <div className="section-title"><h2>{space ? tx("Content", "Contenu") : tx("Your documentation", "Vos documentations")}</h2><span>{nodes.length}</span></div>
      {nodes.length ? <div className="space-grid">{nodes.map((node) => {
        const count = node.type === "directory" ? documents(node.children ?? []).length : 1;
        return <button className="space-card" key={node.path} onClick={() => node.type === "directory" ? onOpenFolder(node.path) : onOpenDocument(node.path)}>
          <span className={`space-card-icon ${node.type}`} >{node.type === "directory" ? <Icons.folder /> : <Icons.file />}</span>
          <span className="space-card-copy"><strong>{node.name.replace(/\.md$/, "")}</strong><small>{node.type === "directory" ? `${count} document${count > 1 ? "s" : ""}` : node.path}</small></span>
          <span className="space-card-arrow">→</span>
        </button>;
      })}</div> : <div className="dashboard-empty"><div className="welcome-icon"><Icons.book /></div><h2>{tx("This space is empty", "Cet espace est vide")}</h2><p>{tx("Create a first page or organize documentation with a folder.", "Créez une première page ou organisez la documentation avec un dossier.")}</p><div><button className="button primary" onClick={onCreateDocument}><Icons.plus />{tx("Create a page", "Créer une page")}</button><button className="button secondary" onClick={onCreateFolder}><Icons.folder />{tx("Create a folder", "Créer un dossier")}</button></div></div>}
    </>}
  </div>;
}
