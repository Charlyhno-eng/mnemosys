# Appeler le MCP Mnemosys en TypeScript

Voir aussi le [même cours en Python](mcp-python.md).

Ces deux cours suivent la même progression : préparer le serveur, initialiser
MCP, découvrir les outils, lire et rechercher, proposer une modification, créer
et supprimer, puis diagnostiquer les erreurs. Les exemples utilisent directement
HTTP/JSON-RPC, sans SDK MCP ni service externe. Ils correspondent au serveur
implémenté dans [`backend/documents/mcp.go`](../backend/documents/mcp.go).

## 1. Préparer Mnemosys et le profil IA

Depuis la racine du projet, lancer le serveur :

```bash
go run ./apps/api
```

L’URL MCP est `http://127.0.0.1:8080/mcp`. Pour changer le port, utiliser
`-addr 127.0.0.1:18080` et adapter `MNEMOSYS_MCP_URL`.

Dans l’interface (voir le [Quickstart](../README.md#quickstart)), créer un profil
humain, puis un profil IA sur la page **Profiles**. Accorder `view` pour les
exemples de lecture ; les exercices suivants demandent aussi `edit`, `create`
et `delete`. Ces droits se configurent avec un profil humain. Le profil humain
actif dans le navigateur ne donne aucun droit supplémentaire au client MCP.

Récupérer les profils avec cette requête REST de configuration :

```bash
curl --fail-with-body http://127.0.0.1:8080/api/settings/application
```

Dans le tableau `profiles`, copier l’`id` de l’entrée dont `type` vaut `ai`.
L’envoyer dans `X-Mnemosys-Profile-ID` à chaque appel MCP. Cet identifiant choisit
une politique de permissions ; il ne constitue pas une authentification.
Les nouvelles configurations refusent les appels d’outils sans cet identifiant.
Seule une ancienne configuration avec le profil actif `legacy` peut encore
utiliser ses permissions IA historiques sans cet en-tête.

Créer une page `demo.md` avec le profil humain. Pour les exercices de proposition,
remplacer `ai_editable: false` par `ai_editable: true` dans son frontmatter et
attendre la fin de la sauvegarde. Tous les chemins MCP sont relatifs au coffre,
par exemple `guides/demo.md`, sans préfixe `Mnemosys-Vault/`. Les fichiers doivent
se terminer par `.md` et les dossiers parents doivent déjà exister.

Pour tester les écritures sans utiliser votre coffre habituel, lancer plutôt
une instance avec une configuration et un stockage temporaires. Arrêter d’abord
le serveur habituel pour libérer le port 8080 :

```bash
mcp_demo_dir=$(mktemp -d)
go run ./apps/api -addr 127.0.0.1:8080 \
  -documents-path "$mcp_demo_dir/vault" \
  -config-path "$mcp_demo_dir/config.toml"
```

Pour préparer cette instance dans l’interface, démarrer Vite avec
`npm --prefix frontend run dev`. Créer les profils et la page sur cette instance,
puis utiliser son identifiant IA dans le client. Les deux options serveur
ci-dessus isolent aussi la configuration ; `-documents-path` seul ne remplace pas un stockage déjà
sélectionné dans une configuration existante.

## 2. Comprendre l’échange MCP

Le serveur implémente la version `2025-06-18` avec un transport Streamable HTTP
sans session. Il accepte des POST et renvoie du JSON ; il n’ouvre pas de flux SSE
persistant. GET et DELETE sur `/mcp` renvoient HTTP 405. Il n’y a ni processus
MCP séparé, ni transport stdio, ni `Mcp-Session-Id` à conserver.

Chaque POST doit contenir :

```text
Content-Type: application/json
Accept: application/json, text/event-stream
X-Mnemosys-Profile-ID: <id du profil IA>
```

Commencer par `initialize` avec `protocolVersion`, `capabilities: {}` et
`clientInfo`. Vérifier la version renvoyée, puis envoyer la notification
`notifications/initialized` sans `id` : la réponse est HTTP 202, sans JSON.
Ajouter ensuite `MCP-Protocol-Version: 2025-06-18` aux requêtes. Les appels
ordinaires ont un `id` unique et reçoivent une réponse avec le même `id`.

`tools/list` découvre les outils et leurs schémas. `tools/call` reçoit
`{"name": "read", "arguments": {"path": "demo.md"}}`. Tous les arguments
d’outils sont des chaînes : aucun booléen, nombre ou `null` n’est accepté.
La taille maximale du corps HTTP est de 1 Mio. Envoyer un seul objet JSON-RPC
par POST, sans tableau de requêtes groupées.

La réponse d’un outil contient du JSON **encodé dans un bloc de texte** :

```json
{
  "jsonrpc": "2.0",
  "id": 4,
  "result": {
    "content": [{"type": "text", "text": "{\"valid\":true,\"issues\":[],\"path\":\"demo.md\"}"}],
    "isError": false
  }
}
```

Il faut donc décoder le corps HTTP, puis `result.content[0].text`. Vérifier
`result.isError`, même si HTTP vaut 200. Une erreur JSON-RPC figure plutôt dans
le champ `error` de premier niveau. Les clients ci-dessous traitent ces trois
niveaux d’erreur : HTTP, JSON-RPC et outil.

## 3. Écrire le client TypeScript

Utiliser Node.js 24 pour exécuter directement ce TypeScript avec `fetch` natif
et suppression des annotations de types. Aucun paquet npm n’est nécessaire
pour l’exécution. Ce client est un programme Node.js, pas un composant React.

Enregistrer le bloc ci-dessous dans `client.ts`, puis lancer dans un autre terminal :

```bash
export MNEMOSYS_MCP_URL='http://127.0.0.1:8080/mcp'
export MNEMOSYS_PROFILE_ID='<id du profil IA>'
node client.ts demo.md
```

Remplacer les valeurs par celles de votre instance. Le chemin peut aussi être
`guides/demo.md`. Sans argument, le client lit `demo.md`. Ce programme complet
ne fait que des lectures et affiche le serveur `mnemosys`, les huit outils,
l’arbre, les résultats de recherche, le Markdown et sa révision, puis
`valid: true` et une liste `issues` vide pour la page d’exercice valide.

```typescript
import process from "node:process";

type ToolResult = {
  content: { type: string; text: string }[];
  isError?: boolean;
};
type DocumentResult = { path: string; content: string; revision: string };

class MCPClient {
  private version: string | undefined;
  private requestId = 0;

  private url: string;
  private profileId: string;

  constructor(url: string, profileId: string) {
    this.url = url;
    this.profileId = profileId;
  }

  async rpc<T>(method: string, params?: object, notification = false): Promise<T> {
    const id = notification ? undefined : ++this.requestId;
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
      Accept: "application/json, text/event-stream",
      "X-Mnemosys-Profile-ID": this.profileId,
    };
    if (this.version) headers["MCP-Protocol-Version"] = this.version;
    const response = await fetch(this.url, {
      method: "POST", headers,
      body: JSON.stringify({ jsonrpc: "2.0", id, method, params }),
      signal: AbortSignal.timeout(15_000),
    });
    const body = await response.text();
    if (!response.ok) throw new Error(`HTTP ${response.status}: ${body}`);
    if (notification) {
      if (response.status !== 202) throw new Error(`Expected HTTP 202: ${response.status}`);
      return undefined as T;
    }
    const reply = JSON.parse(body);
    if (reply.jsonrpc !== "2.0" || reply.id !== id) throw new Error("Invalid JSON-RPC response");
    if (reply.error) throw new Error(`JSON-RPC: ${JSON.stringify(reply.error)}`);
    return reply.result as T;
  }

  async initialize() {
    const result = await this.rpc<{
      protocolVersion: string; serverInfo: { name: string; version: string };
    }>("initialize", {
      protocolVersion: "2025-06-18", capabilities: {},
      clientInfo: { name: "mnemosys-typescript-course", version: "1.0.0" },
    });
    if (result.protocolVersion !== "2025-06-18") throw new Error("Unsupported negotiated protocol version");
    this.version = result.protocolVersion;
    await this.rpc("notifications/initialized", undefined, true);
    return result;
  }

  async call<T = unknown>(name: string, arguments_: Record<string, string>): Promise<T> {
    const result = await this.rpc<ToolResult>("tools/call", { name, arguments: arguments_ });
    if (result.content.length !== 1 || result.content[0].type !== "text") {
      throw new Error("Unexpected tool result format");
    }
    const value = JSON.parse(result.content[0].text);
    if (result.isError) throw new Error(`Tool ${name}: ${JSON.stringify(value)}`);
    return value as T;
  }
}

async function main() {
  const profileId = process.env.MNEMOSYS_PROFILE_ID;
  if (!profileId) throw new Error("Set MNEMOSYS_PROFILE_ID to an AI profile ID");
  const client = new MCPClient(process.env.MNEMOSYS_MCP_URL ?? "http://127.0.0.1:8080/mcp", profileId);
  const path = process.argv[2] ?? "demo.md";
  console.log("Server:", (await client.initialize()).serverInfo);
  await client.rpc("ping");
  const listing = await client.rpc<{ tools: { name: string }[] }>("tools/list");
  console.log("Tools:", listing.tools.map(tool => tool.name));
  console.log("Tree:", await client.call("explore", {}));
  console.log("Search:", await client.call("search", { query: "demo", mode: "lexical" }));
  const document = await client.call<DocumentResult>("read", { path });
  console.log("Revision:", document.revision);
  console.log("Markdown:\n", document.content);
  console.log("Validation:", await client.call("validate", { path }));
}

main().catch(error => {
  console.error(error);
  process.exitCode = 1;
});
```

## 4. Utiliser les huit outils

| Outil | Arguments requis | Arguments optionnels | Résultat / effet |
| --- | --- | --- | --- |
| `explore` | Aucun | `path` | Arbre récursif du coffre ou contenu d’un dossier. |
| `search` | `query` | `mode`, `scope` | Objet avec `query`, `mode`, `results`. |
| `read` | `path` | Aucun | Document avec `content`, `revision`, `id` et métadonnées. |
| `validate` | `path` | `content` | Objet avec `valid`, `issues`, `path`. La page doit exister. |
| `update` | `path`, `content`, `baseRevision` | Aucun | Objet avec `path` et `proposal`, sans sauvegarde de la page. |
| `link` | `path`, `target`, `baseRevision` | `label` | Proposition d’ajout d’un wiki lien vers une page ou un dossier existant. |
| `create` | `path`, `type` | `content`, `pageType` | Création immédiate ; résultat avec `path`. |
| `delete` | `path` | Aucun | Suppression immédiate et récursive ; résultat avec `path`. |

`search` propose `names` (chemins de fichiers/dossiers), `lexical` (défaut,
métadonnées et contenu) et `hybrid` (classement lexical et sémantique local).
`scope` restreint la recherche à un dossier relatif au coffre. La requête est
limitée à 200 caractères ; un filtre comme `team: Platform` cible un champ de
frontmatter. `explore` sans `path` parcourt tout le coffre.

`create.type` vaut `document` ou `directory`. `pageType` vaut `general`
(défaut), `business`, `technical` ou `incident`. Le serveur crée les métadonnées.
Une IA ne peut pas activer `ai_editable` lors de la création.

### Proposer une modification et un lien

Ajouter ce bloc dans `main`, après les appels de lecture du client complet.
Il utilise la page `demo.md` préparée à l’étape 1. Lire la version actuelle,
conserver son frontmatter et passer sa `revision` comme `baseRevision`.

```typescript
  const current = await client.call<DocumentResult>("read", { path });
  const content = current.content + "\n## Proposition\n\nAjout proposé par le client MCP.\n";
  const validation = await client.call<{ valid: boolean; issues: string[] }>("validate", { path, content });
  if (!validation.valid) throw new Error(`Invalid Markdown: ${validation.issues.join(", ")}`);
  type ProposalResult = { proposal: { id: string; status: string } };
  const result = await client.call<ProposalResult>("update", {
    path, content, baseRevision: current.revision,
  });
  console.log("Proposal:", result.proposal.id, result.proposal.status);
  // Préparer une autre page target.md avec le profil humain avant cet appel.
  const linked = await client.call<ProposalResult>("link", {
    path, target: "target.md", label: "Target", baseRevision: current.revision,
  });
  console.log("Link proposal:", linked.proposal.id);
```

Pour l’appel `link`, créer aussi une page distincte `target.md` avec le profil
humain. Une page ne peut pas se lier à elle-même.

`validate` est une vérification en lecture seule : `valid: false` avec des
`issues` n’est pas une erreur de transport ou d’outil. Ici, arrêter avant de
soumettre une proposition invalide. Le serveur vérifie l’identité UUID, le type
de page et la résolution des wiki liens ; il ne juge pas la qualité du texte.

`update` et `link` demandent tous deux `view`, `edit`, `ai_editable: true` et une
révision actuelle. `content` remplace le Markdown entier, pas seulement un
fragment. `link` ajoute un lien `[[id:<uuid>|label]]` pour une page et un lien de
chemin pour un dossier. L’exemple conserve le même instantané pour les deux
propositions : elles restent indépendantes et ne changent pas la page.

Dans Mnemosys, un humain consulte le diff et fusionne ou rejette la proposition.
MCP ne fournit aucun outil de fusion. Une proposition peut rester en statut
`draft` avant sa revue ; un appel réussi ne signifie pas qu’elle est fusionnée.
Les propositions et leur historique restent en mémoire et disparaissent au
redémarrage du serveur. Une modification concurrente peut aussi bloquer leur
fusion : relire et soumettre une nouvelle proposition après revue.

### Créer puis supprimer dans un dossier d’exercice

Ajouter ce bloc dans `main` pour tester `create` et `delete` avec les permissions
correspondantes. Choisir un nom de dossier libre : les créations ne remplacent
pas les éléments existants. La suppression finale efface immédiatement ce
dossier et tous ses descendants, sans proposition ni revue humaine.

```typescript
  await client.call("create", { path: "mcp-course-sandbox", type: "directory" });
  await client.call("create", {
    path: "mcp-course-sandbox/note.md", type: "document",
    pageType: "technical", content: "# Note\n\nCréée via MCP.\n",
  });
  console.log(await client.call("read", { path: "mcp-course-sandbox/note.md" }));
  await client.call("delete", { path: "mcp-course-sandbox" });
```

## 5. Diagnostiquer les erreurs

| Symptôme | Explication / action |
| --- | --- |
| Connexion refusée ou délai dépassé | Vérifier le serveur, le port et `MNEMOSYS_MCP_URL`. Les clients limitent chaque requête à 15 secondes. |
| HTTP 403 | Le pair et le Host doivent être locaux (`127.0.0.1`, `localhost`, loopback IPv6). Un en-tête Origin, s’il existe, doit correspondre exactement à l’origine du serveur. |
| HTTP 405 | Utiliser POST sur `/mcp`, pas GET, DELETE ou un transport SSE persistant. |
| HTTP 406 / 415 | Corriger respectivement `Accept` ou `Content-Type`. |
| HTTP 400 | Vérifier l’en-tête `MCP-Protocol-Version` : seule `2025-06-18` est acceptée. |
| JSON-RPC `-32700` / `-32600` | JSON mal formé, plusieurs objets, ou requête JSON-RPC invalide. |
| JSON-RPC `-32601` / `-32602` | Méthode inconnue, outil inconnu, argument manquant, inconnu ou non textuel. Consulter `tools/list`. |
| `isError: true`, permission refusée | Vérifier l’identifiant du profil IA et ses droits ; pour `update`/`link`, vérifier aussi `ai_editable: true`. |
| `isError: true`, chemin invalide ou introuvable | Utiliser un chemin relatif au coffre, sans `..`, avec parents existants ; vérifier avec `explore`. |
| `isError: true`, document modifié | Le résultat décodé contient `currentDocument`. Relire le document, revoir votre texte et soumettre avec sa nouvelle révision. Ne pas réessayer aveuglément avec la même révision. |
| `valid: false` | Examiner `issues` : UUID manquant/modifié, type de page invalide ou lien introuvable/ambigu. |

Le point d’accès est destiné aux agents locaux de confiance. Il ne possède pas
d’authentification utilisateur et ne doit pas être exposé via un proxy distant.
Le profil transmis sélectionne des permissions, pas une identité authentifiée.

## 6. Vérifications réalisées pour ce cours

Les exemples ont été exécutés contre le serveur Go réel sur
`127.0.0.1:18080`, avec un coffre et une configuration temporaires, sans utiliser
les données habituelles du projet. Environnements : Python 3.12.3 et Node.js
24.21.0. Les contrôles ont couvert l’initialisation, la notification HTTP 202,
le ping, la découverte des huit outils, les lectures, les recherches, la
validation, la création/suppression et les propositions `update`/`link`.
La page enregistrée reste inchangée après les propositions. Les appels sans
profil IA, les chemins sortant du coffre et les révisions périmées sont refusés ;
un wiki lien manquant produit `valid: false`.

La suite backend a également été exécutée :

```bash
GOCACHE=/tmp/mnemosys-go-cache go test ./...
```

Le client TypeScript a été vérifié avec le compilateur en mode `--strict`.
Ces contrôles vérifient l’implémentation locale actuelle, pas la compatibilité
avec tous les SDK ou toutes les versions du protocole MCP.
