import { useEffect, type FormEvent, type ReactNode } from "react";
import { Icons } from "./Icons";

type Props = {
  open: boolean;
  title: string;
  description?: string;
  children: ReactNode;
  confirmLabel: string;
  danger?: boolean;
  busy?: boolean;
  dismissible?: boolean;
  onClose: () => void;
  onConfirm: () => void;
};

export function Dialog({ open, title, description, children, confirmLabel, danger, busy, dismissible = true, onClose, onConfirm }: Props) {
  useEffect(() => {
    if (!open) return;
    const close = (event: KeyboardEvent) => { if (event.key === "Escape" && !busy && dismissible) onClose(); };
    window.addEventListener("keydown", close);
    return () => window.removeEventListener("keydown", close);
  }, [open, busy, dismissible, onClose]);

  if (!open) return null;
  const submit = (event: FormEvent) => { event.preventDefault(); onConfirm(); };
  return <div className="dialog-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget && !busy && dismissible) onClose(); }}>
    <form className="dialog" role="dialog" aria-modal="true" aria-labelledby="dialog-title" onSubmit={submit}>
      {dismissible && <button className="icon-button dialog-close" type="button" onClick={onClose} disabled={busy} aria-label="Close"><Icons.x /></button>}
      <div className="dialog-heading">
        <h2 id="dialog-title">{title}</h2>
        {description && <p>{description}</p>}
      </div>
      <div className="dialog-body">{children}</div>
      <div className="dialog-footer">
        {dismissible && <button className="button secondary" type="button" onClick={onClose} disabled={busy}>Cancel</button>}
        <button className={`button ${danger ? "danger" : "primary"}`} type="submit" disabled={busy}>{busy ? "Processing…" : confirmLabel}</button>
      </div>
    </form>
  </div>;
}
