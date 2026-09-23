**Title:** SQL As Understood By SQLite
**Published:** 2024-04-01

---

Small. Fast. Reliable. Choose any three.

Choose any three.

SQLite understands most of the standard SQL language. But it does [omit some features](omitted.html) while at the same time adding a few features of its own. This document attempts to describe precisely what parts of the SQL language SQLite does and does not support. A list of [SQL keywords](lang_keywords.html) is also provided. The SQL language syntax is described by [syntax diagrams](syntaxdiagrams.html).

The following syntax documentation topics are available:

|  |
|---|
| [aggregate functions](lang_aggfunc.html) [ALTER TABLE](lang_altertable.html) [ANALYZE](lang_analyze.html) [ATTACH DATABASE](lang_attach.html) [BEGIN TRANSACTION](lang_transaction.html) [comment](lang_comment.html) [COMMIT TRANSACTION](lang_transaction.html) [core functions](lang_corefunc.html) [CREATE INDEX](lang_createindex.html) [CREATE TABLE](lang_createtable.html) [CREATE TRIGGER](lang_createtrigger.html) [CREATE VIEW](lang_createview.html) [CREATE VIRTUAL TABLE](lang_createvtab.html) [date and time functions](lang_datefunc.html) [DELETE](lang_delete.html) [DETACH DATABASE](lang_detach.html) [DROP INDEX](lang_dropindex.html) [DROP TABLE](lang_droptable.html) [DROP TRIGGER](lang_droptrigger.html) [DROP VIEW](lang_dropview.html) [END TRANSACTION](lang_transaction.html) [EXPLAIN](lang_explain.html) [expression](lang_expr.html) [INDEXED BY](lang_indexedby.html) [INSERT](lang_insert.html) [JSON functions](json1.html) [keywords](lang_keywords.html) [math functions](lang_mathfunc.html) [ON CONFLICT clause](lang_conflict.html) [PRAGMA](pragma.html#syntax) [REINDEX](lang_reindex.html) [RELEASE SAVEPOINT](lang_savepoint.html) [REPLACE](lang_replace.html) [RETURNING clause](lang_returning.html) [ROLLBACK TRANSACTION](lang_transaction.html) [SAVEPOINT](lang_savepoint.html) [SELECT](lang_select.html) [UPDATE](lang_update.html) [UPSERT](lang_upsert.html) [VACUUM](lang_vacuum.html) [window functions](windowfunctions.html) [WITH clause](lang_with.html) |

The routines [sqlite3_prepare_v2()](c3ref/prepare.html), [sqlite3_prepare()](c3ref/prepare.html), [sqlite3_prepare16()](c3ref/prepare.html), [sqlite3_prepare16_v2()](c3ref/prepare.html), [sqlite3_exec()](c3ref/exec.html), and [sqlite3_get_table()](c3ref/free_table.html) accept an SQL statement list (sql-stmt-list) which is a semicolon-separated list of statements.

[sql-stmt-list:](syntax/sql-stmt-list.html)

Each SQL statement in the statement list is an instance of the following:

[sql-stmt:](syntax/sql-stmt.html)

*This page was last updated on 2024-04-01 12:41:31Z*
