export type Node = {
  name: string;
  path: string;
  type: "directory" | "document";
  children?: Node[];
};

export type Document = { path: string; content: string };
export type StorageSettings = { path: string; vaultPath: string; configured: boolean };
export type DirectoryListing = { path: string; parent?: string; directories: Array<{ name: string; path: string }> };
export type GraphNode = { id: string; name: string; type: Node["type"] };
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
  create: (path: string, type: "directory" | "document", content = "") =>
    request<{ path: string }>("/api/documents", { method: "POST", ...json({ path, type, content }) }),
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
  configureStorage: (path: string) => request<StorageSettings>("/api/settings/storage", { method: "PUT", ...json({ path }) }),
  directories: (path?: string) => request<DirectoryListing>(`/api/settings/directories${path ? `?path=${encodeURIComponent(path)}` : ""}`),
};
