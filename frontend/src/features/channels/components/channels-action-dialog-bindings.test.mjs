import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import ts from 'typescript';

// Vite and transpileModule erase types without checking identifier bindings.
// A merge can therefore leave JSX and submit handlers referencing missing
// state while builds and source-text tests still pass. Check those bindings
// against the real dialog and imports without requiring providers or a server.
test('channel dialog has no unresolved names in its form or submit handlers', () => {
  const configPath = fileURLToPath(new URL('../../../../tsconfig.app.json', import.meta.url));
  const config = ts.readConfigFile(configPath, ts.sys.readFile);
  assert.equal(config.error, undefined);
  const parsed = ts.parseJsonConfigFileContent(config.config, ts.sys, fileURLToPath(new URL('../../../../', import.meta.url)));
  assert.equal(parsed.errors.length, 0);
  const dialogPath = fileURLToPath(new URL('./channels-action-dialog.tsx', import.meta.url));
  const program = ts.createProgram([dialogPath], { ...parsed.options, noEmit: true, incremental: false });
  const dialog = program.getSourceFile(dialogPath);
  assert.ok(dialog);

  // Keep this regression focused on names that would throw ReferenceError;
  // other, pre-existing project type errors are outside this test's scope.
  const missingNames = new Set([2304, 2552, 18004]);
  const errors = program.getSemanticDiagnostics(dialog).filter((diagnostic) => missingNames.has(diagnostic.code));
  assert.deepEqual(
    errors.map((diagnostic) => ts.flattenDiagnosticMessageText(diagnostic.messageText, '\n')),
    []
  );
});
