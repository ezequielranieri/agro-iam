// dom.js — DOM helpers. esc() is the ONLY path dynamic text reaches the DOM
// (FR7): every value inserted by the views flows through it, so hostile names
// like '<img src=x onerror=alert(1)>' render inert as text. el() builds
// elements and escapes all text children through esc() — raw markup strings
// are never interpolated anywhere in the app.
const ESC = {
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;',
  "'": '&#39;',
};

// esc escapes every HTML-sensitive character in a value. null/undefined become
// the empty string so optional fields never render the literal "null".
export function esc(value) {
  return String(value ?? '').replace(/[&<>"']/g, (ch) => ESC[ch]);
}

// el creates an element from a tag, attributes and children. SVG tags are
// created in the SVG namespace so the hand-rolled charts (D1) actually
// render — document.createElement would produce HTML-namespace elements that
// the browser refuses to draw. Text children are escaped with esc(); an
// existing element child is appended as-is (esc already happened at its own
// insertion point). Event handlers are bound with addEventListener for
// attributes named on* whose value is a function.
const SVG_NS = 'http://www.w3.org/2000/svg';
const SVG_TAGS = new Set(['svg', 'path', 'circle', 'rect', 'line', 'text', 'g']);

export function el(tag, attrs = {}, ...children) {
  const node = SVG_TAGS.has(tag)
    ? document.createElementNS(SVG_NS, tag)
    : document.createElement(tag);
  for (const [name, value] of Object.entries(attrs ?? {})) {
    if (value === null || value === undefined) continue;
    if (name === 'class') {
      // setAttribute works for HTML and SVG alike (SVGElement.className is
      // an SVGAnimatedString in some engines, so a direct assignment would
      // silently fail there).
      node.setAttribute('class', String(value));
    } else if (name.startsWith('on') && typeof value === 'function') {
      node.addEventListener(name.slice(2), value);
    } else {
      node.setAttribute(name, String(value));
    }
  }
  for (const child of children) {
    if (child === null || child === undefined) continue;
    node.append(child.nodeType ? child : document.createTextNode(esc(child)));
  }
  return node;
}