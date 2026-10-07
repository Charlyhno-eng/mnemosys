# Appeler le MCP Mnemosys en Python

Voir aussi le [même cours en TypeScript](mcp-typescript.md).

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

## 3. Écrire le client Python

Python 3.10 ou plus récent suffit ; le client utilise uniquement la bibliothèque standard.

Enregistrer le bloc ci-dessous dans `client.py`, puis lancer dans un autre terminal :

```bash
export MNEMOSYS_MCP_URL='http://127.0.0.1:8080/mcp'
export MNEMOSYS_PROFILE_ID='<id du profil IA>'
python3 client.py demo.md
```

Remplacer les valeurs par celles de votre instance. Le chemin peut aussi être
`guides/demo.md`. Sans argument, le client lit `demo.md`. Ce programme complet
ne fait que des lectures et affiche le serveur `mnemosys`, les huit outils,
l’arbre, les résultats de recherche, le Markdown et sa révision, puis
`valid: true` et une liste `issues` vide pour la page d’exercice valide.

```python
import json
import os
import sys
from urllib.error import HTTPError
from urllib.request import Request, urlopen


class MCPClient:
    def __init__(self, url, profile_id):
        self.url = url
        self.profile_id = profile_id
        self.version = None
        self.request_id = 0

    def rpc(self, method, params=None, notification=False):
        payload = {"jsonrpc": "2.0", "method": method}
        if params is not None:
            payload["params"] = params
        if not notification:
            self.request_id += 1
            payload["id"] = self.request_id
        headers = {
            "Content-Type": "application/json",
            "Accept": "application/json, text/event-stream",
            "X-Mnemosys-Profile-ID": self.profile_id,
        }
        if self.version:
            headers["MCP-Protocol-Version"] = self.version
        request = Request(self.url, data=json.dumps(payload).encode("utf-8"),
                          headers=headers, method="POST")
        try:
            with urlopen(request, timeout=15) as response:
                status, body = response.status, response.read().decode("utf-8")
        except HTTPError as error:
            raise RuntimeError(f"HTTP {error.code}: {error.read().decode('utf-8')}") from error
        if notification:
            if status != 202:
                raise RuntimeError(f"Expected HTTP 202, received {status}")
            return None
        reply = json.loads(body)
        if reply.get("jsonrpc") != "2.0" or reply.get("id") != payload["id"]:
            raise RuntimeError("Invalid JSON-RPC response")
        if "error" in reply:
            raise RuntimeError(f"JSON-RPC: {reply['error']}")
        return reply["result"]

    def initialize(self):
        result = self.rpc("initialize", {
            "protocolVersion": "2025-06-18", "capabilities": {},
            "clientInfo": {"name": "mnemosys-python-course", "version": "1.0.0"},
        })
        if result["protocolVersion"] != "2025-06-18":
            raise RuntimeError("Unsupported negotiated protocol version")
        self.version = result["protocolVersion"]
        self.rpc("notifications/initialized", notification=True)
        return result

    def call(self, name, arguments):
        result = self.rpc("tools/call", {"name": name, "arguments": arguments})
        content = result["content"]
        if len(content) != 1 or content[0]["type"] != "text":
            raise RuntimeError("Unexpected tool result format")
        value = json.loads(content[0]["text"])
        if result.get("isError", False):
            raise RuntimeError(f"Tool {name}: {json.dumps(value, ensure_ascii=False)}")
        return value


def main():
    client = MCPClient(os.environ.get("MNEMOSYS_MCP_URL", "http://127.0.0.1:8080/mcp"),
                       os.environ["MNEMOSYS_PROFILE_ID"])
    path = sys.argv[1] if len(sys.argv) > 1 else "demo.md"
    print("Server:", client.initialize()["serverInfo"])
    client.rpc("ping")
    print("Tools:", [tool["name"] for tool in client.rpc("tools/list")["tools"]])
    print("Tree:", client.call("explore", {}))
    print("Search:", client.call("search", {"query": "demo", "mode": "lexical"}))
    document = client.call("read", {"path": path})
    print("Revision:", document["revision"])
    print("Markdown:\n", document["content"])
    print("Validation:", client.call("validate", {"path": path}))


if __name__ == "__main__":
    main()
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

```python
    document = client.call("read", {"path": path})
    content = document["content"] + "\n## Proposition\n\nAjout proposé par le client MCP.\n"
    validation = client.call("validate", {"path": path, "content": content})
    if not validation["valid"]:
        raise RuntimeError(f"Invalid Markdown: {validation['issues']}")
    result = client.call("update", {
        "path": path, "content": content, "baseRevision": document["revision"],
    })
    print("Proposal:", result["proposal"]["id"], result["proposal"]["status"])
    # Préparer une autre page target.md avec le profil humain avant cet appel.
    linked = client.call("link", {
        "path": path, "target": "target.md", "label": "Target",
        "baseRevision": document["revision"],
    })
    print("Link proposal:", linked["proposal"]["id"])
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

```python
    client.call("create", {"path": "mcp-course-sandbox", "type": "directory"})
    client.call("create", {
        "path": "mcp-course-sandbox/note.md", "type": "document",
        "pageType": "technical", "content": "# Note\n\nCréée via MCP.\n",
    })
    print(client.call("read", {"path": "mcp-course-sandbox/note.md"}))
    client.call("delete", {"path": "mcp-course-sandbox"})
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
