# Plan d'Intégration GoRAGlite → HOROS v3.2

**Destinataire** : AutoClaude (GitHub)
**Projet** : GoRAGlite
**Objectif** : Adapter GoRAGlite pour intégration dans HOROS Zone 6 (RAG)
**Priorité** : Haute

---

## 1. Contexte : Qu'est-ce que HOROS ?

### 1.1 Vue d'Ensemble

HOROS v3.2 est un **système d'exploitation pour l'intelligence personnelle** conçu selon une architecture rigoureuse appelée "Monolithe Modulaire Fractal". C'est un système :

- **100% local** : Aucune dépendance cloud ou réseau externe
- **SQLite exclusif** : Toutes les données sont dans des fichiers `.db`
- **Multi-processus** : Fédération de binaires Go spécialisés (pas un monolithe)
- **Zero-HTTP interne** : Communication uniquement via SQLite (pas de HTTP/RPC entre workers)

### 1.2 Architecture Fondamentale

```
HOROS v3.2
├── Meta-Orchestrateur (horos)           # Binaire principal
│   ├── Pattern 4-BDD                    # 4 bases par entité
│   │   ├── horos_meta.input.db         # Commandes entrantes
│   │   ├── horos_meta.lifecycle.db     # État interne
│   │   ├── horos_meta.output.db        # Résultats
│   │   └── horos_meta.metadata.db      # Configuration
│   └── Bus Central
│       └── horos_events.db              # Communication inter-processus
│           ├── tasks                    # File d'attente FIFO
│           ├── heartbeats              # Status toutes les 15s
│           └── logs                    # Logs structurés JSON
├── Zone 1 : INIT (init_manager)
├── Zone 2 : AUDIT (audit_manager)
├── Zone 3 : STATUS (status_manager)
├── Zone 4 : COLLAB (collab_manager)    # MCP servers
├── Zone 5 : WEB (web_manager)
├── Zone 6 : RAG (rag_manager)          ← GoRAGlite ici
├── Zone 7 : PROJETS (projects_manager)
├── Zone 8 : AUTOMATION (auto_manager)
└── Zone 9 : EXITS (exit_manager)
```

### 1.3 Les 5 Dogmes Immuables (Constitution HOROS)

Ces lois sont **non-négociables** et validées par des linters custom.

#### Dogme 1 : Loi du Fichier Unique (Data-Physics)
```
"Si une donnée ne peut pas être copiée via `cp` ou supprimée via `rm`,
elle n'existe pas dans HOROS"
```

**Implications** :
- ✅ Une base = un fichier `.db`
- ✅ Contenu externe : `external_path TEXT` (chemin vers fichier)
- ❌ INTERDIT : Stockage BLOB inline (sauf petits métadonnées)
- ❌ INTERDIT : Bases distantes, S3, cloud storage

#### Dogme 2 : Loi de la Fédération de Binaires (Multi-Processus)
```
"Le système est une fédération de binaires Go autonomes,
pas un monolithe avec threads"
```

**Implications** :
- ✅ 1 binaire par Bloc Logique (rag_manager, audit_manager, etc.)
- ✅ Chaque binaire = processus OS distinct
- ✅ Crash isolation : un binaire crashé n'affecte pas les autres
- ❌ INTERDIT : Goroutines partagées entre zones

#### Dogme 3 : Loi de l'Isolation d'Écriture (One Writer Rule)
```
"Un processus détient le droit d'écriture sur UN SEUL contexte métier"
```

**Implications** :
- ✅ Un writer peut lire plusieurs bases (ATTACH READ ONLY)
- ❌ INTERDIT : `ATTACH db1, db2 en READ_WRITE simultanément`
- ✅ Pattern merger : 1 worker = 1 run DB en écriture, corpus en lecture

#### Dogme 4 : Loi de l'Intention Abstraite
```
"Une entité exprime une intention via l'API framework,
pas d'opérations bas niveau directes"
```

#### Dogme 5 : Loi de la Fractalité (Cycle de Vie Universel)
```
"Chaque binaire respecte les 9 phases du cycle de vie fractal"
```

**Phases obligatoires** :
1. **INIT** : Migrations DBs, enregistrement registry
2. **AUDIT** : Self-check (6 dimensions)
3. **STATUS** : Heartbeats 15s vers `horos_events.db.heartbeats`
9. **EXITS** : Graceful shutdown, flush WAL, désenregistrement

---

## 2. Les 9 Linters Custom HOROS

### 2.1 horos-sqlite (Critique)
**Règle** : Seul driver autorisé = `modernc.org/sqlite` (pure Go, sans CGO)
**Interdit** : `github.com/mattn/go-sqlite3` (requis CGO)

**⚠️ GoRAGlite viole cette règle**

### 2.2 horos-idempotence
**Règle** : Utiliser table `processed_log` avec hash SHA256

### 2.3 horos-attach
**Règle** : Pattern ATTACH/DETACH obligatoire avec defer

### 2.4 horos-heartbeat
**Règle** : Heartbeat toutes les 15 secondes maximum

### 2.5 horos-shutdown
**Règle** : Graceful shutdown obligatoire (<5s)

### 2.6 horos-hash-identity
**Règle** : SHA256 du contenu comme clé primaire

### 2.7 horos-pragma
**Règle** : Pragmas SQLite obligatoires (WAL, foreign_keys, etc.)

### 2.8 horos-http-forbidden
**Règle** : INTERDIT total de HTTP/RPC entre workers

### 2.9 horos-ego
**Règle** : Auto-introspection obligatoire via `pkg/egocheck`

---

## 3. Violations Actuelles de GoRAGlite

### 3.1 Violation Critique : Driver SQLite

**Fichier** : `internal/db/db.go`
```go
import _ "github.com/mattn/go-sqlite3"  // ❌ INTERDIT
```

**Correction requise** :
```go
import _ "modernc.org/sqlite"  // ✅ REQUIS
```

### 3.2 Violation Majeure : Embed Patterns

**Fichiers** : `internal/db/db.go`, `internal/workflow/loader.go`
```go
//go:embed ../../sql/schema/*.sql  // ❌ INTERDIT (.. hors package)
```

**Correction** : Créer package `assets/` à la racine

### 3.3 Violation Moyenne : BLOB Storage

**Fichier** : `sql/schema/corpus.sql`
```sql
content BLOB,           -- ❌ INTERDIT pour gros fichiers
```

**Correction** : Utiliser `external_path TEXT` + copie fichiers

### 3.4 Violation Mineure : Architecture Monolithique

1 binaire `raglite` → Séparer en `rag_manager`, `rag_pdf_worker`, etc.

---

## 4. Plan d'Action

### Phase 1 : Corrections Critiques (Semaine 1)

#### Tâche 1.1 : Remplacer Driver SQLite
```bash
go mod edit -droprequire github.com/mattn/go-sqlite3
go mod edit -require modernc.org/sqlite@v1.28.0
```

#### Tâche 1.2 : Fixer Embed Patterns
Créer `assets/assets.go` :
```go
package assets

import "embed"

//go:embed sql/schema/*.sql
var SchemaFS embed.FS

//go:embed sql/workflows/*.sql
var WorkflowsFS embed.FS
```

#### Tâche 1.3 : BLOB → External Path
```go
// Copier fichier vers storage
storageDir := filepath.Join(dataDir, "storage", "raw", checksum[:2])
externalPath := filepath.Join(storageDir, checksum)
copyFile(sourcePath, externalPath)

// Stocker chemin au lieu du contenu
INSERT INTO raw_files (external_path, checksum, ...) VALUES (?, ?, ...)
```

### Phase 2 : Architecture HOROS (Semaine 2)

#### Tâche 2.1 : Pattern 4-BDD
```
/data/horos/knowledge/
├── rag_input.db          # Interface bus
├── rag_lifecycle.db      # Tables RAG core
├── rag_output.db         # Résultats
└── rag_metadata.db       # Workflows + telemetry
```

#### Tâche 2.2 : Bus Central Integration
Créer `internal/horosbus/bus.go` :
- `ClaimTask()`, `CompleteTask()`, `FailTask()`
- `SendHeartbeat()` toutes les 15s
- `Log()` structuré JSON

#### Tâche 2.3 : RAG Manager Binary
```go
// cmd/rag_manager/main.go
// - Main loop avec heartbeats 15s
// - Consommation tasks depuis bus
// - Graceful shutdown
```

#### Tâche 2.4 : Ego Check
```go
manifest, _ := egocheck.Run("rag_manager", srcFiles, dbFiles)
egocheck.StoreInDB(db, manifest)
```

### Phase 3 : Tests et Validation (Semaine 3)

- Tests unitaires Go (>80% coverage)
- Tests E2E HOROS
- `mage lint` (9 linters)
- Documentation

---

## 5. Critères de Succès

```bash
# Tests SQL
cd test/sql && bash run_tests.sh
# Résultat: 4/4 PASSED

# Linters HOROS
mage lint
# Résultat: 9/9 OK

# Heartbeat visible
sqlite3 horos_events.db "SELECT * FROM heartbeats WHERE zone='RAG'"
# Résultat: row avec last_beat < 15s ago

# Graceful shutdown
kill -SIGTERM $(pgrep rag_manager)
# Résultat: exit propre <5s
```

---

## 6. Livrables

- [ ] Driver SQLite remplacé (`modernc.org/sqlite`)
- [ ] Embed patterns fixés (package `assets/`)
- [ ] BLOB → External path migration
- [ ] Pattern 4-BDD implémenté
- [ ] Bus HOROS integration
- [ ] Binaire `rag_manager` avec heartbeats
- [ ] Ego check intégré
- [ ] Graceful shutdown
- [ ] Tests SQL 4/4 passent
- [ ] Tests Go (>80% coverage)
- [ ] `mage lint` passe (9 linters)

---

## 7. Contraintes NON-NÉGOCIABLES

1. **Driver SQLite pure Go** : `modernc.org/sqlite` uniquement
2. **Pas de content BLOB** pour fichiers > quelques KB
3. **Heartbeats OBLIGATOIRES** toutes les 15s
4. **Graceful shutdown** <5s
5. **Pattern ATTACH/DETACH** avec defer
6. **Ego check** au démarrage
