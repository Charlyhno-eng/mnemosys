# AGENTS.md

## Projet

Cette application est une plateforme de **mémoire et de connaissance d'entreprise**. Elle centralise la documentation et les connaissances d'une organisation pour les rendre structurées, reliées, fiables et exploitables par des humains et des agents IA. Le produit s'inspire de Confluence, Notion et Obsidian, avec pour objectif d'évoluer progressivement vers une mémoire d'entreprise comprenant : documentation collaborative Markdown, liens et relations, recherche classique puis sémantique, entités, permissions, sources et provenance, historique, gouvernance, IA, agents, API/MCP et intégrations métier.

## Philosophie

Développer **incrémentalement**. Chaque étape doit produire un produit fonctionnel avant d'ajouter la suivante. Privilégier la simplicité, éviter la sur-architecture, les abstractions prématurées et les microservices inutiles. Ne pas implémenter les fonctionnalités futures avant qu'elles soient nécessaires. Le produit doit d'abord être rapide, simple et agréable pour écrire et consulter de la documentation.

## Stack

* Backend : Go, librairie standard autant que possible
* Frontend : React router + TypeScript + Tailwind + shadcn/ui
* Database : SQLite dans un premier temps
* Documentation : fichiers Markdown
* API : HTTP/REST

La stack peut évoluer si une décision technique apporte un bénéfice significatif.

## Roadmap

### 1. Documentation Markdown

Construire un Confluence minimaliste dans le navigateur reposant sur des fichiers Markdown. L'utilisateur peut parcourir une arborescence, créer, modifier, supprimer, renommer et déplacer des documents et dossiers, écrire en Markdown et visualiser leur rendu. Pas d'authentification, permissions, IA, recherche sémantique, entités, multi-tenant ou intégrations à cette étape.

### 2. Knowledge base

Ajouter les liens entre documents, backlinks, tags, métadonnées Markdown, recherche et navigation entre connaissances.

### 3. Recherche

Ajouter l'indexation, la recherche plein texte, les filtres et le classement des résultats. Préparer l'architecture pour la recherche sémantique future.

### 4. IA

Ajouter une IA capable de rechercher dans la documentation, répondre avec des sources, résumer les documents et suggérer des liens. Les modifications générées par l'IA doivent rester explicites et contrôlables.

### Évolution future

Ajouter progressivement le knowledge graph (entités, propriétés, relations), la gouvernance (sources, provenance, validation, conflits, obsolescence, audit), les permissions, la mémoire active, les agents IA, MCP/API et les intégrations métier.

## Qualité et sécurité

Le **backend est critique**. Toute fonctionnalité backend doit être accompagnée de tests pertinents avant d'être considérée comme terminée. Tester notamment la logique métier, les erreurs, les cas limites, les accès concurrents, les entrées utilisateur et les comportements inattendus. Les tests d'intégration doivent être utilisés lorsque nécessaire. Toute vulnérabilité de sécurité, fuite de données, contournement d'autorisation, injection, corruption de données ou erreur critique doit être traitée comme bloquante. Ne jamais considérer une fonctionnalité terminée sans avoir vérifié ses cas d'erreur et ses implications de sécurité. Le code backend doit être robuste, déterministe autant que possible et maintenable.

Le frontend sera également testé lorsque nécessaire, mais la priorité initiale est la **fiabilité et la sécurité du backend**.

## Règles pour les agents

1. Comprendre l'existant avant de modifier le code.
2. Implémenter uniquement le nécessaire pour l'étape courante.
3. Privilégier la solution la plus simple.
4. Éviter les dépendances et abstractions inutiles.
5. Tester systématiquement le backend.
6. Ne jamais ignorer ou masquer une erreur.
7. Préserver les données et comportements existants.
8. Toute fonctionnalité doit fonctionner de bout en bout avant d'être considérée comme terminée.
9. Corriger les régressions immédiatement.
10. Ne pas passer à l'étape suivante tant que l'étape actuelle n'est pas stable.

## Organisation du code

```bash
project/
├── apps/
│   ├── api/
│   │   └── main.go
│   └── worker/
│       └── main.go
├── backend/
│   ├── documents/
│   │   ├── handler.go
│   │   ├── service.go
│   │   ├── repository.go
│   │   └── model.go
│   ├── search/
│   ├── users/
│   └── ...
├── db/
│   ├── migrations/
│   └── queries/
├── frontend/
│   └── src/
│       ├── components/
│       ├── pages/
│       ├── features/
│       └── lib/
├── tests/
│   └── e2e/
├── docker-compose.yml
├── go.mod
└── AGENTS.md
```

L'organisation backend est **modulaire par fonctionnalité**. Chaque module regroupe sa logique métier, ses handlers, ses modèles et son accès aux données. Éviter les architectures en couches excessivement abstraites. L'environnement sur lequel tu vas développer est un linux mint sur lequel il y a déjà SQLite d'installé.
