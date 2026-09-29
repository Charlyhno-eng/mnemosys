import { useCallback, useEffect, useMemo, useRef, useState, type DragEvent } from "react";
import { useSearchParams } from "react-router-dom";
import { Dialog } from "./components/Dialog";
import { DocumentTree, type TreeAction } from "./components/DocumentTree";
import { Icons } from "./components/Icons";
import { KnowledgeGraph } from "./components/KnowledgeGraph";
import { LinkPicker } from "./components/LinkPicker";
import { MarkdownPreview } from "./components/MarkdownPreview";
import { APIError, api, type ApplicationSettings, type DirectoryListing, type Document, type DocumentProposal, type Graph, type GraphNode, type Node, type PageType, type PageTypeDefinition, type Permissions, type ProfileSettings, type SavedProfile, type ProposalStatus, type SearchMode, type SearchResult, type StorageSettings } from "./lib/api";

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
function wikiTarget(value: string) {
  return value;
}

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

function documentProperty(content: string, key: "ai_editable", fallback = "") {
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
    if (node.type === "document") {
      const metadata = [node.name, node.path, node.title, node.pageType, node.owner, node.application, node.lastModifiedBy, node.updatedAt].filter(Boolean).join(" ").toLocaleLowerCase();
      return metadata.includes(normalized) ? [node] : [];
    }
    const children = filterTree(node.children ?? [], query);
    return node.name.toLocaleLowerCase().includes(normalized) || children.length ? [{ ...node, children }] : [];
  });
}

export function App() {
  const [searchParams, setSearchParams] = useSearchParams();
  const selectedPath = searchParams.get("doc");
  const spacePath = searchParams.get("space");
  const graphOpen = searchParams.get("view") === "graph";
  const docsOpen = searchParams.get("view") === "docs";
  const [tree, setTree] = useState<Node[]>([]);
  const [graph, setGraph] = useState<Graph>({ nodes: [], edges: [] });
  const [content, setContent] = useState("");
  const [savedContent, setSavedContent] = useState("");
  const [loadedPath, setLoadedPath] = useState<string | null>(null);
  const [mode, setMode] = useState<"edit" | "preview">("preview");
  const [loading, setLoading] = useState(true);
  const [saveStatus, setSaveStatus] = useState<"idle" | "saving" | "saved" | "proposed" | "conflict" | "error">("idle");
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
  const [homeSearchMode, setHomeSearchMode] = useState<SearchMode>("lexical");
  const [homeResults, setHomeResults] = useState<SearchResult[]>([]);
  const [homeSearchLoading, setHomeSearchLoading] = useState(false);
  const [homeSearchError, setHomeSearchError] = useState("");
  const [uploading, setUploading] = useState(false);
  const [imageDrag, setImageDrag] = useState(false);
  const [storage, setStorage] = useState<StorageSettings | null>(null);
  const [applicationSettings, setApplicationSettings] = useState<ApplicationSettings>({ pageTypes: fallbackPageTypes, profile: { type: "human", firstName: "", lastName: "", name: "", team: "" }, aiPermissions: { view: false, create: false, edit: false, delete: false }, profiles: [], activeProfileId: "" });
  const [storageOpen, setStorageOpen] = useState(false);
  const [storagePath, setStoragePath] = useState("");
  const [storageBusy, setStorageBusy] = useState(false);
  const [directoryListing, setDirectoryListing] = useState<DirectoryListing | null>(null);
  const [directoryBusy, setDirectoryBusy] = useState(false);
  const [proposals, setProposals] = useState<DocumentProposal[]>([]);
  const [linkPickerOpen, setLinkPickerOpen] = useState(false);
  const [historyVersion, setHistoryVersion] = useState(0);
  const [revision, setRevision] = useState("");
  const [editConflict, setEditConflict] = useState<Document | null>(null);
  const editorRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const historyRef = useRef<EditHistory>({ past: [], present: "", future: [], lastTypingAt: 0 });
  const saveInFlightRef = useRef(false);
  const pageTypes = applicationSettings.pageTypes;
  const tx = useCallback((english: string, _french: string) => english, []);

  const allFolders = useMemo(() => folders(tree), [tree]);
  const allEntries = useMemo(() => entries(tree), [tree]);
  const visibleTree = useMemo(() => filterTree(tree, query), [tree, query]);
  const spaceNode = useMemo(() => findNode(tree, spacePath), [tree, spacePath]);
  const homeNodes = spaceNode?.type === "directory" ? spaceNode.children ?? [] : tree;
  const dirty = loadedPath === selectedPath && content !== savedContent;
  const aiEditable = documentProperty(content, "ai_editable", "false") === "true";
  const activePermissions = !applicationSettings.activeProfileId ? { view: false, create: false, edit: false, delete: false } : applicationSettings.profile.type === "human" ? { view: true, create: true, edit: true, delete: true } : applicationSettings.aiPermissions;
  const canEditDocument = activePermissions.edit && (applicationSettings.profile.type === "human" || aiEditable);
  const backlinks = useMemo(() => selectedPath ? graph.edges.flatMap((edge) => {
    if (edge.type === "hierarchy" || edge.target !== selectedPath) return [];
    const node = allEntries.find((entry) => entry.path === edge.source);
    return node ? [node] : [];
  }) : [], [allEntries, graph.edges, selectedPath]);

  const flash = useCallback((message: string, tone: Notice["tone"] = "success") => setNotice({ message, tone }), []);
  const refreshTree = useCallback(async () => {
    const [nextTree, nextGraph] = await Promise.all([api.tree(), api.graph()]);
    setTree(nextTree);
    setGraph(nextGraph);
  }, []);
  const refreshProposals = useCallback(async () => {
    setProposals(await api.proposals());
  }, []);

  useEffect(() => {
    Promise.all([api.storage(), api.applicationSettings()]).then(([storageSettings, appSettings]) => {
      setStorage(storageSettings);
      setStoragePath(storageSettings.path);
      setApplicationSettings(appSettings);
      if (appSettings.activeProfileId && (appSettings.profile.type === "human" || appSettings.aiPermissions.view)) {
        void refreshTree().catch((error) => flash(error.message, "error")).finally(() => setLoading(false));
        api.proposals().then(setProposals).catch(() => setProposals([]));
      } else setLoading(false);
      document.documentElement.lang = "en";
    }).catch((error) => flash(error instanceof Error ? error.message : "Settings are unavailable.", "error"));
  }, [flash, refreshTree]);
  useEffect(() => {
    if (!notice) return;
    const timer = window.setTimeout(() => setNotice(null), 3500);
    return () => window.clearTimeout(timer);
  }, [notice]);

  useEffect(() => {
    const query = homeQuery.trim();
    if (!query) {
      setHomeResults([]);
      setHomeSearchLoading(false);
      setHomeSearchError("");
      return;
    }
    let active = true;
    setHomeSearchLoading(true);
    setHomeSearchError("");
    const timer = window.setTimeout(() => {
      api.search(query, homeSearchMode, spaceNode?.path).then((response) => {
        if (active) setHomeResults(response.results);
      }).catch((error) => {
        if (active) {
          setHomeResults([]);
          setHomeSearchError(error instanceof Error ? error.message : "Search is unavailable.");
        }
      }).finally(() => { if (active) setHomeSearchLoading(false); });
    }, 250);
    return () => { active = false; window.clearTimeout(timer); };
  }, [homeQuery, homeSearchMode, spaceNode?.path]);

  useEffect(() => {
    let active = true;
    setLinkPickerOpen(false);
    if (!selectedPath || !applicationSettings.activeProfileId || !activePermissions.view) { setLoadedPath(null); resetHistory(""); setSavedContent(""); setRevision(""); setEditConflict(null); return; }
    setLoadedPath(null);
    api.get(selectedPath).then((document) => {
      if (!active) return;
      resetHistory(document.content);
      setSavedContent(document.content);
      setLoadedPath(document.path);
      setRevision(document.revision);
      setEditConflict(null);
      setSaveStatus("idle");
      setMode("preview");
    }).catch((error) => { if (active) flash(error.message, "error"); });
    return () => { active = false; };
  }, [selectedPath, applicationSettings.activeProfileId, activePermissions.view, flash]);

  const save = useCallback(async (path = selectedPath, value = content, baseRevision = revision) => {
    if (!path || loadedPath !== path || !baseRevision || saveInFlightRef.current) return false;
    saveInFlightRef.current = true;
    setSaveStatus("saving");
    try {
      const result = await api.update(path, value, baseRevision);
      const savedDocument = result.document;
      if (savedDocument) {
        setRevision(savedDocument.revision);
        if (result.proposal) {
          setSavedContent(value);
        } else if (historyRef.current.present === value) {
          resetHistory(savedDocument.content);
          setSavedContent(savedDocument.content);
        } else {
          setSavedContent(value);
        }
      } else {
        setSavedContent(value);
      }
      setEditConflict(null);
      if (result.proposal) {
        setSaveStatus("proposed");
        void refreshProposals().catch(() => undefined);
        flash("AI change submitted as a proposal.");
      } else {
        setSaveStatus("saved");
      }
      void refreshTree().catch(() => undefined);
      return true;
    } catch (error) {
      if (error instanceof APIError && error.status === 409 && error.currentDocument) {
        setEditConflict(error.currentDocument);
        setSaveStatus("conflict");
        flash("This page was changed elsewhere. Review the concurrent version before saving.", "error");
        return false;
      }
      setSaveStatus("error");
      flash(error instanceof Error ? error.message : tx("Unable to save.", "Impossible d’enregistrer."), "error");
      return false;
    } finally {
      saveInFlightRef.current = false;
    }
  }, [content, flash, loadedPath, refreshProposals, refreshTree, revision, selectedPath, tx]);

  useEffect(() => {
    if (!dirty || !selectedPath || editConflict || saveStatus === "saving") return;
    const timer = window.setTimeout(() => void save(selectedPath, content), 800);
    return () => window.clearTimeout(timer);
  }, [content, dirty, editConflict, save, saveStatus, selectedPath]);

  function loadConcurrentVersion() {
    if (!editConflict) return;
    resetHistory(editConflict.content);
    setSavedContent(editConflict.content);
    setRevision(editConflict.revision);
    setEditConflict(null);
    setSaveStatus("idle");
  }

  function overwriteConcurrentVersion() {
    if (!editConflict || !selectedPath) return;
    const currentRevision = editConflict.revision;
    setRevision(currentRevision);
    setEditConflict(null);
    void save(selectedPath, content, currentRevision);
  }

  useEffect(() => {
    const shortcut = (event: KeyboardEvent) => {
      const editing = Boolean(selectedPath && mode === "edit" && !modal && !storageOpen);
      if (editing && (event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "z") { event.preventDefault(); event.shiftKey ? redo() : undo(); return; }
      if (editing && (event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "y") { event.preventDefault(); redo(); return; }
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "s") { event.preventDefault(); void save(); }
      if (activePermissions.create && !modal && !storageOpen && (event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "n") { event.preventDefault(); openCreate("document"); }
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

  function openWikiLink(target: string) {
    target = wikiTarget(target);
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
    const target = stableID ? `id:${stableID}` : node.path;
    const label = node.name.replace(/\.md$/, "");
    const token = `[[${target}|${label}]]`;
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
      const storageSettings = storage?.path === storagePath.trim() ? storage : await api.configureStorage(storagePath.trim());
      setStorage(storageSettings);
      setStoragePath(storageSettings.path);
      setStorageOpen(false);
      setSearchParams({});
      await refreshTree();
      flash("Settings saved.");
    } catch (error) { flash(error instanceof Error ? error.message : tx("Settings could not be saved.", "Impossible d’enregistrer les paramètres."), "error"); }
    finally { setStorageBusy(false); }
  }

  async function acceptProposal(id: string, content?: string) {
    try {
      await api.acceptProposal(id, content);
      await Promise.all([refreshProposals(), refreshTree()]);
      flash("AI proposal accepted.");
    } catch (error) { flash(error instanceof Error ? error.message : "Unable to accept proposal.", "error"); }
  }

  async function rejectProposal(id: string) {
    try {
      await api.rejectProposal(id);
      await refreshProposals();
      flash("AI proposal rejected.");
    } catch (error) { flash(error instanceof Error ? error.message : "Unable to reject proposal.", "error"); }
  }

  async function setProposalStatus(id: string, status: ProposalStatus) {
    try {
      await api.setProposalStatus(id, status);
      await refreshProposals();
      flash(`AI proposal marked ${status.replace(/_/g, " ")}.`);
    } catch (error) { flash(error instanceof Error ? error.message : "Unable to update proposal status.", "error"); }
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
    setStorageOpen(true);
    void browseStorage(storage?.configured ? storage.path : undefined);
  }

  async function saveProfile(id: string | null, profile: ProfileSettings, permissions: Permissions) {
    const settings = id ? await api.updateProfile(id, profile, permissions) : await api.createProfile(profile, permissions);
    setApplicationSettings(settings);
    if (settings.activeProfileId && (settings.profile.type === "human" || settings.aiPermissions.view)) void refreshTree().catch((error) => flash(error.message, "error"));
    flash(id ? "Profile updated." : "Profile added.");
  }

  async function activateProfile(id: string) {
    if (dirty && selectedPath && !(await save(selectedPath, content))) return;
    const settings = await api.activateProfile(id);
    setApplicationSettings(settings);
    setSearchParams({});
    if (settings.profile.type === "human" || settings.aiPermissions.view) await refreshTree();
    else { setTree([]); setGraph({ nodes: [], edges: [] }); }
    flash("Profile selected.");
  }

  const crumbs = graphOpen ? [tx("Graph", "Graphe")] : selectedPath?.split("/") ?? (spacePath ? spacePath.split("/") : []);
  const statusLabel = saveStatus === "saving" ? tx("Saving…", "Enregistrement…") : saveStatus === "proposed" ? tx("Proposal pending review", "Proposition en attente") : saveStatus === "conflict" ? "Concurrent edit" : saveStatus === "error" ? tx("Save error", "Erreur d’enregistrement") : dirty ? tx("Unsaved changes", "Modifications en attente") : tx("Saved", "Enregistré");

  return <main className="app-shell">
    <aside className="sidebar">
      <button className="brand" onClick={() => setSearchParams({})}><span className="brand-mark"><img src="/mnemosys-logo.png?v=2" alt="" /></span><span><strong>Mnemosys</strong><span>{tx("Team memory", "Mémoire d’équipe")}</span></span></button>
      <div className="quick-actions">
        <button className="button primary grow" onClick={() => openCreate("document")} disabled={!activePermissions.create}><Icons.plus />{tx("New page", "Nouvelle page")}</button>
        <button className="icon-button framed" onClick={() => openCreate("directory")} disabled={!activePermissions.create} title={tx("New folder", "Nouveau dossier")} aria-label={tx("New folder", "Nouveau dossier")}><Icons.folder /></button>
      </div>
      <div className="search-box"><span>⌕</span><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={tx("Filter pages…", "Filtrer les pages…")} aria-label={tx("Filter pages", "Filtrer les pages")} />{query && <button onClick={() => setQuery("")} aria-label={tx("Clear", "Effacer")}><Icons.x /></button>}</div>
      <button className={`home-link ${!selectedPath && !spacePath && !graphOpen && !docsOpen ? "active" : ""}`} onClick={() => setSearchParams({})}><Icons.book />Profiles</button>
      <button className={`home-link ${docsOpen ? "active" : ""}`} onClick={() => setSearchParams({ view: "docs" })} disabled={!applicationSettings.activeProfileId}><Icons.file />Documentation</button>
      <button className={`home-link ${graphOpen ? "active" : ""}`} onClick={() => setSearchParams({ view: "graph" })} disabled={!applicationSettings.activeProfileId}><Icons.graph />{tx("Graph", "Graphe")}</button>
      <div className="sidebar-label"><span>{tx("SPACE", "ESPACE")}</span><span>{tree.length}</span></div>
      <nav className={rootDrop ? "root-drop" : ""} data-drop-label={tx("Move to root", "Déplacer à la racine")} aria-label={tx("Documentation tree", "Arborescence documentaire")} onDragOver={(event) => { if (event.target === event.currentTarget) { event.preventDefault(); setRootDrop(true); } }} onDragLeave={() => setRootDrop(false)} onDrop={dropAtRoot}>
        {loading ? <div className="tree-loading"><span /><span /><span /></div> : <DocumentTree nodes={visibleTree} selectedPath={selectedPath} permissions={activePermissions} onSelect={(path) => void select(path)} onAction={openAction} onMove={(node, folder) => void moveNode(node, folder)} />}
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

      {!applicationSettings.activeProfileId || (!selectedPath && !spacePath && !graphOpen && !docsOpen) ? <ProfilePage settings={applicationSettings} onSave={saveProfile} onActivate={activateProfile} onOpenDocs={() => setSearchParams({ view: "docs" })} /> : graphOpen ? <KnowledgeGraph graph={graph} pageTypes={pageTypes} onOpen={openGraphNode} /> : selectedPath ? <>
        <div className="editor-toolbar">
          <div className="toolbar-left">
          <div className="mode-switch"><button className={mode === "preview" ? "active" : ""} onClick={() => setMode("preview")}><Icons.eye />{tx("Preview", "Aperçu")}</button><button className={mode === "edit" ? "active" : ""} onClick={() => setMode("edit")} disabled={!canEditDocument}><Icons.edit />{tx("Edit", "Modifier")}</button></div>
            {canEditDocument && <div className="link-control"><button className={`link-button ${linkPickerOpen ? "active" : ""}`} onClick={() => setLinkPickerOpen((open) => !open)}><Icons.link />{tx("Link", "Relier")}</button>{linkPickerOpen && <LinkPicker nodes={allEntries.filter((node) => node.path !== selectedPath)} onSelect={insertWikiLink} onClose={() => setLinkPickerOpen(false)} />}</div>}
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
          <div className={`save-state ${saveStatus === "error" || saveStatus === "conflict" ? "error" : ""}`}><span className={saveStatus === "saving" ? "saving-spinner" : ""}>{saveStatus !== "saving" && <Icons.check />}</span>{uploading ? tx("Uploading image…", "Import de l’image…") : statusLabel}</div>
        </div>
        {loadedPath !== selectedPath ? <div className="document-loading"><span className="saving-spinner" />{tx("Loading document…", "Chargement du document…")}</div> : <>
          {editConflict && <section className="edit-conflict" role="alert"><div><strong>Concurrent changes detected</strong><p>Another person or agent saved this page after you opened it. Your draft is still in the editor.</p><small>Latest saved version from {new Intl.DateTimeFormat("en-GB", { dateStyle: "medium", timeStyle: "short" }).format(new Date(editConflict.updatedAt))}</small><details><summary>Review latest saved Markdown</summary><pre>{editConflict.content}</pre></details></div><div className="edit-conflict-actions"><button className="button secondary" onClick={loadConcurrentVersion}>Load latest version</button><button className="button conflict-overwrite" onClick={overwriteConcurrentVersion}>Save my draft over latest</button></div></section>}
          {mode === "edit" ? <div className={`editor-wrap ${imageDrag ? "image-drag" : ""}`} onDragOver={(event) => { if (event.dataTransfer.types.includes("Files")) { event.preventDefault(); setImageDrag(true); } }} onDragLeave={() => setImageDrag(false)} onDrop={dropImages}><textarea ref={editorRef} value={content} onChange={(event) => changeContent(event.target.value, true)} aria-label={tx("Markdown content", "Contenu Markdown")} spellCheck placeholder={tx("Start writing in Markdown…", "Commencez à écrire en Markdown…")} /><div className="editor-hint">{tx("Markdown · Autosave · Drop an image or GIF", "Markdown · Enregistrement automatique · Déposez une image ou un GIF")}</div>{imageDrag && <div className="image-drop-overlay"><span>↓</span><strong>{tx("Drop the image here", "Déposez l’image ici")}</strong><small>{tx("PNG, JPEG, WebP or GIF · 10 MB maximum", "PNG, JPEG, WebP ou GIF · 10 Mo maximum")}</small></div>}</div> : <div className="preview-pane"><MarkdownPreview content={content} onOpenWikiLink={openWikiLink} /></div>}
          <div className="backlinks"><div><Icons.link /><span><strong>{tx("Links to this page", "Liens vers cette page")}</strong><small>{backlinks.length ? `${backlinks.length} ${tx(backlinks.length > 1 ? "pages reference this document" : "page references this document", backlinks.length > 1 ? "pages font référence à ce document" : "page fait référence à ce document")}` : tx("No page references this document yet", "Aucune page ne fait encore référence à ce document")}</small></span></div>{backlinks.length > 0 && <div className="backlink-list">{backlinks.map((node) => <button key={node.path} onClick={() => void select(node.path)}><Icons.file /><span>{node.name.replace(/\.md$/, "")}</span><small>Reference · {node.path}</small></button>)}</div>}</div>
        </>}
      </> : <Dashboard nodes={homeNodes} results={homeResults} query={homeQuery} searchMode={homeSearchMode} searchLoading={homeSearchLoading} searchError={homeSearchError} space={spaceNode} canCreate={activePermissions.create} proposals={proposals} onAcceptProposal={(id, content) => void acceptProposal(id, content)} onRejectProposal={(id) => void rejectProposal(id)} onStatusChange={(id, status) => void setProposalStatus(id, status)} onQuery={setHomeQuery} onSearchMode={setHomeSearchMode} onOpenDocument={(path) => void select(path)} onOpenFolder={(path) => { setHomeQuery(""); setSearchParams({ space: path }); }} onCreateDocument={() => openCreate("document")} onCreateFolder={() => openCreate("directory")} />}
    </section>

    {notice && <div className={`toast ${notice.tone}`}><span>{notice.tone === "success" ? <Icons.check /> : "!"}</span>{notice.message}<button onClick={() => setNotice(null)} aria-label="Close"><Icons.x /></button></div>}

    {storageOpen && <div className="settings-drawer-backdrop" onMouseDown={() => { if (!storageBusy) setStorageOpen(false); }}>
      <form className="settings-drawer" role="dialog" aria-modal="true" aria-labelledby="settings-title" onMouseDown={(event) => event.stopPropagation()} onSubmit={(event) => { event.preventDefault(); void configureSettings(); }}>
        <header className="settings-drawer-header"><div><h2 id="settings-title">Settings</h2><p>Customize how Mnemosys works.</p></div><button className="icon-button" type="button" aria-label="Close settings" onClick={() => setStorageOpen(false)} disabled={storageBusy}><Icons.x /></button></header>
        <div className="settings-drawer-body">
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

function Dashboard({ nodes, results, query, searchMode, searchLoading, searchError, space, canCreate, proposals, onAcceptProposal, onRejectProposal, onStatusChange, onQuery, onSearchMode, onOpenDocument, onOpenFolder, onCreateDocument, onCreateFolder }: {
  nodes: Node[];
  results: SearchResult[];
  query: string;
  searchMode: SearchMode;
  searchLoading: boolean;
  searchError: string;
  space: Node | null;
  canCreate: boolean;
  proposals: DocumentProposal[];
  onAcceptProposal: (id: string, content?: string) => void;
  onRejectProposal: (id: string) => void;
  onStatusChange: (id: string, status: ProposalStatus) => void;
  onQuery: (value: string) => void;
  onSearchMode: (mode: SearchMode) => void;
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
      <div className="home-search"><span>⌕</span><input autoFocus value={query} onChange={(event) => onQuery(event.target.value)} placeholder={tx("Search titles and Markdown content…", "Rechercher dans les titres et le contenu Markdown…")} />{searchLoading && <i className="saving-spinner" />}{query && <button onClick={() => onQuery("")} aria-label={tx("Clear", "Effacer")}><Icons.x /></button>}</div>
      <div className="search-mode-toggle" aria-label="Search mode"><button className={searchMode === "names" ? "active" : ""} onClick={() => onSearchMode("names")}><strong>Names</strong><span>File and folder names</span></button><button className={searchMode === "lexical" ? "active" : ""} onClick={() => onSearchMode("lexical")}><strong>Metadata</strong><span>Exact terms across metadata and content</span></button><button className={searchMode === "hybrid" ? "active" : ""} onClick={() => onSearchMode("hybrid")}><strong>Semantic</strong><span>Lexical + semantic context</span></button></div>
    </div>

    {!space && !query && proposals.length > 0 && <section className="proposal-inbox" aria-label="AI proposals">
      <div className="section-title"><h2>AI proposals</h2><span>{proposals.length}</span></div>
      <div className="proposal-list">{proposals.map((proposal) => <ProposalCard key={proposal.id} proposal={proposal} onAccept={onAcceptProposal} onReject={onRejectProposal} onStatusChange={onStatusChange} />)}</div>
    </section>}

    {query ? <section className="search-results">
      <div className="section-title"><h2>{tx("Results", "Résultats")}</h2><span>{results.length}</span></div>
      {searchError ? <div className="no-results"><span>!</span><strong>Search unavailable</strong><p>{searchError}</p></div> : results.length ? <div className="result-list">{results.map((result) => <button key={result.path} onClick={() => result.type === "directory" ? onOpenFolder(result.path) : onOpenDocument(result.path)}><span className="result-icon">{result.type === "directory" ? <Icons.folder /> : <Icons.file />}</span><span><strong>{result.title || result.name.replace(/\.md$/, "")}</strong><small>{result.path}</small>{result.snippet && <p>{result.snippet}</p>}<i>{result.matchTypes.map((matchType) => <em key={matchType} className={matchType}>{matchType}</em>)}</i></span><b>→</b></button>)}</div> : !searchLoading && <div className="no-results"><span>⌕</span><strong>{tx("No pages or folders found", "Aucune page ou dossier trouvé")}</strong><p>{tx("Try another search term or Semantic mode.", "Essayez un autre terme ou le mode sémantique.")}</p></div>}
    </section> : <>
      <div className="section-title"><h2>{space ? tx("Content", "Contenu") : tx("Your documentation", "Vos documentations")}</h2><span>{nodes.length}</span></div>
      {nodes.length ? <div className="space-grid">{nodes.map((node) => {
        const count = node.type === "directory" ? documents(node.children ?? []).length : 1;
        return <button className="space-card" key={node.path} onClick={() => node.type === "directory" ? onOpenFolder(node.path) : onOpenDocument(node.path)}>
          <span className={`space-card-icon ${node.type}`} >{node.type === "directory" ? <Icons.folder /> : <Icons.file />}</span>
          <span className="space-card-copy"><strong>{node.name.replace(/\.md$/, "")}</strong><small>{node.type === "directory" ? `${count} document${count > 1 ? "s" : ""}` : node.path}</small></span>
          <span className="space-card-arrow">→</span>
        </button>;
      })}</div> : <div className="dashboard-empty"><div className="welcome-icon"><Icons.book /></div><h2>{tx("This space is empty", "Cet espace est vide")}</h2><p>{canCreate ? tx("Create a first page or organize documentation with a folder.", "Créez une première page ou organisez la documentation avec un dossier.") : "This profile has read-only access."}</p>{canCreate && <div><button className="button primary" onClick={onCreateDocument}><Icons.plus />{tx("Create a page", "Créer une page")}</button><button className="button secondary" onClick={onCreateFolder}><Icons.folder />{tx("Create a folder", "Créer un dossier")}</button></div>}</div>}
    </>}
  </div>;
}

function ProposalCard({ proposal, onAccept, onReject, onStatusChange }: { proposal: DocumentProposal; onAccept: (id: string, content?: string) => void; onReject: (id: string) => void; onStatusChange: (id: string, status: ProposalStatus) => void }) {
  const [content, setContent] = useState(proposal.proposedContent);
  const [editing, setEditing] = useState(false);
  useEffect(() => { setContent(proposal.proposedContent); }, [proposal.proposedContent]);
  const final = proposal.status === "merged" || proposal.status === "rejected";
  const statusLabel: Record<ProposalStatus, string> = { draft: "Draft", in_review: "In review", needs_human_input: "Needs human input", approved: "Approved", rejected: "Rejected", merged: "Merged" };
  return <article className="proposal-card">
    <div className="proposal-card-heading"><div><span className="profile-kind">AI WORK ITEM</span><strong>{proposal.path}</strong><small>{new Intl.DateTimeFormat("en-GB", { dateStyle: "medium", timeStyle: "short" }).format(new Date(proposal.createdAt))}</small></div><div className="proposal-card-controls"><span className="proposal-status">{statusLabel[proposal.status]}</span>{!final && <button className="icon-button" onClick={() => onReject(proposal.id)} title="Reject proposal" aria-label="Reject proposal"><Icons.x /></button>}</div></div>
    <pre className="proposal-diff">{proposal.diff}</pre>
    {!final && <><label className="proposal-status-control">Status<select value={proposal.status} onChange={(event) => onStatusChange(proposal.id, event.target.value as ProposalStatus)}><option value="draft">Draft</option><option value="in_review">In review</option><option value="needs_human_input">Needs human input</option><option value="approved">Approved</option></select></label>{editing && <textarea className="proposal-editor" value={content} onChange={(event) => setContent(event.target.value)} aria-label={`Edit proposal for ${proposal.path}`} />}<div className="proposal-actions"><button className="button secondary" onClick={() => setEditing((open) => !open)}>{editing ? "Hide editor" : "Modify"}</button><button className="button primary" onClick={() => onAccept(proposal.id, editing ? content : undefined)}>Merge proposal</button><button className="button ghost" onClick={() => onReject(proposal.id)}>Reject</button></div></>}
  </article>;
}

function ProfilePage({ settings, onSave, onActivate, onOpenDocs }: {
  settings: ApplicationSettings;
  onSave: (id: string | null, profile: ProfileSettings, permissions: Permissions) => Promise<void>;
  onActivate: (id: string) => Promise<void>;
  onOpenDocs: () => void;
}) {
  const blank = (): ProfileSettings => ({ type: "human", firstName: "", lastName: "", name: "", team: "" });
  const emptyRights = (): Permissions => ({ view: true, create: false, edit: false, delete: false });
  const [editing, setEditing] = useState<string | null>(null);
  const [formOpen, setFormOpen] = useState(false);
  const [profile, setProfile] = useState<ProfileSettings>(blank);
  const [rights, setRights] = useState<Permissions>(emptyRights);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const canManage = !settings.activeProfileId || settings.profile.type === "human";
  const title = (entry: SavedProfile) => entry.type === "ai" ? entry.name || "AI" : `${entry.firstName} ${entry.lastName}`.trim();

  function start(entry?: SavedProfile) {
    setEditing(entry?.id ?? null);
    setProfile(entry ? { type: entry.type, firstName: entry.firstName, lastName: entry.lastName, name: entry.name || "", team: entry.team } : blank());
    setRights(entry?.permissions ?? emptyRights());
    setError("");
    setFormOpen(true);
  }

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      await onSave(editing, profile, rights);
      setFormOpen(false);
    } catch (cause) { setError(cause instanceof Error ? cause.message : "Could not save profile."); }
    finally { setBusy(false); }
  }

  async function select(id: string) {
    setError("");
    try { await onActivate(id); }
    catch (cause) { setError(cause instanceof Error ? cause.message : "Could not select profile."); }
  }

  return <div className="profile-page">
    <div className="profile-page-heading"><div><span className="profile-kind">MNEMOSYS</span><h1>Profiles</h1><p>Create human and AI profiles, then set each AI profile's access.</p></div>{canManage && settings.profiles.length > 0 && <button className="button primary" onClick={() => start()}><Icons.plus />Add profile</button>}</div>
    {settings.profiles.length ? <div className="profile-list">{settings.profiles.map((entry) => <article className="profile-list-card" key={entry.id}>
      <div className="profile-avatar">{title(entry).slice(0, 1).toUpperCase()}</div>
      <div className="profile-list-copy"><span className="profile-kind">{entry.type === "ai" ? "AI" : "HUMAN"}{entry.id === settings.activeProfileId ? " · ACTIVE" : ""}</span><h2>{title(entry)}</h2><p>{entry.team || "No team"}</p>{entry.type === "ai" && <div className="profile-rights">{(["view", "create", "edit", "delete"] as const).map((right) => <span className={entry.permissions[right] ? "granted" : ""} key={right}>{right}</span>)}</div>}</div>
      <div className="profile-list-actions">{canManage && <button className="button secondary" onClick={() => start(entry)}>Edit</button>}{entry.type === "human" && entry.id !== settings.activeProfileId && settings.profile.type !== "ai" && <button className="button secondary" onClick={() => void select(entry.id)}>Use profile</button>}</div>
    </article>)}</div> : <div className="profile-empty"><h2>No profiles yet</h2><p>Add a human profile to start using the vault. You can also add AI profiles and set their rights here.</p><button className="button primary" onClick={() => start()}><Icons.plus />Add profile</button></div>}
    {!formOpen && error && <p className="settings-error" role="alert">{error}</p>}
    {!settings.activeProfileId && settings.profiles.length > 0 && <p className="profile-note">Add a human profile to start using documentation.</p>}
    {settings.activeProfileId && <button className="button secondary profile-docs-link" onClick={onOpenDocs}>Open documentation →</button>}
    {formOpen && <div className="profile-form-backdrop" onMouseDown={() => !busy && setFormOpen(false)}><form className="profile-form" onMouseDown={(event) => event.stopPropagation()} onSubmit={(event) => void submit(event)} aria-label={editing ? "Edit profile" : "Add profile"}>
      <div className="profile-form-heading"><h2>{editing ? "Edit profile" : "Add profile"}</h2><button type="button" className="icon-button" onClick={() => setFormOpen(false)} aria-label="Close" disabled={busy}><Icons.x /></button></div>
      {!editing && <div className="profile-type-options"><label><input type="radio" name="new-profile-type" checked={profile.type === "human"} onChange={() => setProfile({ ...blank(), type: "human" })} />Human</label><label><input type="radio" name="new-profile-type" checked={profile.type === "ai"} onChange={() => setProfile({ ...blank(), type: "ai" })} />AI</label></div>}
      {profile.type === "human" ? <div className="profile-name-fields"><label className="field"><span>First name</span><input required maxLength={100} value={profile.firstName} onChange={(event) => setProfile({ ...profile, firstName: event.target.value })} /></label><label className="field"><span>Last name</span><input required maxLength={100} value={profile.lastName} onChange={(event) => setProfile({ ...profile, lastName: event.target.value })} /></label></div> : <label className="field"><span>AI name</span><input required maxLength={100} value={profile.name} onChange={(event) => setProfile({ ...profile, name: event.target.value })} /></label>}
      <label className="field"><span>Team</span><input maxLength={100} value={profile.team} onChange={(event) => setProfile({ ...profile, team: event.target.value })} /></label>
      {profile.type === "human" && <p className="profile-note">Human profiles have full access.</p>}
      {profile.type === "ai" && <><h3>AI access</h3><div className="permission-settings">{(["view", "create", "edit", "delete"] as const).map((right) => <label key={right}><input type="checkbox" checked={rights[right]} onChange={(event) => setRights(right === "view" && !event.target.checked ? { view: false, create: false, edit: false, delete: false } : { ...rights, [right]: event.target.checked, view: right === "view" ? event.target.checked : true })} /><span>{right[0].toUpperCase() + right.slice(1)}</span></label>)}</div></>}
      {error && <p className="settings-error" role="alert">{error}</p>}
      <div className="profile-form-actions"><button type="button" className="button secondary" onClick={() => setFormOpen(false)} disabled={busy}>Cancel</button><button className="button primary" type="submit" disabled={busy}>{busy ? "Saving…" : editing ? "Save profile" : "Add profile"}</button></div>
    </form></div>}
  </div>;
}
