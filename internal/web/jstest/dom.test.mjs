import test from 'node:test';
import assert from 'node:assert/strict';
import { esc } from '../static/dom.js';

test('esc leaves plain text untouched', () => {
  assert.equal(esc('Lote Norte'), 'Lote Norte');
});

test('esc escapes every HTML-sensitive character (FR7 hostile input)', () => {
  // The spec's hostile lot name must render inert as text, never as markup.
  assert.equal(esc('<img src=x onerror=alert(1)>'), '&lt;img src=x onerror=alert(1)&gt;');
  assert.equal(esc('a&b<c>d'), 'a&amp;b&lt;c&gt;d');
  assert.equal(esc('say "hi"'), 'say &quot;hi&quot;');
  assert.equal(esc("it's"), 'it&#39;s');
});

test('esc treats null and undefined as empty string', () => {
  assert.equal(esc(null), '');
  assert.equal(esc(undefined), '');
});

test('esc does not double-escape values that are already escaped-looking', () => {
  // esc is applied at every dynamic insertion point, so a raw ampersand is
  // the only input contract — it must be escaped exactly once.
  assert.equal(esc('&amp;'), '&amp;amp;');
});