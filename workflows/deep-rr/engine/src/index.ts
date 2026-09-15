// Dev / test entry — NOT bundled. The bundler (build.js) concatenates the modules in
// ORDER and appends the harness tail `const rr = new ResearchReport(); return await rr.run()`
// itself; this file only re-exports the surfaces the tests import. Importing it must NOT
// auto-run the engine (it only re-exports).
export { meta } from './meta.js';
export { ResearchReport } from './engine.js';
