import { useState } from "react";
import type { DocumentProposal, ProposalStatus } from "../lib/api";
import { Icons } from "./Icons";

const statusLabels: Record<ProposalStatus, string> = {
  draft: "Draft", in_review: "In review", needs_human_input: "Needs human input",
  approved: "Approved", rejected: "Rejected", merged: "Merged",
};
export const isResolvedProposal = (proposal: DocumentProposal) => proposal.status === "merged" || proposal.status === "rejected";
const dateLabel = (value: string) => new Intl.DateTimeFormat("en-GB", { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
const pageName = (path: string) => path.slice(path.lastIndexOf("/") + 1).replace(/\.md$/, "");

function StatusBadge({ status }: { status: ProposalStatus }) {
  return <span className={`merge-status status-${status}`}><i />{statusLabels[status]}</span>;
}

function Diff({ proposal }: { proposal: DocumentProposal }) {
  const lines = proposal.diff.trimEnd().split("\n");
  const added = lines.filter((line) => line.startsWith("+ ")).length;
  const removed = lines.filter((line) => line.startsWith("- ")).length;
  const visible = new Set([0, 1]);
  lines.forEach((line, index) => {
    if (line.startsWith("+ ") || line.startsWith("- ")) {
      for (let context = Math.max(2, index - 3); context <= Math.min(lines.length - 1, index + 3); context++) visible.add(context);
    }
  });
  const excerpt: Array<{ text: string; kind: string }> = [];
  let skipped = 0;
  lines.forEach((line, index) => {
    if (!visible.has(index)) { skipped++; return; }
    if (skipped) { excerpt.push({ text: `⋯ ${skipped} unchanged lines`, kind: "diff-skipped" }); skipped = 0; }
    excerpt.push({ text: line, kind: line.startsWith("+ ") ? "diff-added" : line.startsWith("- ") ? "diff-removed" : "diff-context" });
  });
  if (skipped) excerpt.push({ text: `⋯ ${skipped} unchanged lines`, kind: "diff-skipped" });
  return <div className="merge-diff-wrap">
    <div className="merge-diff-heading"><span>Current → Proposed</span><span><b className="diff-added">+{added}</b><b className="diff-removed">−{removed}</b></span></div>
    <pre className="merge-diff" aria-label={`Proposed changes for ${proposal.path}`}>{excerpt.map((line, index) => <span key={index} className={line.kind}>{line.text}{"\n"}</span>)}</pre>
  </div>;
}

type ReviewActions = {
  busy: boolean;
  mergeBlocked: boolean;
  onAccept: (id: string, content?: string) => Promise<void>;
  onReject: (id: string) => Promise<void>;
  onStatusChange: (id: string, status: ProposalStatus) => Promise<void>;
};

function ReviewCard({ proposal, canReview, busy, mergeBlocked, onAccept, onReject, onStatusChange }: ReviewActions & { proposal: DocumentProposal; canReview: boolean }) {
  const [editing, setEditing] = useState(false);
  const [content, setContent] = useState(proposal.proposedContent);
  const resolved = isResolvedProposal(proposal);
  return <article className="merge-review-card">
    <header><div><span className="eyebrow">AI CHANGE REQUEST</span><strong>Proposal #{proposal.id.slice(0, 8)}</strong><small>Submitted {dateLabel(proposal.createdAt)}</small></div><StatusBadge status={proposal.status} /></header>
    <Diff proposal={proposal} />
    {editing && !resolved && canReview && <label className="merge-edit-label">Proposed Markdown<textarea className="merge-editor" value={content} onChange={(event) => setContent(event.target.value)} disabled={busy} spellCheck={false} /></label>}
    {!resolved && canReview && <footer>
      <label className="merge-status-control">Review status<select value={proposal.status} disabled={busy} onChange={(event) => void onStatusChange(proposal.id, event.target.value as ProposalStatus)}>{Object.entries(statusLabels).filter(([status]) => status !== "merged" && status !== "rejected").map(([status, label]) => <option key={status} value={status}>{label}</option>)}</select></label>
      <div className="merge-review-actions"><button className="button secondary" disabled={busy} onClick={() => setEditing((value) => !value)}><Icons.edit />{editing ? "Hide editor" : "Edit proposal"}</button><button className="button merge-reject" disabled={busy} onClick={() => void onReject(proposal.id)}>Reject</button><button className="button primary" disabled={busy || mergeBlocked} onClick={() => void onAccept(proposal.id, content !== proposal.proposedContent ? content : undefined)}><Icons.check />Merge into page</button></div>
      {mergeBlocked && <p className="merge-review-note">Save or resolve your page changes before merging a proposal.</p>}
    </footer>}
  </article>;
}

export function PageMergeProposals({ proposals, error, canReview, ...actions }: ReviewActions & { proposals: DocumentProposal[]; error: string; canReview: boolean }) {
  if (!proposals.length && !error) return null;
  const pending = proposals.filter((proposal) => !isResolvedProposal(proposal)).length;
  return <section className="page-merge-proposals" aria-label="Page merge proposals">
    {error && <p className="merge-error" role="alert">{error}</p>}
    {proposals.length > 0 && <details open={pending > 0}>
      <summary><span className="merge-section-icon"><Icons.merge /></span><span><strong>Merge proposals <b>{pending || proposals.length}</b></strong><small>{pending ? `${pending} awaiting review · Review AI changes before they enter this page.` : "All requests resolved · View this page’s proposal history."}</small></span><Icons.chevron /></summary>
      <div className="merge-review-list">{proposals.map((proposal) => <ReviewCard key={proposal.id} proposal={proposal} canReview={canReview} {...actions} />)}</div>
    </details>}
  </section>;
}

export function MergeHistory({ proposals, loading, error, onOpenDocument }: { proposals: DocumentProposal[]; loading: boolean; error: string; onOpenDocument: (path: string) => void }) {
  const [query, setQuery] = useState("");
  const [status, setStatus] = useState<ProposalStatus | "all">("all");
  const filtered = [...proposals].reverse().filter((proposal) => (status === "all" || proposal.status === status) && `${proposal.path} ${proposal.id}`.toLowerCase().includes(query.trim().toLowerCase()));
  const pending = proposals.filter((proposal) => !isResolvedProposal(proposal)).length;
  return <div className="merge-history">
    <header className="merge-history-heading"><span className="eyebrow">AI COLLABORATION</span><h1>Merge requests</h1><p>Every AI proposal, from submission to resolution. Open its page to review and merge changes.</p><span className="merge-readonly"><Icons.eye />Read-only activity</span></header>
    <div className="merge-stats">{[["Total requests", proposals.length], ["Awaiting review", pending], ["Merged", proposals.filter((p) => p.status === "merged").length], ["Rejected", proposals.filter((p) => p.status === "rejected").length]].map(([label, count]) => <div key={label}><span>{label}</span><strong>{count}</strong></div>)}</div>
    <div className="merge-filters"><label className="merge-search"><span>⌕</span><input aria-label="Search merge requests" placeholder="Search by page or request ID…" value={query} onChange={(event) => setQuery(event.target.value)} /></label><label className="merge-filter-status">Status<select value={status} onChange={(event) => setStatus(event.target.value as ProposalStatus | "all")}><option value="all">All statuses</option>{Object.entries(statusLabels).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label></div>
    {error && <p className="merge-error" role="alert">{error}</p>}
    {loading ? <div className="merge-empty"><span className="saving-spinner" /><p>Loading merge requests…</p></div> : filtered.length ? <div className="merge-history-list"><div className="merge-list-heading"><span>Request / Page</span><span>Status & activity</span></div>{filtered.map((proposal) => <article className="merge-history-row" key={proposal.id}>
      <div className="merge-row-main"><span className="merge-row-icon"><Icons.merge /></span><div className="merge-row-copy"><strong>{pageName(proposal.path)}</strong><small title={proposal.path}>{proposal.path}</small><span>#{proposal.id.slice(0, 8)} · {dateLabel(proposal.createdAt)}</span></div><StatusBadge status={proposal.status} /><button className="button secondary" onClick={() => onOpenDocument(proposal.path)}>Open page <Icons.chevron /></button></div>
      <details className="merge-history-details"><summary>View submitted changes</summary><Diff proposal={proposal} /></details>
    </article>)}</div> : <div className="merge-empty"><span className="merge-empty-icon"><Icons.merge /></span><h2>{proposals.length ? "No matching requests" : "No merge requests yet"}</h2><p>{proposals.length ? "Try another search or status filter." : "AI proposals will appear here when an agent submits changes to a page."}</p></div>}
  </div>;
}
