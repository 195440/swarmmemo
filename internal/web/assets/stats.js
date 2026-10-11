// /stats readouts: a progressive enhancement of the server-rendered charts.
// Each .chart-plot carries its x labels (data-x) and each series its
// formatted values (data-values) and, for a line, its heights (data-ys, in
// percent from the top). Hovering, tapping or focusing a plot shows a
// crosshair, a dot on each line and every series' value at that x; the arrow
// keys, Home and End move it. Without this script the legends and the All
// numbers tables carry the numbers.
(() => {
  'use strict';
  // A link to All numbers, or to a table inside it, opens the block.
  const reveal = () => {
    let id;
    try {
      id = decodeURIComponent(location.hash.slice(1));
    } catch {
      return; // A malformed fragment (#%) must not stop the readouts below.
    }
    const target = id && document.getElementById(id);
    const block = target && target.closest('details');
    if (block && !block.open) {
      block.open = true;
      target.scrollIntoView();
    }
  };
  window.addEventListener('hashchange', reveal);
  reveal();
  const make = (tag, cls, parent) => {
    const el = document.createElement(tag);
    el.className = cls;
    parent.appendChild(el);
    return el;
  };
  for (const plot of document.querySelectorAll('.chart-plot[data-x]')) {
    const svg = plot.querySelector('svg.chart-svg');
    if (!svg) continue;
    const xs = plot.dataset.x.split('|');
    const n = xs.length;
    const series = [...svg.querySelectorAll('[data-values]')].map((el) => ({
      label: el.dataset.label,
      cls: (el.getAttribute('class') || '').split(' ').find((c) => /^[sb]-/.test(c)) || '',
      area: el.classList.contains('band'),
      values: el.dataset.values.split('|'),
      ys: el.dataset.ys ? el.dataset.ys.split('|').map(Number) : null,
    }));
    if (!n || !series.length) continue;
    plot.tabIndex = 0;
    plot.classList.add('has-readout');
    const guide = make('span', 'chart-guide', plot);
    const dots = series.map((s) => (s.ys ? make('span', 'chart-dot ' + s.cls, plot) : null));
    const box = make('div', 'chart-readout', plot);
    box.setAttribute('aria-hidden', 'true');
    // The plot is role=img, so screen readers skip what changes inside it:
    // the day's values are announced from a status line beside the plot.
    const said = make('span', 'sr-only', plot.parentNode);
    said.setAttribute('role', 'status');
    const off = () => {
      plot.classList.remove('is-reading');
    };
    let at = n - 1;
    const show = (i) => {
      at = Math.max(0, Math.min(n - 1, i));
      const p = plot.getBoundingClientRect();
      const r = svg.getBoundingClientRect();
      const x = r.left - p.left + (n > 1 ? (at / (n - 1)) * r.width : r.width / 2);
      guide.style.left = x + 'px';
      guide.style.top = r.top - p.top + 'px';
      guide.style.height = r.height + 'px';
      series.forEach((s, k) => {
        if (!dots[k]) return;
        dots[k].style.left = x + 'px';
        dots[k].style.top = r.top - p.top + (s.ys[at] / 100) * r.height + 'px';
      });
      box.replaceChildren();
      make('b', 'chart-readout-x', box).textContent = xs[at] + (at === n - 1 ? ' (so far)' : '');
      for (const s of series) {
        const row = make('span', 'chart-readout-row', box);
        make('span', 'chart-readout-key ' + (s.area ? 'band ' : s.cls ? 'line ' : 'none ') + s.cls, row);
        make('span', 'chart-readout-label', row).textContent = s.label;
        make('span', 'chart-readout-value', row).textContent = s.values[at];
      }
      plot.classList.add('is-reading');
      const w = box.offsetWidth;
      box.style.left = Math.max(0, Math.min(p.width - w, x - w / 2)) + 'px';
      // Above the plot by default; below it when the space above is off
      // screen, under the sticky contents bar, or outside the chart (over
      // the headline tiles or the chart before it).
      const bar = document.querySelector('.stats-toc');
      const chart = plot.closest('figure');
      const top = Math.max(0, bar ? bar.getBoundingClientRect().bottom : 0, chart ? chart.getBoundingClientRect().top : 0);
      box.classList.toggle('below', p.top - box.offsetHeight - 8 < top);
      said.textContent = xs[at] + (at === n - 1 ? ' (so far)' : '') + ': ' + series.map((s) => s.label + ' ' + s.values[at]).join(', ');
    };
    const fromPointer = (e) => {
      const r = svg.getBoundingClientRect();
      if (r.width <= 0) return;
      show(Math.round(((e.clientX - r.left) / r.width) * (n - 1)));
    };
    plot.addEventListener('pointermove', fromPointer);
    // A tap focuses the plot, so its readout stays until the next tap elsewhere.
    plot.addEventListener('pointerdown', (e) => {
      if (e.pointerType !== 'mouse') plot.focus({preventScroll: true});
      fromPointer(e);
    });
    plot.addEventListener('pointerleave', () => {
      if (document.activeElement !== plot) off();
    });
    // The readout is placed in pixels: a resize while it shows would leave
    // it at the old width (and the page scrolling sideways), so place it again.
    new ResizeObserver(() => {
      if (plot.classList.contains('is-reading')) show(at);
    }).observe(plot);
    plot.addEventListener('focus', () => show(at));
    plot.addEventListener('blur', off);
    plot.addEventListener('keydown', (e) => {
      const step = { ArrowLeft: -1, ArrowRight: 1 }[e.key];
      if (step) show(at + step);
      else if (e.key === 'Home') show(0);
      else if (e.key === 'End') show(n - 1);
      else if (e.key === 'Escape') off();
      else return;
      e.preventDefault();
    });
  }
})();
