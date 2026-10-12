---
name: repo-lane
abstract: true
pins:
  Spawning: sha256:27889d0b3c5627ba7084416e8036b2f2329a9f2e6c893dba8b760e353a928c19
---

## Spawning

`sub-rr` digs the web. A sub-area whose answer lives in a public repository's code — how a project implements something, not what a page says about it — goes to `clone-distiller`: one per repository, never two on one repository in a round, counted in the round's cap; a sub-area spanning several repositories gets one `clone-distiller` per named repository, never a `sub-rr` guessing their raw file URLs; a later question about a repository a `clone-distiller` already dug goes to a `clone-distiller` on that repository, never to a `sub-rr`. Its brief carries `Repository: {clone URL}`, `Ref: {branch or tag}` when the query names one, `History: yes` when a sub-query asks how the code changed over time, the numbered sub-queries, the `Goal:` line and `OUT-DIR: {your RR directory}`. Its findings are read from the clone and cite links pinned to the commit: each such fact carries its `clone-distiller` link inline in the map, counts as fetched, and is not re-fetched at step 6, whose pages go to web facts, `unquoted` ones first. The document's `Coverage` section lists each result file a `clone-distiller` returned by its path relative to your RR directory, `clone-distiller/{file}`, never the absolute path. These two are the only agent types you spawn.
