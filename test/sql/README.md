# GoRAGlite SQL Tests

Tests SQL autonomes exécutables directement avec `sqlite3`. Aucune dépendance Go requise.

## Prérequis

```bash
# Ubuntu/Debian
apt-get install sqlite3

# macOS
brew install sqlite3

# Vérifier
sqlite3 --version
```

## Exécution rapide

```bash
cd test/sql
bash run_tests.sh
```

## Tests disponibles

| Test | Description | Vérifie |
|------|-------------|---------|
| `test_corpus_schema.sql` | Schéma corpus.db | Tables, FTS, FK, CHECK constraints |
| `test_workflows_schema.sql` | Schéma workflows.db | 12 workflows, 110 steps |
| `test_run_schema.sql` | Schéma run éphémère | Métadonnées, deltas, vues |
| `test_e2e_workflow.sql` | Pipeline complet | Ingest → chunks → vectors → merge |

## Exécution individuelle

```bash
# Créer une DB temporaire et exécuter un test
sqlite3 /tmp/test.db < test_corpus_schema.sql

# Ou inspecter interactivement
sqlite3 /tmp/test.db
sqlite> .read test_corpus_schema.sql
```

## Structure d'un test

Chaque test suit ce pattern :

```sql
.bail on           -- Arrêter sur erreur inattendue
.headers on
.mode column

-- TEST N: Description
.print "=== TEST N: Description ==="
-- ... SQL statements ...

-- Pour tester des erreurs attendues
.bail off
-- statement qui doit échouer
SELECT CASE WHEN ... THEN 'OK' ELSE 'ERROR' END;

-- Marqueur de succès (requis)
.print "=== ALL ... TESTS PASSED ==="
```

## Validation des contraintes

Les tests vérifient que les contraintes SQL fonctionnent :

```sql
-- FK constraint (doit échouer)
INSERT INTO chunks (file_id, ...) VALUES ('nonexistent', ...);
-- → FOREIGN KEY constraint failed

-- CHECK constraint (doit échouer)
UPDATE raw_files SET status = 'invalid';
-- → CHECK constraint failed
```

## Résultat attendu

```
========================================
GoRAGlite SQL Test Suite
========================================

Running: test_corpus_schema
=== TEST 1: Schema Creation ===
...
=== ALL CORPUS SCHEMA TESTS PASSED ===
✓ PASSED: test_corpus_schema

...

TEST SUMMARY
========================================
Passed: 4
Failed: 0

All tests passed!
```

## Ajouter un nouveau test

1. Créer `test_<name>.sql` dans ce dossier
2. Suivre le pattern avec `.bail on/off` et marqueur de succès
3. Ajouter l'appel dans `run_tests.sh` :

```bash
echo "----------------------------------------"
echo "Test N: Description"
echo "----------------------------------------"
run_test "test_<name>.sql"
```

## Debugging

```bash
# Voir toutes les tables créées
sqlite3 /tmp/test.db ".tables"

# Inspecter une table
sqlite3 /tmp/test.db "SELECT * FROM chunks LIMIT 5"

# Voir le schéma
sqlite3 /tmp/test.db ".schema chunks"

# Mode verbose
sqlite3 /tmp/test.db ".echo on" < test_corpus_schema.sql
```

## CI Integration

```yaml
# GitHub Actions
- name: Run SQL Tests
  run: |
    apt-get install -y sqlite3
    cd test/sql && bash run_tests.sh
```

## Fichiers

```
test/sql/
├── README.md                  # Cette documentation
├── run_tests.sh               # Runner bash
├── test_corpus_schema.sql     # Tests corpus.db
├── test_workflows_schema.sql  # Tests workflows.db
├── test_run_schema.sql        # Tests run.db template
└── test_e2e_workflow.sql      # Test end-to-end
```
