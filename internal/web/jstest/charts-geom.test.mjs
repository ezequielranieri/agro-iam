import test from 'node:test';
import assert from 'node:assert/strict';
import {
  buildLinePath,
  buildDonutSegments,
  mapMarkers,
} from '../static/charts.js';

// Geometry tests for the hand-rolled SVG widgets (FD1, D1). The aggregation
// layer is covered in charts.test.mjs; these cases pin the SVG output:
// a smooth (non-jagged) line, annular donut segments whose inner edge really
// sits on the inner radius, and deterministic map markers inside the canvas.

// ---- buildLinePath (FD1: smooth, non-jagged line) ------------------------

test('buildLinePath returns null for fewer than two points (nothing to draw)', () => {
  assert.equal(buildLinePath([], 300, 100), null);
  assert.equal(buildLinePath([{ label: 'Jan', count: 1 }], 300, 100), null);
});

test('buildLinePath draws a smooth cubic path, never a jagged polyline', () => {
  const points = [
    { label: 'Oct', count: 1 },
    { label: 'Nov', count: 3 },
    { label: 'Dec', count: 2 },
  ];
  const path = buildLinePath(points, 300, 100);
  assert.ok(path.startsWith('M '), `path must start with a move: ${path}`);
  assert.equal(path.match(/\bC /g).length, 2, 'one cubic segment per interval');
  assert.ok(!path.includes('L '), 'smooth curve must not contain line commands');
});

test('buildLinePath handles an all-zero series without NaN (flat baseline)', () => {
  const points = [
    { label: 'Jan', count: 0 },
    { label: 'Feb', count: 0 },
    { label: 'Mar', count: 0 },
  ];
  const path = buildLinePath(points, 300, 100);
  assert.ok(!path.includes('NaN'), `no NaN in ${path}`);
  // Zero count maps to the bottom of the chart: every anchor sits on the
  // baseline (height minus the 10px padding).
  const anchors = path.match(/(?:M|C) ([^ ]+) (90)(?:\s|,)/g) || [];
  assert.equal(anchors.length, 3);
});

// ---- buildDonutSegments (FD1: donut with legend) -------------------------

test('buildDonutSegments returns no segments for empty shares', () => {
  assert.deepEqual(buildDonutSegments([], 26, 40), []);
});

test('buildDonutSegments draws a single-part share as a full ring', () => {
  const segments = buildDonutSegments([{ label: 'Glifosato 48%', count: 4 }], 26, 40);
  assert.equal(segments.length, 1);
  assert.equal(segments[0].label, 'Glifosato 48%');
  assert.equal(segments[0].fraction, 1);
  // A full circle cannot be one SVG arc command: the ring must be two half
  // arcs for the outer edge and two for the inner edge.
  assert.equal(segments[0].path.match(/\bA /g).length, 4);
});

test('buildDonutSegments splits the circle proportionally with advancing angles', () => {
  const segments = buildDonutSegments([
    { label: 'Glifosato 48%', count: 2 },
    { label: 'Atrazina', count: 1 },
  ], 26, 40);
  assert.equal(segments.length, 2);
  assert.equal(segments[0].label, 'Glifosato 48%');
  assert.equal(segments[0].fraction, 2 / 3);
  assert.equal(segments[1].fraction, 1 / 3);
  assert.equal(segments[1].startAngle, (2 / 3) * 2 * Math.PI);
  const total = segments.reduce((sum, s) => sum + s.fraction, 0);
  assert.ok(Math.abs(total - 1) < 1e-9, 'fractions cover the full circle');
});

test('donut segment inner edge sits on the inner radius, outer on the outer', () => {
  const segments = buildDonutSegments([
    { label: 'A', count: 1 },
    { label: 'B', count: 1 },
  ], 26, 40);
  for (const segment of segments) {
    // Walk the SVG commands: M/L carry one point, A carries one end point
    // (the radii and flags are parameters, not coordinates).
    const tokens = segment.path.split(' ').filter((t) => t !== '');
    const points = [];
    for (let i = 0; i < tokens.length;) {
      const cmd = tokens[i];
      if (cmd === 'M' || cmd === 'L') {
        points.push([Number(tokens[i + 1]), Number(tokens[i + 2])]);
        i += 3;
      } else if (cmd === 'A') {
        points.push([Number(tokens[i + 6]), Number(tokens[i + 7])]);
        i += 8; // rx ry rot large-arc sweep x y
      } else {
        i += 1;
      }
    }
    const atRadius = (r) => points.filter(([x, y]) => {
      const dist = Math.sqrt(x * x + y * y);
      return Math.abs(dist - r) < 1e-6;
    }).length;
    // Every annular sector has exactly two points on the outer edge (the M
    // anchor and the outer arc end) and two on the inner edge (the L anchor
    // and the inner arc end). If the inner edge were computed with the outer
    // radius, no point would sit at 26 and four would sit at 40.
    assert.equal(atRadius(26), 2, `inner edge must contribute 2 points at radius 26`);
    assert.equal(atRadius(40), 2, `outer edge must contribute 2 points at radius 40`);
  }
});

// ---- mapMarkers (FD1: illustrated map with circular markers) -------------

test('mapMarkers returns no markers when nothing was applied', () => {
  assert.deepEqual(mapMarkers([], [], 300, 160), []);
  assert.deepEqual(mapMarkers([{ id: 'lot-north', name: 'Lote Norte' }], [{ lotId: 'lot-north', count: 0 }], 300, 160), []);
});

test('mapMarkers places deterministic markers inside the canvas, sized by count', () => {
  const lots = [
    { id: 'lot-north', name: 'Lote Norte - Soja' },
    { id: 'lot-south', name: 'Lote Sur - Maiz' },
  ];
  const breakdown = [
    { lotId: 'lot-north', count: 9 },
    { lotId: 'lot-south', count: 1 },
  ];
  const markers = mapMarkers(lots, breakdown, 300, 160);
  assert.equal(markers.length, 2);

  const byId = Object.fromEntries(markers.map((m) => [m.id, m]));
  assert.equal(byId['lot-north'].name, 'Lote Norte - Soja');
  assert.equal(byId['lot-north'].count, 9);
  for (const marker of markers) {
    assert.ok(marker.cx >= 16 && marker.cx <= 284, `cx ${marker.cx} inside canvas`);
    assert.ok(marker.cy >= 16 && marker.cy <= 144, `cy ${marker.cy} inside canvas`);
  }
  assert.ok(byId['lot-north'].r > byId['lot-south'].r, 'busier lot draws a bigger marker');
});

test('mapMarkers is deterministic — same input, same marker positions', () => {
  const lots = [{ id: 'lot-north', name: 'Lote Norte' }];
  const breakdown = [{ lotId: 'lot-north', count: 4 }];
  assert.deepEqual(mapMarkers(lots, breakdown, 300, 160), mapMarkers(lots, breakdown, 300, 160));
});
