import { useState, type DragEvent } from "react";
import { Icons } from "./Icons";
import type { Node } from "../lib/api";

export type TreeAction = "rename" | "move" | "delete" | "new-document" | "new-directory";

type Props = {
  nodes: Node[];
  selectedPath: string | null;
  onSelect: (path: string) => void;
  onAction: (node: Node, action: TreeAction) => void;
  onMove: (source: Node, targetFolder: string) => void;
};

export function DocumentTree({ nodes, selectedPath, onSelect, onAction, onMove }: Props) {
  const tx = (english: string, _french: string) => english;
  const [openFolders, setOpenFolders] = useState<Set<string>>(new Set());
  const [menuPath, setMenuPath] = useState<string | null>(null);
  const [dropPath, setDropPath] = useState<string | null>(null);

  function toggleFolder(path: string) {
    setOpenFolders((current) => {
      const next = new Set(current);
      next.has(path) ? next.delete(path) : next.add(path);
      return next;
    });
  }

  function beginDrag(event: DragEvent, node: Node) {
    event.dataTransfer.effectAllowed = "move";
    event.dataTransfer.setData("application/x-mnemosys-path", node.path);
    event.dataTransfer.setData("application/x-mnemosys-type", node.type);
  }

  function drop(event: DragEvent, folder: Node) {
    event.preventDefault();
    event.stopPropagation();
    setDropPath(null);
    const path = event.dataTransfer.getData("application/x-mnemosys-path");
    const type = event.dataTransfer.getData("application/x-mnemosys-type") as Node["type"];
    if (path && path !== folder.path) onMove({ path, type, name: path.split("/").pop() ?? path }, folder.path);
  }

  function render(items: Node[], depth = 0) {
    return <ul className="tree-list">{items.map((node) => {
      const folder = node.type === "directory";
      const expanded = folder && openFolders.has(node.path);
      return <li key={node.path} className="tree-item">
        <div className={`tree-row ${node.path === selectedPath ? "selected" : ""} ${dropPath === node.path ? "drop-target" : ""}`}
          style={{ paddingLeft: `${10 + depth * 15}px` }} draggable onDragStart={(event) => beginDrag(event, node)}
          onContextMenu={(event) => { event.preventDefault(); event.stopPropagation(); setMenuPath(node.path); }}
          onDragOver={folder ? (event) => { event.preventDefault(); event.dataTransfer.dropEffect = "move"; setDropPath(node.path); } : undefined}
          onDragLeave={folder ? () => setDropPath(null) : undefined} onDrop={folder ? (event) => drop(event, node) : undefined}>
          <button className="tree-main" type="button" onClick={() => folder ? toggleFolder(node.path) : onSelect(node.path)} title={node.path}>
            {folder ? <Icons.chevron className={`tree-chevron ${expanded ? "expanded" : ""}`} /> : <span className="tree-spacer" />}
            {folder ? <Icons.folder className="tree-icon folder-icon" /> : <Icons.file className="tree-icon file-icon" />}
            <span className="tree-label">{folder ? node.name : node.name.replace(/\.md$/, "")}</span>
          </button>
          <button className="icon-button tree-more" type="button" onClick={() => setMenuPath(menuPath === node.path ? null : node.path)} aria-label={`${tx("Actions for", "Actions pour")} ${node.name}`} aria-expanded={menuPath === node.path}><Icons.more /></button>
          {menuPath === node.path && <>
            <button className="menu-scrim" type="button" aria-label={tx("Close menu", "Fermer le menu")} onClick={() => setMenuPath(null)} />
            <div className="context-menu">
              {folder && <>
                <button type="button" onClick={() => { setMenuPath(null); onAction(node, "new-document"); }}><Icons.file />{tx("New document", "Nouveau document")}</button>
                <button type="button" onClick={() => { setMenuPath(null); onAction(node, "new-directory"); }}><Icons.folder />{tx("New folder", "Nouveau dossier")}</button>
                <span className="menu-separator" />
              </>}
              <button type="button" onClick={() => { setMenuPath(null); onAction(node, "rename"); }}><Icons.edit />{tx("Rename", "Renommer")}</button>
              <button type="button" onClick={() => { setMenuPath(null); onAction(node, "move"); }}><Icons.move />{tx("Move", "Déplacer")}</button>
              <button className="destructive" type="button" onClick={() => { setMenuPath(null); onAction(node, "delete"); }}><Icons.trash />{tx("Delete", "Supprimer")}</button>
            </div>
          </>}
        </div>
        {folder && expanded && render(node.children ?? [], depth + 1)}
      </li>;
    })}</ul>;
  }

  if (nodes.length === 0) return <div className="tree-empty"><Icons.file /><p>{tx("No documents", "Aucun document")}</p><span>{tx("Create your first page", "Créez votre première page")}</span></div>;
  return render(nodes);
}
