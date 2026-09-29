export type Node = {
  name: string;
  path: string;
  type: "directory" | "document";
  children?: Node[];
  title?: string;
  pageType?: PageType;
  owner?: string;
  application?: string;
  aiEditable?: boolean;
  lastModifiedBy?: ProfileType;
  aiTouched?: boolean;
  updatedAt?: string;
  team?: string;
  folderPath?: string;
};

export type PageType = string;
export type ProfileType = "human" | "ai";
export type PageTypeDefinition = { id: PageType; label: string; description: string; color: string; builtIn: boolean };
export type ProfileSettings = { type: ProfileType; firstName: string; lastName: string; name: string; team: string };
export type Permissions = { view: boolean; create: boolean; edit: boolean; delete: boolean };
export type SavedProfile = ProfileSettings & { id: string; permissions: Permissions };
export type ApplicationSettings = { pageTypes: PageTypeDefinition[]; profile: ProfileSettings; aiPermissions: Permissions; profiles: SavedProfile[]; activeProfileId: string };
export type Document = { id: string; path: string; content: string; revision: string; pageType: PageType; owner: string; application: string; aiEditable: boolean; lastModifiedBy: ProfileType; aiTouched: boolean; updatedAt: string };
export type ProposalStatus = "draft" | "in_review" | "needs_human_input" | "approved" | "rejected" | "merged";
export type DocumentProposal = { id: string; path: string; originalContent: string; proposedContent: string; diff: string; status: ProposalStatus; createdAt: string };
export type StorageSettings = { path: string; vaultPath: string; configured: boolean };
export type DirectoryListing = { path: string; parent?: string; directories: Array<{ name: string; path: string }> };
export type GraphNode = { id: string; documentId?: string; name: string; type: Node["type"]; pageType?: PageType };
export type GraphEdge = { source: string; target: string; type: "hierarchy" | "link" };
export type Graph = { nodes: GraphNode[]; edges: GraphEdge[] };
export type SearchMode = "names" | "lexical" | "hybrid";
export type SearchResult = { path: string; type: "directory" | "document"; name: string; title: string; pageType: PageType; snippet: string; score: number; matchTypes: Array<"name" | "lexical" | "semantic"> };
export type SearchResponse = { query: string; mode: SearchMode; results: SearchResult[] };

export class APIError extends Error {
  constructor(message: string, readonly status: number, readonly currentDocument?: Document) {
    super(message);
    this.name = "APIError";
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, init);
  if (!response.ok) {
    const body = (await response.json().catch(() => null)) as { error?: string; currentDocument?: Document } | null;
    throw new APIError(body?.error ?? "An unexpected error occurred.", response.status, body?.currentDocument);
  }
  if (response.status === 204) return undefined as T;
  return response.json() as Promise<T>;
}

const json = (body: unknown): RequestInit => ({
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
});

export const api = {
  tree: () => request<Node[]>("/api/documents/tree"),
  graph: () => request<Graph>("/api/documents/graph"),
  search: (query: string, mode: SearchMode, scope?: string) => {
    const parameters = new URLSearchParams({ q: query, mode });
    if (scope) parameters.set("scope", scope);
    return request<SearchResponse>(`/api/documents/search?${parameters}`);
  },
  get: (path: string) => request<Document>(`/api/documents/content?path=${encodeURIComponent(path)}`),
  create: (path: string, type: "directory" | "document", content = "", pageType?: PageType) =>
    request<{ path: string }>("/api/documents", { method: "POST", ...json({ path, type, content, ...(pageType ? { pageType } : {}) }) }),
  update: (path: string, content: string, baseRevision: string) =>
    request<{ path: string; proposal?: DocumentProposal; document?: Document }>("/api/documents", { method: "PUT", ...json({ path, content, baseRevision }) }),
  move: (path: string, newPath: string) =>
    request<{ path: string }>("/api/documents", { method: "PUT", ...json({ path, newPath }) }),
  remove: (path: string) => request<void>(`/api/documents?path=${encodeURIComponent(path)}`, { method: "DELETE" }),
  upload: (file: File) => {
    const body = new FormData();
    body.append("file", file);
    return request<{ url: string }>("/api/assets", { method: "POST", body });
  },
  storage: () => request<StorageSettings>("/api/settings/storage"),
  applicationSettings: () => request<ApplicationSettings>("/api/settings/application"),
  createProfile: (profile: ProfileSettings, permissions: Permissions) => request<ApplicationSettings>("/api/profiles", { method: "POST", ...json({ profile, permissions }) }),
  updateProfile: (id: string, profile: ProfileSettings, permissions: Permissions) => request<ApplicationSettings>(`/api/profiles/${encodeURIComponent(id)}`, { method: "PUT", ...json({ profile, permissions }) }),
  activateProfile: (id: string) => request<ApplicationSettings>(`/api/profiles/${encodeURIComponent(id)}/activate`, { method: "PUT" }),
  proposals: () => request<DocumentProposal[]>("/api/documents/proposals"),
  acceptProposal: (id: string, content?: string) => request<void>(`/api/documents/proposals/${encodeURIComponent(id)}/accept`, { method: "POST", ...json(content === undefined ? {} : { content }) }),
  rejectProposal: (id: string) => request<void>(`/api/documents/proposals/${encodeURIComponent(id)}`, { method: "DELETE" }),
  setProposalStatus: (id: string, status: ProposalStatus) => request<void>(`/api/documents/proposals/${encodeURIComponent(id)}`, { method: "PATCH", ...json({ status }) }),
  configureApplication: (profile: ProfileSettings, aiPermissions: Permissions) => request<ApplicationSettings>("/api/settings/application", { method: "PUT", ...json({ profile, aiPermissions }) }),
  configureStorage: (path: string) => request<StorageSettings>("/api/settings/storage", { method: "PUT", ...json({ path }) }),
  directories: (path?: string) => request<DirectoryListing>(`/api/settings/directories${path ? `?path=${encodeURIComponent(path)}` : ""}`),
};
