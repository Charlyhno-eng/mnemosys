export type Node = {
  name: string;
  path: string;
  type: "directory" | "document";
  children?: Node[];
};

export type PageType = string;
export type PageTypeDefinition = { id: PageType; label: string; description: string; color: string; builtIn: boolean };
export type ApplicationSettings = { pageTypes: PageTypeDefinition[] };
export type Document = { id: string; path: string; content: string; pageType: PageType; updatedAt: string };
export type StorageSettings = { path: string; vaultPath: string; configured: boolean };
export type DirectoryListing = { path: string; parent?: string; directories: Array<{ name: string; path: string }> };
export type GraphNode = { id: string; documentId?: string; name: string; type: Node["type"]; pageType?: PageType };
export type GraphEdge = { source: string; target: string; type: "hierarchy" | "link" };
export type Graph = { nodes: GraphNode[]; edges: GraphEdge[] };

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, init);
  if (!response.ok) {
    const body = (await response.json().catch(() => null)) as { error?: string } | null;
    throw new Error(body?.error ?? "Une erreur inattendue est survenue.");
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
  get: (path: string) => request<Document>(`/api/documents/content?path=${encodeURIComponent(path)}`),
  create: (path: string, type: "directory" | "document", content = "", pageType?: PageType) =>
    request<{ path: string }>("/api/documents", { method: "POST", ...json({ path, type, content, ...(pageType ? { pageType } : {}) }) }),
  update: (path: string, content: string) =>
    request<{ path: string }>("/api/documents", { method: "PUT", ...json({ path, content }) }),
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
  configureApplication: (settings: ApplicationSettings) => request<ApplicationSettings>("/api/settings/application", { method: "PUT", ...json(settings) }),
  configureStorage: (path: string) => request<StorageSettings>("/api/settings/storage", { method: "PUT", ...json({ path }) }),
  directories: (path?: string) => request<DirectoryListing>(`/api/settings/directories${path ? `?path=${encodeURIComponent(path)}` : ""}`),
};
